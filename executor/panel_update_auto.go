package executor

import (
	"fmt"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/naibabiji/server-panel/config"
	"github.com/naibabiji/server-panel/database"
)

const (
	autoUpdateCheckInterval   = 10 * time.Minute
	autoUpdateFetchInterval   = 24 * time.Hour
	autoUpdateFailureCooldown = 24 * time.Hour
)

var panelUpdateAutoStarted sync.Once

// StartPanelAutoUpdateScheduler starts the background policy-gated
// auto-update loop. Safe to call multiple times; only the first call takes
// effect. Auto-update itself is opt-in (settings key
// panel_auto_update_enabled, default "false") — this just runs the periodic
// check so the toggle can be flipped live without a restart.
func StartPanelAutoUpdateScheduler(currentVersion, configPath string, cfg *config.Config) {
	panelUpdateAutoStarted.Do(func() {
		go func() {
			time.Sleep(2 * time.Minute)
			runPanelAutoUpdateCheck(currentVersion, configPath, cfg)
			ticker := time.NewTicker(autoUpdateCheckInterval)
			defer ticker.Stop()
			for range ticker.C {
				runPanelAutoUpdateCheck(currentVersion, configPath, cfg)
			}
		}()
	})
}

func runPanelAutoUpdateCheck(currentVersion, configPath string, cfg *config.Config) {
	if currentVersion == "" || currentVersion == "dev" {
		return
	}
	if readUpdateSetting("panel_auto_update_enabled") != "true" {
		return
	}
	if !withinAutoUpdateWindow(readUpdateSetting("panel_auto_update_window")) {
		return
	}
	if readUpdateSetting("panel_auto_update_last_status") == "failed" {
		if t, err := time.Parse(time.RFC3339, readUpdateSetting("panel_auto_update_last_attempt_at")); err == nil {
			if time.Since(t) < autoUpdateFailureCooldown {
				return
			}
		}
	}

	waitingVersion := readUpdateSetting("panel_auto_update_signature_wait_version")
	waitingAt := readUpdateSetting("panel_auto_update_signature_wait_at")
	lastCheck := readUpdateSetting("panel_auto_update_last_check_at")

	shouldFetch := waitingVersion != "" // retry an in-flight wait state on every tick
	if !shouldFetch {
		if lastCheck == "" {
			shouldFetch = true
		} else if t, err := time.Parse(time.RFC3339, lastCheck); err == nil && time.Since(t) >= autoUpdateFetchInterval {
			shouldFetch = true
		}
	}
	if !shouldFetch {
		return
	}
	setUpdateSetting("panel_auto_update_last_check_at", time.Now().UTC().Format(time.RFC3339))

	release, err := FetchLatestPanelRelease()
	if err != nil {
		log.Printf("自动更新检查失败: %v", err)
		return
	}
	if CompareVersions(release.TagName, currentVersion) <= 0 {
		return
	}
	if !IsStableVersion(release.TagName) {
		return // never auto-install a prerelease
	}

	mode := readUpdateSetting("panel_auto_update_mode")
	if mode == "" {
		mode = "patch_only"
	}
	if mode == "patch_only" && !IsPatchBump(currentVersion, release.TagName) {
		return // minor/major bumps always require a manual click
	}

	if waitingVersion != "" && waitingVersion != release.TagName {
		waitingVersion = ""
		waitingAt = ""
	}
	if waitingVersion == "" {
		waitingVersion = release.TagName
		waitingAt = time.Now().UTC().Format(time.RFC3339)
		setUpdateSetting("panel_auto_update_signature_wait_version", waitingVersion)
		setUpdateSetting("panel_auto_update_signature_wait_at", waitingAt)
	}

	delayMinutes := atoiDefault(readUpdateSetting("panel_auto_update_release_delay_minutes"), 15)
	waitStart, waitErr := time.Parse(time.RFC3339, waitingAt)
	if waitErr == nil && time.Since(waitStart) < time.Duration(delayMinutes)*time.Minute {
		return // release still within its "maturity" window
	}

	timeoutMinutes := atoiDefault(readUpdateSetting("panel_auto_update_signature_timeout_minutes"), 120)
	if waitErr == nil && time.Since(waitStart) > time.Duration(timeoutMinutes)*time.Minute {
		log.Printf("自动更新: 版本 %s 等待签名文件超时，放弃本次自动更新", release.TagName)
		setUpdateSetting("panel_auto_update_signature_wait_version", "")
		setUpdateSetting("panel_auto_update_signature_wait_at", "")
		setAutoUpdateResult("failed", "等待发布签名文件超时", release.TagName)
		notifyAutoUpdateOutcome(false, release.TagName, "等待发布签名文件超时，已放弃本次自动更新")
		return
	}

	_, _, sigName, err := PanelAssetNames()
	if err != nil {
		return
	}
	if FindAssetURL(release, sigName) == "" {
		return // signature not published yet; keep waiting within the timeout above
	}

	setUpdateSetting("panel_auto_update_signature_wait_version", "")
	setUpdateSetting("panel_auto_update_signature_wait_at", "")

	if err := ExecutePanelUpdate(PanelUpdateOptions{
		CurrentVersion: currentVersion,
		ConfigPath:     configPath,
		Config:         cfg,
		Trigger:        "auto",
		UseWatchdog:    true,
	}); err != nil {
		log.Printf("自动更新未执行: %v", err)
		setAutoUpdateResult("failed", err.Error(), release.TagName)
		notifyAutoUpdateOutcome(false, release.TagName, err.Error())
	}
}

func withinAutoUpdateWindow(window string) bool {
	parts := strings.SplitN(window, "-", 2)
	if len(parts) != 2 {
		return true // malformed window config shouldn't permanently block auto-update
	}
	start, okStart := parseClock(parts[0])
	end, okEnd := parseClock(parts[1])
	if !okStart || !okEnd {
		return true
	}
	now := time.Now()
	cur := now.Hour()*60 + now.Minute()
	if start <= end {
		return cur >= start && cur < end
	}
	return cur >= start || cur < end // window wraps past midnight
}

func parseClock(s string) (minutes int, ok bool) {
	parts := strings.SplitN(strings.TrimSpace(s), ":", 2)
	if len(parts) != 2 {
		return 0, false
	}
	h, err1 := strconv.Atoi(parts[0])
	m, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil || h < 0 || h > 23 || m < 0 || m > 59 {
		return 0, false
	}
	return h*60 + m, true
}

func atoiDefault(s string, def int) int {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return def
	}
	return n
}

func readUpdateSetting(key string) string {
	db := database.GetDB()
	if db == nil {
		return ""
	}
	var v string
	_ = db.QueryRow("SELECT svalue FROM settings WHERE skey = ?", key).Scan(&v)
	return v
}

func setUpdateSetting(key, value string) {
	db := database.GetDB()
	if db == nil {
		return
	}
	_, _ = db.Exec("INSERT OR REPLACE INTO settings (skey, svalue) VALUES (?, ?)", key, value)
}

func setAutoUpdateRunning(stage, message, targetVersion string) {
	_ = message
	now := time.Now().UTC().Format(time.RFC3339)
	setUpdateSetting("panel_auto_update_last_attempt_at", now)
	setUpdateSetting("panel_auto_update_last_status", "running")
	setUpdateSetting("panel_auto_update_last_stage", stage)
	setUpdateSetting("panel_auto_update_last_target_version", targetVersion)
	setUpdateSetting("panel_auto_update_last_error", "")
}

func setAutoUpdateResult(status, errMsg, targetVersion string) {
	now := time.Now().UTC().Format(time.RFC3339)
	setUpdateSetting("panel_auto_update_last_attempt_at", now)
	setUpdateSetting("panel_auto_update_last_status", status)
	setUpdateSetting("panel_auto_update_last_target_version", targetVersion)
	setUpdateSetting("panel_auto_update_last_error", errMsg)
	if status == "success" {
		setUpdateSetting("panel_auto_update_last_stage", "health_check")
		setUpdateSetting("panel_auto_update_last_success_at", now)
		setUpdateSetting("panel_auto_update_last_success_version", targetVersion)
	}
}

func notifyAutoUpdateOutcome(success bool, version, detail string) {
	to := readUpdateSetting("admin_email")
	if to == "" {
		return
	}
	subject := "面板自动更新成功: " + version
	body := fmt.Sprintf("面板已自动更新到 %s，健康检查通过。", version)
	if !success {
		subject = "面板自动更新失败: " + version
		body = fmt.Sprintf("面板自动更新到 %s 失败，已自动回滚。\n\n%s", version, detail)
	}
	if err := SendMail(to, subject, body); err != nil {
		log.Printf("发送自动更新通知邮件失败: %v", err)
	}
}
