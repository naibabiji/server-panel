package main

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/naibabiji/server-panel/config"
	"github.com/naibabiji/server-panel/database"
)

// newMinimalBackupDB creates a trivial-but-valid sqlite file (VerifyDBBackup
// requires it to open and pass PRAGMA integrity_check) to stand in for a
// real database backup snapshot.
func newMinimalBackupDB(t *testing.T, path string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if _, err := db.Exec("CREATE TABLE settings (skey TEXT PRIMARY KEY, svalue TEXT)"); err != nil {
		t.Fatalf("create table: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close db: %v", err)
	}
}

func TestRunRestoreBackupRemovesStaleKeyWhenArchiveHasNone(t *testing.T) {
	srcDir := t.TempDir()
	dbPath := filepath.Join(srcDir, "server-panel.db")
	newMinimalBackupDB(t, dbPath)

	archiveDir := t.TempDir()
	archivePath, err := database.CreateFullBackupArchive(dbPath, filepath.Join(srcDir, "no-such-secret.key"), archiveDir)
	if err != nil {
		t.Fatalf("CreateFullBackupArchive: %v", err)
	}

	// Target ("this panel", freshly reinstalled) already has its own
	// secret.key from install - this is the exact scenario the restore path
	// must not leave stale: handlers.GetSecretEncryptionKey prefers an
	// existing file over any DB-stored legacy key, so leaving this file in
	// place would silently defeat the legacy-key fallback after restore.
	targetDir := t.TempDir()
	liveSecretKeyPath := filepath.Join(targetDir, "secret.key")
	if err := os.WriteFile(liveSecretKeyPath, []byte("stale-target-key"), 0600); err != nil {
		t.Fatalf("seed target secret.key: %v", err)
	}

	cfg := &config.Config{
		Panel:  config.PanelConfig{DataDir: targetDir},
		SQLite: config.SQLiteConfig{Path: filepath.Join(targetDir, "server-panel.db")},
	}

	if err := runRestoreBackup(cfg, archivePath); err != nil {
		t.Fatalf("runRestoreBackup: %v", err)
	}

	if _, err := os.Stat(liveSecretKeyPath); !os.IsNotExist(err) {
		t.Errorf("expected stale secret.key removed from live path, stat err = %v", err)
	}

	matches, _ := filepath.Glob(liveSecretKeyPath + ".*.pre-restore")
	if len(matches) != 1 {
		t.Fatalf("expected exactly one .pre-restore copy of the stale key, got %v", matches)
	}
	data, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatalf("read pre-restore key copy: %v", err)
	}
	if string(data) != "stale-target-key" {
		t.Errorf("pre-restore key copy content = %q, want %q", data, "stale-target-key")
	}

	if _, err := os.Stat(cfg.SQLite.Path); err != nil {
		t.Errorf("expected restored db at live path: %v", err)
	}
}

func TestRunRestoreBackupWritesArchiveKeyToLivePath(t *testing.T) {
	srcDir := t.TempDir()
	dbPath := filepath.Join(srcDir, "server-panel.db")
	newMinimalBackupDB(t, dbPath)

	secretKeyPath := filepath.Join(srcDir, "secret.key")
	if err := os.WriteFile(secretKeyPath, []byte("archive-key-value"), 0600); err != nil {
		t.Fatalf("write source secret.key: %v", err)
	}

	archiveDir := t.TempDir()
	archivePath, err := database.CreateFullBackupArchive(dbPath, secretKeyPath, archiveDir)
	if err != nil {
		t.Fatalf("CreateFullBackupArchive: %v", err)
	}

	targetDir := t.TempDir()
	cfg := &config.Config{
		Panel:  config.PanelConfig{DataDir: targetDir},
		SQLite: config.SQLiteConfig{Path: filepath.Join(targetDir, "server-panel.db")},
	}

	if err := runRestoreBackup(cfg, archivePath); err != nil {
		t.Fatalf("runRestoreBackup: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(targetDir, "secret.key"))
	if err != nil {
		t.Fatalf("read restored secret.key: %v", err)
	}
	if string(data) != "archive-key-value" {
		t.Errorf("restored secret.key content = %q, want %q", data, "archive-key-value")
	}
}
