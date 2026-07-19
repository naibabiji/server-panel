package database

import (
	"archive/tar"
	"compress/gzip"
	"database/sql"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// secretKeyArchiveName and dbArchiveName are the fixed entry names full
// backup archives use, independent of the source files' on-disk names.
const (
	dbArchiveName        = "server-panel.db"
	secretKeyArchiveName = "secret.key"
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
	// Read-only single-connection integrity check - no WAL/pragma tuning
	// needed here (and modernc.org/sqlite doesn't recognize the mattn-style
	// _journal_mode= DSN param anyway, see database.Open).
	db, err := sql.Open("sqlite", path)
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

// CreateFullBackupArchive bundles a database snapshot and the at-rest secret
// encryption key into a single gzip'd tar file in dir, and returns its path.
// secretKeyPath is optional: if it doesn't exist (the panel never wrote a
// secret.key file, e.g. no secrets have been saved yet), the archive is
// still created without it. dbPath is removed once its contents are safely
// inside the archive, since keeping the loose file around would double the
// backup directory's size for no benefit.
//
// Without secret.key, restoring this archive onto a panel that regenerated
// a new key (e.g. after a fresh install) leaves every encrypted field
// (SSH/panel passwords, etc.) permanently undecryptable - the archive
// format exists specifically to prevent that.
func CreateFullBackupArchive(dbPath, secretKeyPath, dir string) (string, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", fmt.Errorf("failed to create backup directory: %w", err)
	}
	archivePath := filepath.Join(dir, fmt.Sprintf("server-panel-backup.%s.tar.gz", time.Now().UTC().Format("20060102-150405")))

	if err := writeBackupArchive(archivePath, dbPath, secretKeyPath); err != nil {
		_ = os.Remove(archivePath)
		return "", err
	}
	_ = os.Remove(dbPath)
	return archivePath, nil
}

func writeBackupArchive(archivePath, dbPath, secretKeyPath string) error {
	f, err := os.OpenFile(archivePath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return fmt.Errorf("failed to create archive: %w", err)
	}
	defer f.Close()

	gw := gzip.NewWriter(f)
	tw := tar.NewWriter(gw)

	if err := addFileToTar(tw, dbPath, dbArchiveName); err != nil {
		return fmt.Errorf("failed to add database to archive: %w", err)
	}
	if _, statErr := os.Stat(secretKeyPath); statErr == nil {
		if err := addFileToTar(tw, secretKeyPath, secretKeyArchiveName); err != nil {
			return fmt.Errorf("failed to add secret key to archive: %w", err)
		}
	} else if !os.IsNotExist(statErr) {
		// Anything other than "the key file doesn't exist yet" (permission
		// denied, I/O error, ...) must fail the backup loudly rather than
		// silently ship a "successful" archive that's missing the key.
		return fmt.Errorf("failed to stat secret key: %w", statErr)
	}

	if err := tw.Close(); err != nil {
		return fmt.Errorf("failed to finalize archive: %w", err)
	}
	if err := gw.Close(); err != nil {
		return fmt.Errorf("failed to finalize archive: %w", err)
	}
	return f.Close()
}

func addFileToTar(tw *tar.Writer, path, nameInArchive string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	hdr := &tar.Header{
		Name:    nameInArchive,
		Mode:    0600,
		Size:    int64(len(data)),
		ModTime: time.Now(),
	}
	if err := tw.WriteHeader(hdr); err != nil {
		return err
	}
	_, err = tw.Write(data)
	return err
}

// ExtractFullBackupArchive extracts a CreateFullBackupArchive archive into
// destDir. secretKeyPath is "" if the archive doesn't contain a secret key
// (see CreateFullBackupArchive). Entry names are taken by filepath.Base
// before joining with destDir, so a maliciously crafted archive can't write
// outside destDir via ".." path segments.
func ExtractFullBackupArchive(archivePath, destDir string) (dbPath, secretKeyPath string, err error) {
	f, openErr := os.Open(archivePath)
	if openErr != nil {
		return "", "", fmt.Errorf("failed to open backup archive: %w", openErr)
	}
	defer f.Close()

	gr, gzErr := gzip.NewReader(f)
	if gzErr != nil {
		return "", "", fmt.Errorf("failed to read backup archive: %w", gzErr)
	}
	defer gr.Close()

	if mkErr := os.MkdirAll(destDir, 0700); mkErr != nil {
		return "", "", mkErr
	}

	tr := tar.NewReader(gr)
	for {
		hdr, nextErr := tr.Next()
		if nextErr == io.EOF {
			break
		}
		if nextErr != nil {
			return "", "", fmt.Errorf("corrupt backup archive: %w", nextErr)
		}
		name := filepath.Base(hdr.Name)
		if name != dbArchiveName && name != secretKeyArchiveName {
			continue
		}
		outPath := filepath.Join(destDir, name)
		out, createErr := os.OpenFile(outPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
		if createErr != nil {
			return "", "", createErr
		}
		if _, copyErr := io.Copy(out, tr); copyErr != nil {
			out.Close()
			return "", "", fmt.Errorf("failed to extract %s: %w", name, copyErr)
		}
		if closeErr := out.Close(); closeErr != nil {
			return "", "", closeErr
		}
		switch name {
		case dbArchiveName:
			dbPath = outPath
		case secretKeyArchiveName:
			secretKeyPath = outPath
		}
	}
	if dbPath == "" {
		return "", "", fmt.Errorf("backup archive missing %s", dbArchiveName)
	}
	return dbPath, secretKeyPath, nil
}
