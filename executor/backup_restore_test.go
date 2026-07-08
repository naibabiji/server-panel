package executor

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/naibabiji/server-panel/config"
	"github.com/naibabiji/server-panel/database"
	_ "modernc.org/sqlite"
)

// withTestConfig points config.AppConfig at a config backed by a temp data
// dir for the duration of the test, restoring the previous value after.
func withTestConfig(t *testing.T) *config.Config {
	t.Helper()
	prev := config.AppConfig
	cfg := &config.Config{Panel: config.PanelConfig{DataDir: t.TempDir()}}
	config.AppConfig = cfg
	t.Cleanup(func() { config.AppConfig = prev })
	return cfg
}

// writeTestBackupArchive builds a real, valid full backup archive (via the
// same code path RunDatabaseBackup uses) in cfg's backups directory, named
// so it matches backupFilenamePattern.
func writeTestBackupArchive(t *testing.T, cfg *config.Config, name string) string {
	t.Helper()
	dir, err := backupDirPath(cfg)
	if err != nil {
		t.Fatalf("backupDirPath: %v", err)
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatalf("mkdir backups dir: %v", err)
	}

	srcDir := t.TempDir()
	dbPath := filepath.Join(srcDir, "server-panel.db")
	writeRealSQLiteFile(t, dbPath)
	secretKeyPath := filepath.Join(srcDir, "secret.key")
	if err := os.WriteFile(secretKeyPath, []byte("fake-key"), 0600); err != nil {
		t.Fatalf("write fake key: %v", err)
	}

	archivePath, err := database.CreateFullBackupArchive(dbPath, secretKeyPath, srcDir)
	if err != nil {
		t.Fatalf("CreateFullBackupArchive: %v", err)
	}
	finalPath := filepath.Join(dir, name)
	data, err := os.ReadFile(archivePath)
	if err != nil {
		t.Fatalf("read built archive: %v", err)
	}
	if err := os.WriteFile(finalPath, data, 0600); err != nil {
		t.Fatalf("write archive into backups dir: %v", err)
	}
	return finalPath
}

// writeRealSQLiteFile creates a real, valid (if trivial) SQLite database
// file at path, so VerifyDBBackup's PRAGMA integrity_check has something
// genuine to check rather than just a file of arbitrary bytes.
func writeRealSQLiteFile(t *testing.T, path string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open sqlite file: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE t (id INTEGER PRIMARY KEY)`); err != nil {
		t.Fatalf("create table in test sqlite file: %v", err)
	}
}

func TestResolveBackupPathRejectsBadNames(t *testing.T) {
	cfg := withTestConfig(t)
	writeTestBackupArchive(t, cfg, "server-panel-backup.20260101-000000.tar.gz")

	cases := []string{
		"../../etc/passwd",
		"server-panel-backup.20260101-000000.tar.gz/../../../etc/passwd",
		"not-a-backup.tar.gz",
		"server-panel-backup.tar.gz",
		"",
	}
	for _, name := range cases {
		if _, err := ResolveBackupPath(name); err == nil {
			t.Errorf("ResolveBackupPath(%q) = nil error, want error", name)
		}
	}
}

func TestResolveBackupPathAcceptsRealFile(t *testing.T) {
	cfg := withTestConfig(t)
	path := writeTestBackupArchive(t, cfg, "server-panel-backup.20260101-000000.tar.gz")

	got, err := ResolveBackupPath("server-panel-backup.20260101-000000.tar.gz")
	if err != nil {
		t.Fatalf("ResolveBackupPath: %v", err)
	}
	if got != path {
		t.Errorf("ResolveBackupPath = %q, want %q", got, path)
	}
}

func TestResolveBackupPathRejectsMissingFile(t *testing.T) {
	withTestConfig(t)
	if _, err := ResolveBackupPath("server-panel-backup.20260101-000000.tar.gz"); err == nil {
		t.Fatal("expected error resolving a backup that was never created")
	}
}

func TestListDatabaseBackupsSortedNewestFirst(t *testing.T) {
	cfg := withTestConfig(t)
	older := writeTestBackupArchive(t, cfg, "server-panel-backup.20260101-000000.tar.gz")
	newer := writeTestBackupArchive(t, cfg, "server-panel-backup.20260102-000000.tar.gz")

	olderTime := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	newerTime := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	if err := os.Chtimes(older, olderTime, olderTime); err != nil {
		t.Fatalf("chtimes older: %v", err)
	}
	if err := os.Chtimes(newer, newerTime, newerTime); err != nil {
		t.Fatalf("chtimes newer: %v", err)
	}

	items, err := ListDatabaseBackups()
	if err != nil {
		t.Fatalf("ListDatabaseBackups: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("len(items) = %d, want 2", len(items))
	}
	if items[0].Filename != "server-panel-backup.20260102-000000.tar.gz" {
		t.Errorf("items[0].Filename = %q, want newest first", items[0].Filename)
	}
	if items[1].Filename != "server-panel-backup.20260101-000000.tar.gz" {
		t.Errorf("items[1].Filename = %q, want oldest last", items[1].Filename)
	}
}

func TestValidateBackupArchiveAcceptsRealArchive(t *testing.T) {
	cfg := withTestConfig(t)
	path := writeTestBackupArchive(t, cfg, "server-panel-backup.20260101-000000.tar.gz")

	if err := validateBackupArchive(path); err != nil {
		t.Errorf("validateBackupArchive on a real archive: %v", err)
	}
}

func TestValidateBackupArchiveRejectsCorruptArchive(t *testing.T) {
	withTestConfig(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "garbage.tar.gz")
	if err := os.WriteFile(path, []byte("not a real archive"), 0600); err != nil {
		t.Fatalf("write garbage file: %v", err)
	}

	if err := validateBackupArchive(path); err == nil {
		t.Error("expected validateBackupArchive to reject a corrupt archive")
	}
}

func TestPendingRestoreMarkerRoundTrip(t *testing.T) {
	cfg := withTestConfig(t)
	archivePath := writeTestBackupArchive(t, cfg, "server-panel-backup.20260101-000000.tar.gz")

	if err := writePendingRestoreMarker(cfg, archivePath); err != nil {
		t.Fatalf("writePendingRestoreMarker: %v", err)
	}

	got, ok := ConsumePendingRestore()
	if !ok {
		t.Fatal("ConsumePendingRestore: ok = false, want true")
	}
	if got != archivePath {
		t.Errorf("ConsumePendingRestore = %q, want %q", got, archivePath)
	}

	if _, ok := ConsumePendingRestore(); ok {
		t.Error("ConsumePendingRestore after consuming once: ok = true, want false (marker should be deleted)")
	}
}

func TestConsumePendingRestoreNoMarker(t *testing.T) {
	withTestConfig(t)
	if _, ok := ConsumePendingRestore(); ok {
		t.Error("ConsumePendingRestore with no marker written: ok = true, want false")
	}
}
