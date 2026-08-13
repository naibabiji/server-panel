package executor

import (
	"archive/zip"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	_ "modernc.org/sqlite"
)

func fileManagerTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(`CREATE TABLE settings (skey TEXT PRIMARY KEY, svalue TEXT NOT NULL DEFAULT '', updated_at DATETIME DEFAULT CURRENT_TIMESTAMP)`); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestManagedFilesStayInsideAuthorizedRoot(t *testing.T) {
	db := fileManagerTestDB(t)
	dataDir := t.TempDir()
	root := filepath.Join(dataDir, "backups")
	if err := os.MkdirAll(filepath.Join(root, "nested"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "nested", "a.txt"), []byte("ok"), 0644); err != nil {
		t.Fatal(err)
	}

	target, err := ManagedFilePath(db, dataDir, root, "/nested/a.txt", false)
	if err != nil || target != filepath.Join(root, "nested", "a.txt") {
		t.Fatalf("target=%q err=%v", target, err)
	}
	if _, err := ManagedFilePath(db, dataDir, root, "/../../etc/passwd", false); err == nil {
		t.Fatal("path traversal unexpectedly resolved")
	}
	if err := os.Symlink("/etc", filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	if _, err := ManagedFilePath(db, dataDir, root, "/escape/passwd", false); err == nil || !strings.Contains(err.Error(), "超出了授权目录") {
		t.Fatalf("symlink escape error=%v", err)
	}
}

func TestManagedRootCannotBeDeleted(t *testing.T) {
	db := fileManagerTestDB(t)
	dataDir := t.TempDir()
	root := filepath.Join(dataDir, "backups")
	if err := os.MkdirAll(root, 0755); err != nil {
		t.Fatal(err)
	}
	if err := DeleteManagedFile(db, dataDir, root, "/"); err == nil {
		t.Fatal("managed root deletion accepted")
	}
	if _, err := os.Stat(root); err != nil {
		t.Fatalf("managed root was removed: %v", err)
	}
}

func TestCustomRootRejectsServerRootWithGuidance(t *testing.T) {
	_, err := validateCustomFileRoot("/", "/www/server/server-panel")
	if err == nil || !strings.Contains(err.Error(), "SSH/SFTP") || !strings.Contains(err.Error(), "/var/www") {
		t.Fatalf("unexpected guidance: %v", err)
	}
}

func TestManagedFileLifecycle(t *testing.T) {
	db := fileManagerTestDB(t)
	dataDir := t.TempDir()
	root := filepath.Join(dataDir, "backups")
	if err := os.MkdirAll(root, 0755); err != nil {
		t.Fatal(err)
	}
	if err := CreateManagedDirectory(db, dataDir, root, "/", "server-01"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "server-01", "backup.txt"), []byte("data"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := RenameManagedFile(db, dataDir, root, "/server-01/backup.txt", "renamed.txt"); err != nil {
		t.Fatal(err)
	}
	entries, err := ListManagedFiles(db, dataDir, root, "/server-01")
	if err != nil || len(entries) != 1 || entries[0].Name != "renamed.txt" {
		t.Fatalf("entries=%#v err=%v", entries, err)
	}
	if err := DeleteManagedFile(db, dataDir, root, "/server-01"); err != nil {
		t.Fatal(err)
	}
}

func TestConcurrentCustomRootAddsDoNotLoseUpdates(t *testing.T) {
	db := fileManagerTestDB(t)
	db.SetMaxOpenConns(1)
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, root := range []FileRoot{{Path: "/srv/a", Name: "a", Source: "custom"}, {Path: "/srv/b", Name: "b", Source: "custom"}} {
		wg.Add(1)
		go func(root FileRoot) {
			defer wg.Done()
			errs <- addCustomFileRootRecord(db, root)
		}(root)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	roots, err := loadCustomFileRoots(db)
	if err != nil || len(roots) != 2 {
		t.Fatalf("roots=%#v err=%v", roots, err)
	}
}

func TestManagedFilesRejectSSHKeyDirectory(t *testing.T) {
	db := fileManagerTestDB(t)
	dataDir := t.TempDir()
	root := filepath.Join(dataDir, "backups")
	if err := os.MkdirAll(filepath.Join(root, ".ssh"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".ssh", "id_ed25519"), []byte("private"), 0600); err != nil {
		t.Fatal(err)
	}
	entries, err := ListManagedFiles(db, dataDir, root, "/")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Name == ".ssh" {
			t.Fatal(".ssh directory was exposed in listing")
		}
	}
	if _, err := ManagedFilePath(db, dataDir, root, "/.ssh/id_ed25519", false); err == nil {
		t.Fatal("SSH private key path was accessible")
	}
}

func TestManagedFilesRejectSymlinkAliasToSSHKeyDirectory(t *testing.T) {
	db := fileManagerTestDB(t)
	dataDir := t.TempDir()
	root := filepath.Join(dataDir, "backups")
	sshDir := filepath.Join(root, "user", ".ssh")
	if err := os.MkdirAll(sshDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sshDir, "id_ed25519"), []byte("private"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(".ssh", filepath.Join(root, "user", "pubkey")); err != nil {
		t.Fatal(err)
	}
	if _, err := ManagedFilePath(db, dataDir, root, "/user/pubkey/id_ed25519", false); err == nil {
		t.Fatal("symlink alias exposed SSH private key")
	}
	entries, err := ListManagedFiles(db, dataDir, root, "/user")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Name == "pubkey" {
			t.Fatal("symlink alias to .ssh was exposed in listing")
		}
	}
}

func TestExtractManagedZipRejectsTraversal(t *testing.T) {
	db := fileManagerTestDB(t)
	dataDir := t.TempDir()
	root := filepath.Join(dataDir, "backups")
	if err := os.MkdirAll(root, 0755); err != nil {
		t.Fatal(err)
	}
	archivePath := filepath.Join(root, "unsafe.zip")
	f, err := os.Create(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	w, err := zw.Create("../../outside.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("bad")); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := ExtractManagedZip(db, dataDir, root, "/unsafe.zip"); err == nil {
		t.Fatal("zip traversal entry was accepted")
	}
	if _, err := os.Stat(filepath.Join(dataDir, "outside.txt")); !os.IsNotExist(err) {
		t.Fatal("zip traversal wrote outside the managed root")
	}
}

func TestCompressManagedDirectoryCleansTrailingSlash(t *testing.T) {
	db := fileManagerTestDB(t)
	dataDir := t.TempDir()
	root := filepath.Join(dataDir, "backups")
	if err := os.MkdirAll(filepath.Join(root, "folder"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "folder", "a.txt"), []byte("ok"), 0644); err != nil {
		t.Fatal(err)
	}
	dest, err := CompressManagedFile(db, dataDir, root, "/folder/")
	if err != nil {
		t.Fatal(err)
	}
	if dest != filepath.Join(root, "folder.zip") {
		t.Fatalf("archive written to %q", dest)
	}
	if _, err := os.Stat(filepath.Join(root, "folder", ".zip")); !os.IsNotExist(err) {
		t.Fatal("archive was written inside its source directory")
	}
}

func TestExtractManagedDotZipUsesFallbackDirectory(t *testing.T) {
	db := fileManagerTestDB(t)
	dataDir := t.TempDir()
	root := filepath.Join(dataDir, "backups")
	if err := os.MkdirAll(root, 0755); err != nil {
		t.Fatal(err)
	}
	archivePath := filepath.Join(root, ".zip")
	f, err := os.Create(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	w, _ := zw.Create("a.txt")
	_, _ = w.Write([]byte("ok"))
	_ = zw.Close()
	_ = f.Close()
	dest, err := ExtractManagedZip(db, dataDir, root, "/.zip")
	if err != nil {
		t.Fatal(err)
	}
	if dest != filepath.Join(root, "extracted") {
		t.Fatalf("fallback destination=%q", dest)
	}
}
