package executor

import (
	"archive/zip"
	"bytes"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/naibabiji/server-panel/database"
	_ "modernc.org/sqlite"
)

func testBackupCryptoDB(t *testing.T, password string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(`CREATE TABLE settings (skey TEXT PRIMARY KEY, svalue TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	recipient, wrapped, err := PrepareBackupEncryption(db, "", password, false)
	if err != nil {
		t.Fatal(err)
	}
	tx, _ := db.Begin()
	if err := SaveBackupEncryptionSettings(tx, recipient, wrapped); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestPortableBackupRoundTripAndWrongPassword(t *testing.T) {
	const password = "correct horse battery staple"
	db := testBackupCryptoDB(t, password)
	oldBackupDB := backupDB
	backupDB = func() *sql.DB { return db }
	t.Cleanup(func() { backupDB = oldBackupDB })

	dir := t.TempDir()
	dbPath := filepath.Join(dir, "server-panel.db")
	writeRealSQLiteFile(t, dbPath)
	keyPath := filepath.Join(dir, "secret.key")
	if err := os.WriteFile(keyPath, []byte("portable-secret-key"), 0600); err != nil {
		t.Fatal(err)
	}
	legacy, err := database.CreateFullBackupArchive(dbPath, keyPath, dir)
	if err != nil {
		t.Fatal(err)
	}
	portable, err := createPortableBackup(legacy, dir)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Ext(portable) != ".spbackup" {
		t.Fatalf("unexpected portable name: %s", portable)
	}

	if _, err := decryptPortableBackup(portable, "wrong password", dir); err == nil {
		t.Fatal("wrong password unexpectedly decrypted backup")
	}
	restoredArchive, err := decryptPortableBackup(portable, password, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(restoredArchive)
	extractDir := t.TempDir()
	restoredDB, restoredKey, err := database.ExtractFullBackupArchive(restoredArchive, extractDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := database.VerifyDBBackup(restoredDB); err != nil {
		t.Fatal(err)
	}
	key, err := os.ReadFile(restoredKey)
	if err != nil {
		t.Fatal(err)
	}
	if string(key) != "portable-secret-key" {
		t.Fatalf("secret key = %q", key)
	}
}

func TestPortableBackupRejectsUnexpectedZipEntry(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.spbackup")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	w, _ := zw.CreateHeader(&zip.FileHeader{Name: "unexpected", Method: zip.Store})
	_, _ = w.Write([]byte("bad"))
	_ = zw.Close()
	_ = f.Close()
	if _, err := decryptPortableBackup(path, "password", t.TempDir()); err == nil {
		t.Fatal("unexpected zip content was accepted")
	}
}

func TestPortableBackupRejectsTamperedPayload(t *testing.T) {
	const password = "tamper-test-password"
	db := testBackupCryptoDB(t, password)
	oldBackupDB := backupDB
	backupDB = func() *sql.DB { return db }
	t.Cleanup(func() { backupDB = oldBackupDB })
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "server-panel.db")
	writeRealSQLiteFile(t, dbPath)
	legacy, err := database.CreateFullBackupArchive(dbPath, filepath.Join(dir, "missing.key"), dir)
	if err != nil {
		t.Fatal(err)
	}
	portable, err := createPortableBackup(legacy, dir)
	if err != nil {
		t.Fatal(err)
	}

	zr, err := zip.OpenReader(portable)
	if err != nil {
		t.Fatal(err)
	}
	tampered := filepath.Join(dir, "tampered.spbackup")
	out, _ := os.Create(tampered)
	zw := zip.NewWriter(out)
	for _, entry := range zr.File {
		data, readErr := readZipEntry(entry, maxDecryptedBackupSize)
		if readErr != nil {
			t.Fatal(readErr)
		}
		if entry.Name == "payload.age" {
			data[len(data)/2] ^= 0xff
		}
		if err := writeStoredZipEntry(zw, entry.Name, data); err != nil {
			t.Fatal(err)
		}
	}
	_ = zr.Close()
	_ = zw.Close()
	_ = out.Close()
	if _, err := decryptPortableBackup(tampered, password, dir); err == nil {
		t.Fatal("tampered encrypted payload was accepted")
	}
}

func TestPortableBackupRejectsOversizedIdentityEntry(t *testing.T) {
	path := filepath.Join(t.TempDir(), "oversized.spbackup")
	f, _ := os.Create(path)
	zw := zip.NewWriter(f)
	manifest := []byte(`{"format":"server-panel-backup","version":1,"created_at":"2026-01-01T00:00:00Z","cipher":"age-v1"}`)
	_ = writeStoredZipEntry(zw, "manifest.json", manifest)
	_ = writeStoredZipEntry(zw, "identity.age", bytes.Repeat([]byte{'x'}, maxWrappedIdentitySize+1))
	_ = writeStoredZipEntry(zw, "payload.age", []byte("x"))
	_ = zw.Close()
	_ = f.Close()
	if _, err := decryptPortableBackup(path, "password", t.TempDir()); err == nil {
		t.Fatal("oversized identity entry was accepted")
	}
}

func TestChangingViewPasswordRewrapsSameBackupIdentity(t *testing.T) {
	db := testBackupCryptoDB(t, "old-password")
	var oldRecipient string
	_ = db.QueryRow("SELECT svalue FROM settings WHERE skey = ?", backupRecipientSetting).Scan(&oldRecipient)
	recipient, wrapped, err := PrepareBackupEncryption(db, "old-password", "new-password", false)
	if err != nil {
		t.Fatal(err)
	}
	if recipient != oldRecipient {
		t.Fatal("password change replaced the backup keypair")
	}
	if _, err := unwrapBackupIdentity(wrapped, "old-password"); err == nil {
		t.Fatal("old password still unwraps identity")
	}
	if _, err := unwrapBackupIdentity(wrapped, "new-password"); err != nil {
		t.Fatalf("new password cannot unwrap identity: %v", err)
	}
}
