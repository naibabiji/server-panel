package database

import (
	"os"
	"path/filepath"
	"testing"
)

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

func TestExtractFullBackupArchiveRejectsMissingDB(t *testing.T) {
	dir := t.TempDir()
	// An archive with no server-panel.db entry (e.g. someone renamed/corrupted it).
	archivePath, err := CreateFullBackupArchive(mustWriteTempFile(t, dir, "server-panel.db", "content"), filepath.Join(dir, "missing.key"), dir)
	if err != nil {
		t.Fatalf("CreateFullBackupArchive: %v", err)
	}
	// Sanity: a well-formed archive still extracts fine.
	if _, _, err := ExtractFullBackupArchive(archivePath, t.TempDir()); err != nil {
		t.Fatalf("well-formed archive should extract: %v", err)
	}

	if _, _, err := ExtractFullBackupArchive(filepath.Join(dir, "nonexistent.tar.gz"), t.TempDir()); err == nil {
		t.Fatal("expected error extracting a nonexistent archive")
	}
}

func mustWriteTempFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}
