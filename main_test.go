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

func TestCopyFileLeavesDestinationUntouchedOnWriteFailure(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	if err := os.WriteFile(src, []byte("new content"), 0600); err != nil {
		t.Fatalf("write src: %v", err)
	}

	dstDir := filepath.Join(dir, "dstdir")
	if err := os.Mkdir(dstDir, 0700); err != nil {
		t.Fatalf("mkdir dstdir: %v", err)
	}
	dst := filepath.Join(dstDir, "dst")
	if err := os.WriteFile(dst, []byte("original content"), 0600); err != nil {
		t.Fatalf("write dst: %v", err)
	}

	// Read-only directory: copyFile can't create its temp file, so the
	// existing dst must survive completely unmodified - not truncated by
	// an in-place os.WriteFile(dst, ...).
	if err := os.Chmod(dstDir, 0500); err != nil {
		t.Fatalf("chmod dstdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dstDir, 0700) })

	if err := copyFile(src, dst); err == nil {
		t.Fatal("expected copyFile to fail when its directory is read-only")
	}

	data, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("read dst after failed copy: %v", err)
	}
	if string(data) != "original content" {
		t.Errorf("dst after failed copy = %q, want untouched %q", data, "original content")
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

// TestRunRestoreBackupRollsBackKeyWhenDatabaseWriteFails forces the final
// database.RestoreDatabaseFile step to fail (by making its target directory
// read-only) after the secret-key step has already succeeded, and checks
// that the live secret.key is rolled back to its pre-restore content rather
// than left paired with the database that was never actually replaced.
func TestRunRestoreBackupRollsBackKeyWhenDatabaseWriteFails(t *testing.T) {
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
	liveSecretKeyPath := filepath.Join(targetDir, "secret.key")
	if err := os.WriteFile(liveSecretKeyPath, []byte("stale-target-key"), 0600); err != nil {
		t.Fatalf("seed target secret.key: %v", err)
	}

	// secret.key lives directly in targetDir (writable); the database lives
	// in a separate, pre-created, read-only subdirectory so only the final
	// RestoreDatabaseFile write fails - the earlier secret-key step must
	// still succeed for this to actually test the rollback.
	dbDir := filepath.Join(targetDir, "dbdir")
	if err := os.Mkdir(dbDir, 0500); err != nil {
		t.Fatalf("mkdir read-only dbdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dbDir, 0700) })

	cfg := &config.Config{
		Panel:  config.PanelConfig{DataDir: targetDir},
		SQLite: config.SQLiteConfig{Path: filepath.Join(dbDir, "server-panel.db")},
	}

	if err := runRestoreBackup(cfg, archivePath); err == nil {
		t.Fatal("expected runRestoreBackup to fail when the database directory is read-only")
	}

	data, err := os.ReadFile(liveSecretKeyPath)
	if err != nil {
		t.Fatalf("read live secret.key after failed restore: %v", err)
	}
	if string(data) != "stale-target-key" {
		t.Errorf("secret.key after failed database write = %q, want rollback to original %q", data, "stale-target-key")
	}
}
