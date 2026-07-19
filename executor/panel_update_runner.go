package executor

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/naibabiji/server-panel/config"
	"github.com/naibabiji/server-panel/database"
)

const (
	defaultPanelBinaryPath = "/usr/local/bin/server-panel"
	panelBinaryBackupKeep  = 5
	panelDBBackupKeep      = 7
)

var errWaitingSignature = fmt.Errorf("等待发布签名文件")

type PanelUpdateOptions struct {
	CurrentVersion string
	ConfigPath     string
	Config         *config.Config
	Trigger        string // "manual" or "auto"
	UseWatchdog    bool
}

type PanelUpdateStatus struct {
	Running         bool   `json:"running"`
	Stage           string `json:"stage"`
	Message         string `json:"message"`
	Percent         int    `json:"percent"`
	TargetVersion   string `json:"target_version"`
	DownloadedBytes int64  `json:"downloaded_bytes"`
	TotalBytes      int64  `json:"total_bytes"`
	Completed       bool   `json:"completed"`
	Error           string `json:"error"`
}

var (
	panelUpdateMu       sync.Mutex
	panelUpdateStatusMu sync.RWMutex
	panelUpdateStatus   PanelUpdateStatus
)

func SnapshotPanelUpdateStatus() PanelUpdateStatus {
	panelUpdateStatusMu.RLock()
	defer panelUpdateStatusMu.RUnlock()
	return panelUpdateStatus
}

func resetPanelUpdateStatus(target string) {
	panelUpdateStatusMu.Lock()
	defer panelUpdateStatusMu.Unlock()
	panelUpdateStatus = PanelUpdateStatus{Running: true, TargetVersion: target}
}

func setPanelUpdateStep(stage, message string, percent int) {
	panelUpdateStatusMu.Lock()
	defer panelUpdateStatusMu.Unlock()
	panelUpdateStatus.Running = true
	panelUpdateStatus.Stage = stage
	panelUpdateStatus.Message = message
	panelUpdateStatus.Percent = percent
}

func setPanelUpdateProgress(downloaded, total int64) {
	panelUpdateStatusMu.Lock()
	defer panelUpdateStatusMu.Unlock()
	panelUpdateStatus.DownloadedBytes = downloaded
	panelUpdateStatus.TotalBytes = total
}

func setPanelUpdateCompleted(message string) {
	panelUpdateStatusMu.Lock()
	defer panelUpdateStatusMu.Unlock()
	panelUpdateStatus.Running = false
	panelUpdateStatus.Completed = true
	panelUpdateStatus.Message = message
	panelUpdateStatus.Percent = 100
}

func setPanelUpdateFailed(stage string, err error) {
	panelUpdateStatusMu.Lock()
	defer panelUpdateStatusMu.Unlock()
	panelUpdateStatus.Running = false
	panelUpdateStatus.Stage = stage
	panelUpdateStatus.Error = err.Error()
}

// ExecutePanelUpdate checks for and, if available, starts installing a newer
// panel release. It does the version fetch/compare synchronously (so an
// obvious "already latest" or network failure surfaces immediately as a
// returned error) and then continues the actual download/verify/install
// sequence in the background; progress is tracked via
// SnapshotPanelUpdateStatus and errors from that point on are recorded there
// and in the operation log, not returned from this function.
func ExecutePanelUpdate(opts PanelUpdateOptions) error {
	if runtime.GOOS != "linux" {
		return fmt.Errorf("仅支持 Linux")
	}
	if !panelUpdateMu.TryLock() {
		return fmt.Errorf("已有更新任务正在执行，请稍后再试")
	}

	release, err := FetchLatestPanelRelease()
	if err != nil {
		panelUpdateMu.Unlock()
		return err
	}
	if CompareVersions(release.TagName, opts.CurrentVersion) <= 0 {
		panelUpdateMu.Unlock()
		return fmt.Errorf("已经是最新版本")
	}

	resetPanelUpdateStatus(release.TagName)
	go func() {
		defer panelUpdateMu.Unlock()
		runPanelUpdate(opts, release)
	}()
	return nil
}

func runPanelUpdate(opts PanelUpdateOptions, release *GithubRelease) {
	target := release.TagName
	recordStage := func(status, stage, message string) {
		RecordOperationLog("panel_"+opts.Trigger+"_update", target, status, stage+": "+message)
		if opts.Trigger == "auto" && status == "running" {
			setAutoUpdateRunning(stage, message, target)
		}
	}
	fail := func(stage string, err error) {
		setPanelUpdateFailed(stage, err)
		recordStage("failed", stage, err.Error())
		if opts.Trigger == "auto" {
			setUpdateSetting("panel_auto_update_last_stage", stage)
			setAutoUpdateResult("failed", err.Error(), target)
			notifyAutoUpdateOutcome(false, target, stage+": "+err.Error())
		}
	}

	dataDir := "/www/server/server-panel"
	if opts.Config != nil && opts.Config.Panel.DataDir != "" {
		dataDir = opts.Config.Panel.DataDir
	}
	binaryPath := panelBinaryPath(opts.Config)
	serviceName := PanelServiceName(opts.Config)

	setPanelUpdateStep("fetch_release", "已获取最新版本信息: "+target, 5)
	recordStage("running", "fetch_release", "latest="+target)

	setPanelUpdateStep("resolve_assets", "解析更新资源", 10)
	binaryName, checksumsName, sigName, err := PanelAssetNames()
	if err != nil {
		fail("resolve_assets", err)
		return
	}
	binaryURL := FindAssetURL(release, binaryName)
	checksumsURL := FindAssetURL(release, checksumsName)
	sigURL := FindAssetURL(release, sigName)
	if binaryURL == "" || checksumsURL == "" {
		fail("resolve_assets", fmt.Errorf("发行版缺少必要的资源文件（二进制或校验文件），无法验证完整性"))
		return
	}
	if sigURL == "" {
		fail("waiting_signature", errWaitingSignature)
		return
	}

	tmpDir, err := os.MkdirTemp("", "server-panel-update-*")
	if err != nil {
		fail("prepare_download", err)
		return
	}
	defer os.RemoveAll(tmpDir)

	setPanelUpdateStep("download_binary", "下载新版本二进制", 15)
	newBinaryPath := filepath.Join(tmpDir, binaryName)
	if err := downloadFile(binaryURL, newBinaryPath, 10*time.Minute, setPanelUpdateProgress); err != nil {
		fail("download_binary", err)
		return
	}

	setPanelUpdateStep("download_checksums", "下载校验文件", 62)
	checksumsPath := filepath.Join(tmpDir, checksumsName)
	if err := downloadFileRetry(checksumsURL, checksumsPath); err != nil {
		fail("download_checksums", err)
		return
	}

	setPanelUpdateStep("download_signature", "下载签名文件", 66)
	sigPath := filepath.Join(tmpDir, sigName)
	if err := downloadFileRetry(sigURL, sigPath); err != nil {
		fail("download_signature", err)
		return
	}

	setPanelUpdateStep("verify_signature", "校验签名", 72)
	checksumsBytes, err := os.ReadFile(checksumsPath)
	if err != nil {
		fail("verify_signature", err)
		return
	}
	sigRaw, err := os.ReadFile(sigPath)
	if err != nil {
		fail("verify_signature", err)
		return
	}
	sigBytes, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(sigRaw)))
	if err != nil {
		fail("verify_signature", fmt.Errorf("签名文件格式无效: %w", err))
		return
	}
	if !VerifyReleaseSignature(checksumsBytes, sigBytes) {
		fail("verify_signature", fmt.Errorf("签名校验失败，发布内容可能已被篡改"))
		return
	}

	setPanelUpdateStep("verify_checksum", "校验文件完整性", 78)
	expectedHash, err := findChecksumForFile(string(checksumsBytes), binaryName)
	if err != nil {
		fail("verify_checksum", err)
		return
	}
	actualHash, err := sha256File(newBinaryPath)
	if err != nil {
		fail("verify_checksum", err)
		return
	}
	if !strings.EqualFold(expectedHash, actualHash) {
		fail("verify_checksum", fmt.Errorf("SHA256 校验不匹配，文件可能已损坏"))
		return
	}
	if err := os.Chmod(newBinaryPath, 0755); err != nil {
		fail("verify_checksum", err)
		return
	}

	setPanelUpdateStep("preflight", "冒烟测试新版本二进制", 82)
	preflightArgs := []string{"--info"}
	if opts.ConfigPath != "" {
		preflightArgs = append(preflightArgs, "--config", opts.ConfigPath)
	}
	if err := exec.Command(newBinaryPath, preflightArgs...).Run(); err != nil {
		fail("preflight", fmt.Errorf("新版本二进制无法正常启动: %w", err))
		return
	}
	if opts.UseWatchdog {
		if _, err := exec.LookPath("systemd-run"); err != nil {
			fail("preflight", fmt.Errorf("未找到 systemd-run，无法启用更新看护进程: %w", err))
			return
		}
	}

	setPanelUpdateStep("disk_check", "检查磁盘空间", 84)
	info, err := os.Stat(newBinaryPath)
	if err != nil {
		fail("disk_check", err)
		return
	}
	if err := checkUpdateDiskSpace(filepath.Dir(binaryPath), dataDir, info.Size()); err != nil {
		fail("disk_check", err)
		return
	}

	setPanelUpdateStep("backup", "备份当前二进制", 88)
	backupBinaryPath, err := backupCurrentBinary(binaryPath, opts.CurrentVersion)
	if err != nil {
		fail("backup", err)
		return
	}
	useWatchdog := opts.UseWatchdog
	if useWatchdog && !binarySupportsWatchdog(backupBinaryPath) {
		useWatchdog = false
		RecordOperationLog("panel_"+opts.Trigger+"_update", target, "warning", "watchdog 不受当前旧版本二进制支持，本次更新不会启用自动回滚")
	}

	var backupDBPath string
	if opts.Config != nil {
		backupDir := filepath.Join(dataDir, "backups")
		backupDBPath, err = database.BackupDatabase(backupDir)
		if err != nil {
			fail("backup", fmt.Errorf("数据库备份失败: %w", err))
			return
		}
		if err := database.VerifyDBBackup(backupDBPath); err != nil {
			fail("backup", fmt.Errorf("数据库备份校验失败: %w", err))
			return
		}
	}

	planPath := rollbackPlanPath(dataDir)
	if useWatchdog {
		plan := rollbackPlan{
			CurrentVersion: opts.CurrentVersion,
			TargetVersion:  target,
			BackupBinary:   backupBinaryPath,
			BackupDB:       backupDBPath,
			ConfigPath:     opts.ConfigPath,
			HealthURL:      healthURL(opts.Config),
			BinaryPath:     binaryPath,
			ServiceName:    serviceName,
			Trigger:        opts.Trigger,
			CreatedAt:      time.Now().UTC(),
		}
		if err := writeRollbackPlanFile(planPath, plan); err != nil {
			fail("backup", fmt.Errorf("写入回滚计划失败: %w", err))
			return
		}
	}

	setPanelUpdateStep("replace_binary", "替换二进制文件", 92)
	if err := atomicReplacePanelFile(newBinaryPath, binaryPath, 0755); err != nil {
		fail("replace_binary", err)
		return
	}
	if err := os.Chmod(binaryPath, 0755); err != nil {
		_ = atomicReplacePanelFile(backupBinaryPath, binaryPath, 0755)
		fail("replace_binary", fmt.Errorf("设置执行权限失败，已回滚二进制: %w", err))
		return
	}

	if useWatchdog {
		setPanelUpdateStep("start_watchdog", "启动更新看护进程", 95)
		if err := startUpdateWatchdog(backupBinaryPath, planPath, opts.ConfigPath); err != nil {
			_ = atomicReplacePanelFile(backupBinaryPath, binaryPath, 0755)
			_ = os.Remove(planPath)
			fail("start_watchdog", fmt.Errorf("启动看护进程失败，已回滚二进制: %w", err))
			return
		}
	}

	setPanelUpdateStep("restart", "更新完成，服务即将重启", 98)
	recordStage("success", "replace_binary", "二进制已替换，等待重启")
	RestartPanelService()
	setPanelUpdateCompleted("更新文件已替换，面板正在重启并等待健康检查...")
}

// downloadFile streams url into dest, refusing to overwrite an existing
// file (O_EXCL) so a stale/tampered file at that path can't be silently reused.
func downloadFile(url, dest string, timeout time.Duration, onProgress func(downloaded, total int64)) error {
	if !strings.HasPrefix(url, "https://") {
		return fmt.Errorf("拒绝下载非 HTTPS 更新资源: %s", url)
	}
	client := &http.Client{Timeout: timeout}
	resp, err := client.Get(url)
	if err != nil {
		return fmt.Errorf("下载失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("下载返回状态码 %d", resp.StatusCode)
	}

	out, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return fmt.Errorf("创建目标文件失败: %w", err)
	}
	defer out.Close()

	total := resp.ContentLength
	var downloaded int64
	buf := make([]byte, 32*1024)
	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := out.Write(buf[:n]); werr != nil {
				return fmt.Errorf("写入文件失败: %w", werr)
			}
			downloaded += int64(n)
			if onProgress != nil {
				onProgress(downloaded, total)
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return fmt.Errorf("读取下载内容失败: %w", readErr)
		}
	}
	return nil
}

func downloadFileRetry(url, dest string) error {
	var lastErr error
	for attempt := 1; attempt <= 3; attempt++ {
		if err := downloadFile(url, dest, 60*time.Second, nil); err == nil {
			return nil
		} else {
			lastErr = err
			os.Remove(dest)
			time.Sleep(time.Duration(attempt) * 2 * time.Second)
		}
	}
	return lastErr
}

// findChecksumForFile parses `sha256sum`-style output ("<hash>  <filename>"
// per line) and returns the hash for filename. The checksums file covers
// multiple binaries (panel + agent), so matching by filename is required.
func findChecksumForFile(checksumsContent, filename string) (string, error) {
	for _, line := range strings.Split(checksumsContent, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		name := strings.TrimPrefix(fields[len(fields)-1], "*")
		if name == filename {
			return fields[0], nil
		}
	}
	return "", fmt.Errorf("校验文件中未找到 %s 的哈希", filename)
}

func sha256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func checkUpdateDiskSpace(binDir, dataDir string, binarySize int64) error {
	required := uint64(binarySize)*4 + 64*1024*1024
	if err := statfsAvailable(binDir, required); err != nil {
		return err
	}
	if dataDir != "" && dataDir != binDir {
		if err := statfsAvailable(dataDir, required); err != nil {
			return err
		}
	}
	return nil
}

func statfsAvailable(dir string, required uint64) error {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(dir, &stat); err != nil {
		return fmt.Errorf("检查磁盘空间失败 (%s): %w", dir, err)
	}
	available := stat.Bavail * uint64(stat.Bsize)
	if available < required {
		return fmt.Errorf("磁盘空间不足 (%s): 可用 %d MB，需要至少 %d MB", dir, available/1024/1024, required/1024/1024)
	}
	return nil
}

func panelBinaryPath(cfg *config.Config) string {
	if cfg != nil && cfg.Systemd.BinaryPath != "" {
		return cfg.Systemd.BinaryPath
	}
	return defaultPanelBinaryPath
}

func backupCurrentBinary(binaryPath, currentVersion string) (string, error) {
	safeVersion := strings.NewReplacer("/", "_", " ", "_").Replace(currentVersion)
	if safeVersion == "" {
		safeVersion = "unknown"
	}
	backupPath := fmt.Sprintf("%s.bak.%s.%s", binaryPath, safeVersion, time.Now().UTC().Format("20060102-150405"))
	if err := copyPanelFile(binaryPath, backupPath, 0755); err != nil {
		return "", fmt.Errorf("备份当前二进制失败: %w", err)
	}
	return backupPath, nil
}

func copyPanelFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	ok := false
	defer func() {
		out.Close()
		if !ok {
			os.Remove(dst)
		}
	}()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	if err := out.Sync(); err != nil {
		return err
	}
	if err := os.Chmod(dst, mode); err != nil {
		return err
	}
	ok = true
	return nil
}

func atomicReplacePanelFile(src, dst string, mode os.FileMode) error {
	tmp := fmt.Sprintf("%s.tmp.%d.%d", dst, os.Getpid(), time.Now().UnixNano())
	if err := copyPanelFile(src, tmp, mode); err != nil {
		return err
	}
	if err := os.Rename(tmp, dst); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Chmod(dst, mode)
}

type rollbackPlan struct {
	CurrentVersion string    `json:"current_version"`
	TargetVersion  string    `json:"target_version"`
	BackupBinary   string    `json:"backup_binary"`
	BackupDB       string    `json:"backup_db"`
	ConfigPath     string    `json:"config_path"`
	HealthURL      string    `json:"health_url"`
	BinaryPath     string    `json:"binary_path"`
	ServiceName    string    `json:"service_name"`
	Trigger        string    `json:"trigger"`
	CreatedAt      time.Time `json:"created_at"`
}

func rollbackPlanPath(dataDir string) string {
	return filepath.Join(dataDir, "update_rollback.json")
}

func writeRollbackPlanFile(path string, plan rollbackPlan) error {
	data, err := json.MarshalIndent(plan, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}

func readRollbackPlanFile(path string) (*rollbackPlan, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var plan rollbackPlan
	if err := json.Unmarshal(data, &plan); err != nil {
		return nil, err
	}
	return &plan, nil
}

func healthURL(cfg *config.Config) string {
	port := 8444
	if cfg != nil && cfg.Panel.TLSPort > 0 {
		port = cfg.Panel.TLSPort
	}
	return fmt.Sprintf("https://127.0.0.1:%d/healthz", port)
}

func startUpdateWatchdog(backupBinary, planPath, configPath string) error {
	unit := fmt.Sprintf("server-panel-update-watchdog-%d", time.Now().UnixNano())
	cmd := exec.Command("systemd-run",
		"--unit", unit, "--collect",
		"--property", "Type=simple",
		"--property", "KillMode=process",
		backupBinary, "--update-watchdog", planPath, "--config", configPath,
	)
	return cmd.Run()
}

func binarySupportsWatchdog(binaryPath string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, binaryPath, "--help").CombinedOutput()
	if ctx.Err() != nil {
		return false
	}
	if err != nil {
		return false
	}
	return strings.Contains(string(out), "update-watchdog")
}

// FinalizePendingPanelUpdate is called once at normal startup. It's purely an
// audit breadcrumb: if a rollback plan is on disk and this process's version
// matches the plan's target, the new binary did start successfully (the
// actual pass/fail determination and cleanup is the watchdog's job).
func FinalizePendingPanelUpdate(cfg *config.Config, currentVersion string) {
	if cfg == nil {
		return
	}
	plan, err := readRollbackPlanFile(rollbackPlanPath(cfg.Panel.DataDir))
	if err != nil {
		return
	}
	if plan.TargetVersion == currentVersion {
		RecordOperationLog("panel_"+plan.Trigger+"_update", currentVersion, "info", "new_process_started: 新进程已启动，等待健康检查确认")
	}
}

// RunUpdateWatchdog is executed by a separate systemd-run unit running the
// OLD (pre-update) binary, so the safety net doesn't depend on the very
// binary being validated. It polls the new process's health endpoint and
// automatically restores the backed-up binary (and database, if it looks
// corrupted) on failure.
func RunUpdateWatchdog(planPath string, cfg *config.Config) {
	plan, err := readRollbackPlanFile(planPath)
	if err != nil {
		log.Printf("watchdog: 读取回滚计划失败: %v", err)
		return
	}

	if waitForHealthy(plan.HealthURL, 3*time.Second, 4*time.Minute) {
		RecordOperationLog("panel_"+plan.Trigger+"_update", plan.TargetVersion, "success", "health_check: 新版本运行正常")
		setAutoUpdateResult("success", "", plan.TargetVersion)
		cleanupPanelUpdateBackups(cfg)
		os.Remove(planPath)
		if plan.Trigger == "auto" {
			notifyAutoUpdateOutcome(true, plan.TargetVersion, "")
		}
		return
	}

	RecordOperationLog("panel_"+plan.Trigger+"_update", plan.TargetVersion, "failed", "health_check: 健康检查超时，开始自动回滚")
	binaryPath := plan.BinaryPath
	if binaryPath == "" {
		binaryPath = defaultPanelBinaryPath
	}
	serviceName := plan.ServiceName
	if serviceName == "" {
		serviceName = PanelServiceName(cfg)
	}
	serviceStopped := true
	if err := StopPanelServiceSync(serviceName); err != nil {
		serviceStopped = false
		log.Printf("watchdog: 停止服务失败，仍将尝试回滚二进制: %v", err)
	}
	if err := atomicReplacePanelFile(plan.BackupBinary, binaryPath, 0755); err != nil {
		log.Printf("watchdog: 回滚二进制失败: %v", err)
	}
	detail := "健康检查失败，已自动回滚二进制"
	if !serviceStopped && plan.BackupDB != "" {
		RecordOperationLog("panel_"+plan.Trigger+"_update", plan.TargetVersion, "warning", "服务停止失败，跳过数据库回滚以避免破坏运行中的 SQLite 文件")
	}
	if serviceStopped && plan.BackupDB != "" && cfg != nil && shouldRestoreDBAfterHealthFailure(cfg.SQLite.Path) {
		if err := restoreDBBackupForWatchdog(plan.BackupDB, cfg.SQLite.Path); err != nil {
			log.Printf("watchdog: 回滚数据库失败: %v", err)
		} else {
			detail = "健康检查失败，已自动回滚二进制和数据库"
			RecordOperationLog("panel_"+plan.Trigger+"_update", plan.TargetVersion, "info", "已回滚数据库到更新前状态")
		}
	}
	setAutoUpdateResult("failed", detail, plan.TargetVersion)
	if plan.Trigger == "auto" {
		notifyAutoUpdateOutcome(false, plan.TargetVersion, detail)
	}
	os.Remove(planPath)
	if serviceStopped {
		if err := StartPanelServiceSync(serviceName); err != nil {
			log.Printf("watchdog: 启动服务失败: %v", err)
		} else {
			RecordOperationLog("panel_"+plan.Trigger+"_update", plan.TargetVersion, "info", "已启动回滚后的面板服务")
		}
	} else if err := RestartPanelServiceSync(serviceName); err != nil {
		log.Printf("watchdog: 服务停止失败后尝试重启也失败: %v", err)
		RecordOperationLog("panel_"+plan.Trigger+"_update", plan.TargetVersion, "failed", "服务停止失败且重启失败，请手动执行 systemctl restart "+serviceName)
	} else {
		RecordOperationLog("panel_"+plan.Trigger+"_update", plan.TargetVersion, "warning", "服务停止失败后已执行 restart 以加载回滚二进制")
	}
}

func restoreDBBackupForWatchdog(backupPath, liveDBPath string) error {
	if database.GetDB() != nil {
		if err := database.Close(); err != nil {
			return fmt.Errorf("关闭当前数据库连接失败: %w", err)
		}
	}
	if err := database.RestoreDatabaseFile(backupPath, liveDBPath); err != nil {
		return err
	}
	if err := database.Open(liveDBPath); err != nil {
		return fmt.Errorf("恢复后重新打开数据库失败: %w", err)
	}
	return nil
}

func waitForHealthy(url string, interval, timeout time.Duration) bool {
	client := &http.Client{
		Timeout:   interval,
		Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}},
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		resp, err := client.Get(url)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return true
			}
		}
		time.Sleep(interval)
	}
	return false
}

// shouldRestoreDBAfterHealthFailure opens the live database file independently
// (the failed new process may or may not still hold it) and checks that the
// core tables are readable; if not, the new binary's migrations likely broke
// something and the database backup should be restored too, not just the binary.
func shouldRestoreDBAfterHealthFailure(dbPath string) bool {
	// Read-only sanity check - no WAL/pragma tuning needed here (and
	// modernc.org/sqlite doesn't recognize the mattn-style _journal_mode=
	// DSN param anyway, see database.Open).
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return true
	}
	defer db.Close()
	for _, table := range []string{"schema_version", "admin_users", "websites", "settings"} {
		var count int
		if err := db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count); err != nil {
			return true
		}
	}
	return false
}

func cleanupPanelUpdateBackups(cfg *config.Config) {
	binaryPath := panelBinaryPath(cfg)
	pruneGlobBackups(filepath.Dir(binaryPath), filepath.Base(binaryPath)+".bak.*", panelBinaryBackupKeep)
	pruneGlobBackups(filepath.Dir(binaryPath), filepath.Base(binaryPath)+".tmp.*", 0)
	if cfg != nil && cfg.Panel.DataDir != "" {
		pruneGlobBackups(filepath.Join(cfg.Panel.DataDir, "backups"), "server-panel.db.bak.*", panelDBBackupKeep)
	}
}

func pruneGlobBackups(dir, pattern string, keep int) {
	matches, err := filepath.Glob(filepath.Join(dir, pattern))
	if err != nil || len(matches) <= keep {
		return
	}
	sort.Strings(matches) // timestamp suffix sorts lexically in chronological order
	for _, m := range matches[:len(matches)-keep] {
		os.Remove(m)
	}
}
