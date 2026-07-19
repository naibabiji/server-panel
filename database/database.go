package database

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

var DB *sql.DB

func Open(dbPath string) error {
	dir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("failed to create database directory: %w", err)
	}

	// modernc.org/sqlite only recognizes _pragma=<stmt> (repeated) and
	// _time_format as DSN query params - the go-sqlite3 (mattn) style
	// _journal_mode=/_busy_timeout=/_synchronous=/_cache_size=/_foreign_keys=
	// keys used here previously are silently ignored by this driver, which
	// left the database running in the default rollback journal mode with
	// busy_timeout=0 (see modernc.org/sqlite@v1.33.1/sqlite.go applyQueryParams).
	dsn := dbPath + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=synchronous(NORMAL)&_pragma=cache_size(-8000)&_pragma=foreign_keys(ON)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return fmt.Errorf("failed to open sqlite: %w", err)
	}

	db.SetMaxOpenConns(4)
	db.SetMaxIdleConns(2)
	db.SetConnMaxIdleTime(5 * time.Minute)
	db.SetConnMaxLifetime(30 * time.Minute)

	if err := db.Ping(); err != nil {
		db.Close()
		return fmt.Errorf("sqlite ping failed: %w", err)
	}

	if err := verifyPragmas(db); err != nil {
		db.Close()
		return err
	}

	DB = db
	return nil
}

// verifyPragmas guards against PRAGMA journal_mode=WAL silently no-op'ing:
// SQLite doesn't return an error if the switch fails (e.g. the file is held
// open by another process/tool, or the filesystem doesn't support the shared
// memory WAL needs) - it just keeps the previous mode. Failing fast here is
// much better than discovering it later as a "database is locked" error.
func verifyPragmas(db *sql.DB) error {
	var journalMode string
	if err := db.QueryRow("PRAGMA journal_mode;").Scan(&journalMode); err != nil {
		return fmt.Errorf("failed to read journal_mode: %w", err)
	}
	if !strings.EqualFold(journalMode, "wal") {
		return fmt.Errorf("sqlite did not switch to WAL journal mode (got %q) - the database file may be open elsewhere, or its filesystem doesn't support WAL's shared memory file", journalMode)
	}

	var foreignKeys int
	if err := db.QueryRow("PRAGMA foreign_keys;").Scan(&foreignKeys); err != nil {
		return fmt.Errorf("failed to read foreign_keys: %w", err)
	}
	if foreignKeys != 1 {
		return fmt.Errorf("sqlite foreign_keys pragma is off (got %d)", foreignKeys)
	}

	var busyTimeout int
	if err := db.QueryRow("PRAGMA busy_timeout;").Scan(&busyTimeout); err != nil {
		return fmt.Errorf("failed to read busy_timeout: %w", err)
	}
	if busyTimeout < 5000 {
		return fmt.Errorf("sqlite busy_timeout is %dms, want >= 5000ms", busyTimeout)
	}

	return nil
}

func RunMigrations() error {
	for _, stmt := range migrations {
		if _, err := DB.Exec(stmt); err != nil {
			if strings.Contains(err.Error(), "duplicate column name") {
				continue
			}
			return fmt.Errorf("migration failed: %w\nSQL: %s", err, stmt[:100])
		}
	}
	return nil
}

func GetDB() *sql.DB {
	return DB
}

func Close() error {
	if DB != nil {
		err := DB.Close()
		DB = nil
		return err
	}
	return nil
}
