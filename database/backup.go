package database

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// BackupDatabase writes a consistent online snapshot of the live database into
// dir using SQLite's VACUUM INTO, and returns the backup file's path.
func BackupDatabase(dir string) (string, error) {
	if DB == nil {
		return "", fmt.Errorf("database not open")
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", fmt.Errorf("failed to create backup directory: %w", err)
	}
	backupPath := filepath.Join(dir, fmt.Sprintf("server-panel.db.bak.%s", time.Now().UTC().Format("20060102-150405")))
	quotedPath := strings.ReplaceAll(backupPath, "'", "''")
	if _, err := DB.Exec(fmt.Sprintf("VACUUM INTO '%s'", quotedPath)); err != nil {
		return "", fmt.Errorf("vacuum into backup failed: %w", err)
	}
	return backupPath, nil
}

// VerifyDBBackup opens a backup file independently and runs an integrity
// check, so a corrupt backup is caught before it's ever relied on.
func VerifyDBBackup(path string) error {
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("backup file not found: %w", err)
	}
	dsn := path + "?_journal_mode=WAL"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return fmt.Errorf("failed to open backup: %w", err)
	}
	defer db.Close()

	var result string
	if err := db.QueryRow("PRAGMA integrity_check").Scan(&result); err != nil {
		return fmt.Errorf("integrity check query failed: %w", err)
	}
	if result != "ok" {
		return fmt.Errorf("integrity check reported: %s", result)
	}
	return nil
}

// RestoreDatabaseFile replaces the live database file with a backup copy.
// The caller must Close() the live DB connection before calling this, and
// re-Open() it afterward. Any stale WAL/SHM files next to the live path are
// removed so the restored file isn't merged with leftover write-ahead state.
func RestoreDatabaseFile(backupPath, liveDBPath string) error {
	data, err := os.ReadFile(backupPath)
	if err != nil {
		return fmt.Errorf("failed to read backup: %w", err)
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		_ = os.Remove(liveDBPath + suffix)
	}
	tmpPath := fmt.Sprintf("%s.restore.%d", liveDBPath, time.Now().UnixNano())
	if err := os.WriteFile(tmpPath, data, 0600); err != nil {
		return fmt.Errorf("failed to write live database: %w", err)
	}
	if err := os.Rename(tmpPath, liveDBPath); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("failed to replace live database: %w", err)
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		_ = os.Remove(liveDBPath + suffix)
	}
	return nil
}
