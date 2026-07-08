package executor

import (
	"fmt"
	"io"
	"mime/multipart"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"time"

	"github.com/naibabiji/server-panel/config"
	"github.com/naibabiji/server-panel/database"
	"github.com/naibabiji/server-panel/timeutil"
)

// backupFilenamePattern matches names CreateFullBackupArchive and
// SaveUploadedBackup produce. It's the only shape ResolveBackupPath accepts,
// so a client-supplied filename can never escape the backups directory via
// "..", an absolute path, or an unrelated file elsewhere on disk.
var backupFilenamePattern = regexp.MustCompile(`^server-panel-backup\.[0-9]{8}-[0-9]{6}(\.uploaded)?\.tar\.gz$`)

type BackupFileInfo struct {
	Filename  string `json:"filename"`
	SizeBytes int64  `json:"size_bytes"`
	SizeHuman string `json:"size_human"`
	ModTime   string `json:"mod_time"`
}

// ListDatabaseBackups lists full backup archives on disk, newest first.
func ListDatabaseBackups() ([]BackupFileInfo, error) {
	cfg := config.AppConfig
	dir, err := backupDirPath(cfg)
	if err != nil {
		return nil, err
	}
	matches, err := filepath.Glob(filepath.Join(dir, "server-panel-backup.*.tar.gz"))
	if err != nil {
		return nil, err
	}
	items := make([]BackupFileInfo, 0, len(matches))
	for _, path := range matches {
		info, statErr := os.Stat(path)
		if statErr != nil {
			continue
		}
		items = append(items, BackupFileInfo{
			Filename:  filepath.Base(path),
			SizeBytes: info.Size(),
			SizeHuman: formatBackupSize(info.Size()),
			ModTime:   timeutil.Display(info.ModTime()),
		})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ModTime > items[j].ModTime })
	return items, nil
}

// ResolveBackupPath turns a client-supplied filename into a path inside the
// backups directory, rejecting anything that doesn't match the exact naming
// scheme this package generates or that doesn't exist on disk.
func ResolveBackupPath(filename string) (string, error) {
	base := filepath.Base(filename)
	if !backupFilenamePattern.MatchString(base) {
		return "", fmt.Errorf("无效的备份文件名")
	}
	cfg := config.AppConfig
	dir, err := backupDirPath(cfg)
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, base)
	if _, err := os.Stat(path); err != nil {
		return "", fmt.Errorf("备份文件不存在")
	}
	return path, nil
}

// RemoveBackupFile deletes a backup archive by filename, used to clean up an
// uploaded archive that fails validation so it doesn't linger as a fake backup.
func RemoveBackupFile(filename string) error {
	path, err := ResolveBackupPath(filename)
	if err != nil {
		return err
	}
	return os.Remove(path)
}

// validateBackupArchive extracts the archive into a scratch directory and
// runs the same integrity check backups are verified with at creation time,
// so a corrupt or unrelated .tar.gz is rejected before it's ever scheduled
// for restore.
func validateBackupArchive(path string) error {
	tmpDir, err := os.MkdirTemp("", "server-panel-restore-check-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpDir)

	dbPath, _, err := database.ExtractFullBackupArchive(path, tmpDir)
	if err != nil {
		return err
	}
	return database.VerifyDBBackup(dbPath)
}

// SaveUploadedBackup streams an uploaded archive into the backups directory
// under a generated name that matches backupFilenamePattern, via a
// temp-file-then-rename write so a failed/partial upload never leaves a
// truncated file at the final name.
func SaveUploadedBackup(fh *multipart.FileHeader) (string, error) {
	cfg := config.AppConfig
	dir, err := backupDirPath(cfg)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", fmt.Errorf("创建备份目录失败: %w", err)
	}

	src, err := fh.Open()
	if err != nil {
		return "", fmt.Errorf("读取上传文件失败: %w", err)
	}
	defer src.Close()

	filename := fmt.Sprintf("server-panel-backup.%s.uploaded.tar.gz", time.Now().UTC().Format("20060102-150405"))
	finalPath := filepath.Join(dir, filename)
	tmpPath := fmt.Sprintf("%s.tmp.%d", finalPath, time.Now().UnixNano())

	dst, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return "", fmt.Errorf("写入上传文件失败: %w", err)
	}
	if _, err := io.Copy(dst, src); err != nil {
		dst.Close()
		os.Remove(tmpPath)
		return "", fmt.Errorf("写入上传文件失败: %w", err)
	}
	if err := dst.Close(); err != nil {
		os.Remove(tmpPath)
		return "", fmt.Errorf("写入上传文件失败: %w", err)
	}
	if err := os.Rename(tmpPath, finalPath); err != nil {
		os.Remove(tmpPath)
		return "", fmt.Errorf("保存上传文件失败: %w", err)
	}
	return filename, nil
}

// pendingRestoreMarkerName is the file that records which backup archive to
// restore on the next boot. ScheduleRestore writes it and triggers a service
// restart; main.go's applyPendingRestoreIfAny consumes it before the live
// database is opened.
const pendingRestoreMarkerName = "pending-restore.path"

func pendingRestoreMarkerPath(cfg *config.Config) (string, error) {
	if cfg == nil {
		return "", fmt.Errorf("配置未加载")
	}
	return filepath.Join(cfg.Panel.DataDir, pendingRestoreMarkerName), nil
}

func writePendingRestoreMarker(cfg *config.Config, archivePath string) error {
	markerPath, err := pendingRestoreMarkerPath(cfg)
	if err != nil {
		return err
	}
	tmpPath := fmt.Sprintf("%s.tmp.%d", markerPath, time.Now().UnixNano())
	if err := os.WriteFile(tmpPath, []byte(archivePath), 0600); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, markerPath); err != nil {
		os.Remove(tmpPath)
		return err
	}
	return nil
}

// ConsumePendingRestore reads and deletes the pending-restore marker, if any.
// It's consumed (not left in place) as soon as it's read, so a bad archive
// fails the boot once via log.Fatalf rather than retry-looping forever.
func ConsumePendingRestore() (string, bool) {
	cfg := config.AppConfig
	markerPath, err := pendingRestoreMarkerPath(cfg)
	if err != nil {
		return "", false
	}
	data, err := os.ReadFile(markerPath)
	if err != nil {
		return "", false
	}
	_ = os.Remove(markerPath)
	archivePath := string(data)
	if archivePath == "" {
		return "", false
	}
	return archivePath, true
}

// ScheduleRestore validates a backup archive already on disk (an existing
// local backup, or one just saved by SaveUploadedBackup) and schedules it to
// be restored on the panel's next boot, then restarts the service so that
// boot happens right away. See executor/restart.go's RestartPanelService and
// main.go's runRestoreBackup/applyPendingRestoreIfAny for why this goes
// through a restart instead of swapping the live database in place.
func ScheduleRestore(filename string) error {
	cfg := config.AppConfig
	path, err := ResolveBackupPath(filename)
	if err != nil {
		return err
	}
	if err := validateBackupArchive(path); err != nil {
		return fmt.Errorf("备份文件校验失败: %w", err)
	}
	if err := writePendingRestoreMarker(cfg, path); err != nil {
		return fmt.Errorf("写入恢复标记失败: %w", err)
	}
	RecordOperationLog("database_restore", filename, "scheduled", "已安排从备份恢复，等待服务重启")
	RestartPanelService()
	return nil
}
