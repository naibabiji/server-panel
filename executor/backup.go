package executor

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/naibabiji/server-panel/config"
	"github.com/naibabiji/server-panel/database"
)

const (
	backupDefaultKeepCount  = 10
	backupDefaultMaxEmailMB = 20
)

var backupRunMu sync.Mutex

type DatabaseBackupResult struct {
	Path               string `json:"path"`
	Filename           string `json:"filename"`
	SizeBytes          int64  `json:"size_bytes"`
	SizeHuman          string `json:"size_human"`
	EmailSent          bool   `json:"email_sent"`
	EmailSkippedReason string `json:"email_skipped_reason,omitempty"`
	Status             string `json:"status"`
	Message            string `json:"message"`
}

func RunDatabaseBackup(trigger string, emailEnabled bool) (DatabaseBackupResult, error) {
	backupRunMu.Lock()
	defer backupRunMu.Unlock()

	result := DatabaseBackupResult{Status: "running"}
	setBackupStatus("running", "", "")
	RecordOperationLog("database_backup", trigger, "running", "database backup started")

	cfg := config.AppConfig
	if cfg == nil {
		err := fmt.Errorf("配置未加载")
		setBackupStatus("failed", "", err.Error())
		RecordOperationLog("database_backup", trigger, "failed", err.Error())
		return result, err
	}

	dir := filepath.Join(cfg.Panel.DataDir, "backups", "database")
	dbPath, err := database.BackupDatabase(dir)
	if err != nil {
		setBackupStatus("failed", "", err.Error())
		RecordOperationLog("database_backup", trigger, "failed", err.Error())
		return result, err
	}
	if err := database.VerifyDBBackup(dbPath); err != nil {
		setBackupStatus("failed", "", err.Error())
		RecordOperationLog("database_backup", trigger, "failed", err.Error())
		return result, err
	}

	// Bundle the secret encryption key alongside the database: without it,
	// restoring this backup onto a panel with a different key (e.g. after a
	// fresh install) leaves every encrypted field permanently unreadable.
	secretKeyPath := filepath.Join(cfg.Panel.DataDir, "secret.key")
	path, err := database.CreateFullBackupArchive(dbPath, secretKeyPath, dir)
	if err != nil {
		setBackupStatus("failed", "", err.Error())
		RecordOperationLog("database_backup", trigger, "failed", err.Error())
		return result, err
	}

	info, err := os.Stat(path)
	if err != nil {
		setBackupStatus("failed", "", err.Error())
		RecordOperationLog("database_backup", trigger, "failed", err.Error())
		return result, err
	}

	result.Path = path
	result.Filename = filepath.Base(path)
	result.SizeBytes = info.Size()
	result.SizeHuman = formatBackupSize(info.Size())
	result.Status = "success"
	result.Message = "备份已生成（含数据库与密钥）"

	var warnings []string

	keepCount := backupSettingInt("backup_keep_count", backupDefaultKeepCount)
	if keepCount < 1 {
		keepCount = backupDefaultKeepCount
	}
	if err := pruneDatabaseBackups(dir, keepCount); err != nil {
		result.Status = "warning"
		warnings = append(warnings, "清理旧备份失败: "+err.Error())
	}

	if emailEnabled {
		emailStatus, emailErr := sendDatabaseBackupEmail(result)
		if emailErr != nil {
			result.Status = "warning"
			result.EmailSkippedReason = emailErr.Error()
			warnings = append(warnings, "邮件未发送: "+emailErr.Error())
		} else if emailStatus != "" {
			result.EmailSent = true
			result.Message = emailStatus
		}
	}

	if len(warnings) > 0 {
		result.Message += "，但" + strings.Join(warnings, "；")
	}

	setBackupStatus(result.Status, time.Now().Format(time.RFC3339), strings.Join(warnings, "；"))
	RecordOperationLog("database_backup", trigger, result.Status, result.Message)
	return result, nil
}

func StartDatabaseBackupScheduler(interval time.Duration) {
	if interval <= 0 {
		interval = time.Hour
	}
	go func() {
		timer := time.NewTimer(2 * time.Minute)
		defer timer.Stop()
		<-timer.C

		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			runAutoDatabaseBackupIfDue()
			<-ticker.C
		}
	}()
}

func runAutoDatabaseBackupIfDue() {
	if !backupSettingBool("backup_auto_enabled") {
		return
	}
	frequency := backupSetting("backup_frequency")
	if frequency == "" {
		frequency = "weekly"
	}
	lastRun := backupSetting("backup_last_run_at")
	if !autoBackupDue(time.Now(), lastRun, frequency) {
		return
	}
	emailEnabled := backupSettingBool("backup_email_enabled")
	if _, err := RunDatabaseBackup("auto", emailEnabled); err != nil {
		return
	}
}

func autoBackupDue(now time.Time, lastRun, frequency string) bool {
	if strings.TrimSpace(lastRun) == "" {
		return true
	}
	last, err := time.Parse(time.RFC3339, lastRun)
	if err != nil {
		return true
	}
	switch frequency {
	case "daily":
		return now.Sub(last) >= 24*time.Hour
	default:
		return now.Sub(last) >= 7*24*time.Hour
	}
}

func sendDatabaseBackupEmail(result DatabaseBackupResult) (string, error) {
	maxMB := backupSettingInt("backup_max_email_mb", backupDefaultMaxEmailMB)
	if maxMB < 1 {
		maxMB = backupDefaultMaxEmailMB
	}
	limit := int64(maxMB) * 1024 * 1024
	if result.SizeBytes > limit {
		return "", fmt.Errorf("备份文件 %s 超过邮件附件上限 %d MB", result.SizeHuman, maxMB)
	}

	data, err := os.ReadFile(result.Path)
	if err != nil {
		return "", fmt.Errorf("读取备份文件失败: %w", err)
	}
	body := fmt.Sprintf("Server Panel 备份已生成，附件包含数据库和密钥（server-panel.db + secret.key）。\n\n文件名：%s\n大小：%s\n生成时间：%s\n\n此附件可解密面板中保存的所有敏感信息，请妥善保存，不要转发给无关人员。",
		result.Filename, result.SizeHuman, time.Now().Format("2006-01-02 15:04:05"))
	err = SendMailWithAttachments("", "Server Panel 备份", body, []MailAttachment{{
		Filename:    result.Filename,
		ContentType: "application/gzip",
		Data:        data,
	}})
	if err != nil {
		return "", err
	}
	return "备份已生成并发送到管理员邮箱", nil
}

func pruneDatabaseBackups(dir string, keepCount int) error {
	entries, err := filepath.Glob(filepath.Join(dir, "server-panel-backup.*.tar.gz"))
	if err != nil {
		return err
	}
	sort.Slice(entries, func(i, j int) bool {
		infoI, errI := os.Stat(entries[i])
		infoJ, errJ := os.Stat(entries[j])
		if errI != nil || errJ != nil {
			return entries[i] > entries[j]
		}
		return infoI.ModTime().After(infoJ.ModTime())
	})
	if len(entries) <= keepCount {
		return nil
	}
	for _, path := range entries[keepCount:] {
		if err := os.Remove(path); err != nil {
			return err
		}
	}
	return nil
}

func backupSetting(key string) string {
	db := database.GetDB()
	if db == nil {
		return ""
	}
	var value string
	_ = db.QueryRow("SELECT svalue FROM settings WHERE skey = ?", key).Scan(&value)
	return value
}

func backupSettingBool(key string) bool {
	return backupSetting(key) == "true"
}

func backupSettingInt(key string, fallback int) int {
	value := strings.TrimSpace(backupSetting(key))
	if value == "" {
		return fallback
	}
	n, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return n
}

func setBackupStatus(status, runAt, errMsg string) {
	db := database.GetDB()
	if db == nil {
		return
	}
	_, _ = db.Exec("INSERT OR REPLACE INTO settings (skey, svalue) VALUES ('backup_last_status', ?)", status)
	_, _ = db.Exec("INSERT OR REPLACE INTO settings (skey, svalue) VALUES ('backup_last_error', ?)", errMsg)
	if runAt != "" {
		_, _ = db.Exec("INSERT OR REPLACE INTO settings (skey, svalue) VALUES ('backup_last_run_at', ?)", runAt)
	}
}

func formatBackupSize(bytes int64) string {
	if bytes < 1024 {
		return fmt.Sprintf("%d B", bytes)
	}
	units := []string{"KB", "MB", "GB"}
	value := float64(bytes)
	for _, unit := range units {
		value /= 1024
		if value < 1024 || unit == "GB" {
			return fmt.Sprintf("%.1f %s", value, unit)
		}
	}
	return fmt.Sprintf("%d B", bytes)
}
