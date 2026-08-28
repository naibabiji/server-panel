package database

import (
	"archive/tar"
	"compress/gzip"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBackupDatabaseCreatesVerifiableCompressedSnapshot(t *testing.T) {
	dir := t.TempDir()
	livePath := filepath.Join(dir, "live.db")
	liveDB, err := sql.Open("sqlite", livePath)
	if err != nil {
		t.Fatalf("open live db: %v", err)
	}
	t.Cleanup(func() { _ = liveDB.Close() })
	if _, err := liveDB.Exec("CREATE TABLE metrics (id INTEGER PRIMARY KEY, value TEXT); INSERT INTO metrics VALUES (1, 'history')"); err != nil {
		t.Fatalf("seed live db: %v", err)
	}

	oldDB := DB
	DB = liveDB
	t.Cleanup(func() { DB = oldDB })

	backupPath, err := BackupDatabase(filepath.Join(dir, "backups"))
	if err != nil {
		t.Fatalf("BackupDatabase: %v", err)
	}
	if !strings.HasSuffix(backupPath, ".gz") {
		t.Fatalf("backup path = %q, want .gz suffix", backupPath)
	}
	if err := VerifyDBBackup(backupPath); err != nil {
		t.Fatalf("VerifyDBBackup: %v", err)
	}

	restoredPath := filepath.Join(dir, "restored.db")
	if err := RestoreDatabaseFile(backupPath, restoredPath); err != nil {
		t.Fatalf("RestoreDatabaseFile: %v", err)
	}
	restoredDB, err := sql.Open("sqlite", restoredPath)
	if err != nil {
		t.Fatalf("open restored db: %v", err)
	}
	defer restoredDB.Close()
	var value string
	if err := restoredDB.QueryRow("SELECT value FROM metrics WHERE id = 1").Scan(&value); err != nil {
		t.Fatalf("read restored monitoring history: %v", err)
	}
	if value != "history" {
		t.Fatalf("restored value = %q, want history", value)
	}
}

func TestBackupDatabaseForUserArchiveExcludesMonitoringHistory(t *testing.T) {
	dir := t.TempDir()
	livePath := filepath.Join(dir, "live.db")
	liveDB, err := sql.Open("sqlite", livePath)
	if err != nil {
		t.Fatalf("open live db: %v", err)
	}
	t.Cleanup(func() { _ = liveDB.Close() })
	for _, stmt := range []string{
		"CREATE TABLE metrics (id INTEGER PRIMARY KEY, value REAL)",
		"CREATE TABLE host_metrics (id INTEGER PRIMARY KEY, value REAL)",
		"CREATE TABLE settings (skey TEXT PRIMARY KEY, svalue TEXT)",
		"INSERT INTO metrics VALUES (1, 12.5)",
		"INSERT INTO host_metrics VALUES (1, 25.5)",
		"INSERT INTO settings VALUES ('site_name', 'example')",
	} {
		if _, err := liveDB.Exec(stmt); err != nil {
			t.Fatalf("seed live db with %q: %v", stmt, err)
		}
	}

	oldDB := DB
	DB = liveDB
	t.Cleanup(func() { DB = oldDB })

	backupPath, err := BackupDatabaseForUserArchive(filepath.Join(dir, "backups"))
	if err != nil {
		t.Fatalf("BackupDatabaseForUserArchive: %v", err)
	}

	backupDB, err := sql.Open("sqlite", backupPath)
	if err != nil {
		t.Fatalf("open backup db: %v", err)
	}
	defer backupDB.Close()

	for _, table := range []string{"metrics", "host_metrics"} {
		var count int
		if err := backupDB.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count); err != nil {
			t.Fatalf("count backup table %s: %v", table, err)
		}
		if count != 0 {
			t.Errorf("backup table %s has %d rows, want 0", table, count)
		}
		if err := liveDB.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count); err != nil {
			t.Fatalf("count live table %s: %v", table, err)
		}
		if count != 1 {
			t.Errorf("live table %s has %d rows, want 1", table, count)
		}
	}

	var siteName string
	if err := backupDB.QueryRow("SELECT svalue FROM settings WHERE skey = 'site_name'").Scan(&siteName); err != nil {
		t.Fatalf("read business data from backup: %v", err)
	}
	if siteName != "example" {
		t.Errorf("backup site_name = %q, want %q", siteName, "example")
	}
}

func TestFullBackupArchiveRoundTrip(t *testing.T) {
	srcDir := t.TempDir()
	dbPath := filepath.Join(srcDir, "server-panel.db")
	secretKeyPath := filepath.Join(srcDir, "secret.key")
	if err := os.WriteFile(dbPath, []byte("fake sqlite content"), 0600); err != nil {
		t.Fatalf("write dbPath: %v", err)
	}
	if err := os.WriteFile(secretKeyPath, []byte("fake-secret-key-base64"), 0600); err != nil {
		t.Fatalf("write secretKeyPath: %v", err)
	}

	archiveDir := t.TempDir()
	archivePath, err := CreateFullBackupArchive(dbPath, secretKeyPath, archiveDir)
	if err != nil {
		t.Fatalf("CreateFullBackupArchive: %v", err)
	}
	if _, err := os.Stat(archivePath); err != nil {
		t.Fatalf("archive not created: %v", err)
	}
	// dbPath's contents now live inside the archive; the loose file should be gone.
	if _, err := os.Stat(dbPath); !os.IsNotExist(err) {
		t.Fatalf("expected dbPath to be removed after bundling, stat err = %v", err)
	}

	destDir := t.TempDir()
	extractedDB, extractedKey, err := ExtractFullBackupArchive(archivePath, destDir)
	if err != nil {
		t.Fatalf("ExtractFullBackupArchive: %v", err)
	}

	dbData, err := os.ReadFile(extractedDB)
	if err != nil {
		t.Fatalf("read extracted db: %v", err)
	}
	if string(dbData) != "fake sqlite content" {
		t.Errorf("extracted db content = %q, want %q", dbData, "fake sqlite content")
	}

	if extractedKey == "" {
		t.Fatal("expected extracted secret key path, got empty string")
	}
	keyData, err := os.ReadFile(extractedKey)
	if err != nil {
		t.Fatalf("read extracted key: %v", err)
	}
	if string(keyData) != "fake-secret-key-base64" {
		t.Errorf("extracted key content = %q, want %q", keyData, "fake-secret-key-base64")
	}
}

func TestFullBackupArchiveWithoutSecretKey(t *testing.T) {
	srcDir := t.TempDir()
	dbPath := filepath.Join(srcDir, "server-panel.db")
	if err := os.WriteFile(dbPath, []byte("fake sqlite content"), 0600); err != nil {
		t.Fatalf("write dbPath: %v", err)
	}
	missingSecretKeyPath := filepath.Join(srcDir, "does-not-exist.key")

	archiveDir := t.TempDir()
	archivePath, err := CreateFullBackupArchive(dbPath, missingSecretKeyPath, archiveDir)
	if err != nil {
		t.Fatalf("CreateFullBackupArchive: %v", err)
	}

	destDir := t.TempDir()
	extractedDB, extractedKey, err := ExtractFullBackupArchive(archivePath, destDir)
	if err != nil {
		t.Fatalf("ExtractFullBackupArchive: %v", err)
	}
	if extractedDB == "" {
		t.Fatal("expected extracted db path")
	}
	if extractedKey != "" {
		t.Errorf("expected no secret key in archive, got %q", extractedKey)
	}
}

func TestExtractFullBackupArchiveRejectsNonexistentArchive(t *testing.T) {
	if _, _, err := ExtractFullBackupArchive(filepath.Join(t.TempDir(), "nonexistent.tar.gz"), t.TempDir()); err == nil {
		t.Fatal("expected error extracting a nonexistent archive")
	}
}

func TestExtractFullBackupArchiveRejectsArchiveMissingDBEntry(t *testing.T) {
	dir := t.TempDir()
	// A well-formed tar.gz that simply never had a server-panel.db entry
	// (e.g. corrupted upstream, or hand-edited) - CreateFullBackupArchive
	// can't produce this shape itself, so build it directly.
	archivePath := filepath.Join(dir, "server-panel-backup.no-db.tar.gz")
	writeRawArchive(t, archivePath, map[string]string{
		secretKeyArchiveName: "fake-secret-key-base64",
	})

	if _, _, err := ExtractFullBackupArchive(archivePath, t.TempDir()); err == nil {
		t.Fatal("expected error extracting an archive missing the database entry")
	}
}

func writeRawArchive(t *testing.T, path string, entries map[string]string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create %s: %v", path, err)
	}
	defer f.Close()

	gw := gzip.NewWriter(f)
	tw := tar.NewWriter(gw)
	for name, content := range entries {
		hdr := &tar.Header{Name: name, Mode: 0600, Size: int64(len(content))}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("write header for %s: %v", name, err)
		}
		if _, err := tw.Write([]byte(content)); err != nil {
			t.Fatalf("write content for %s: %v", name, err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("close tar writer: %v", err)
	}
	if err := gw.Close(); err != nil {
		t.Fatalf("close gzip writer: %v", err)
	}
}
