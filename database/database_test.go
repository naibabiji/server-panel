package database

import (
	"path/filepath"
	"testing"
)

func TestOpenSwitchesToWALWithWorkingBusyTimeoutAndForeignKeys(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "server-panel.db")
	if err := Open(dbPath); err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = Close() })

	var journalMode string
	if err := DB.QueryRow("PRAGMA journal_mode;").Scan(&journalMode); err != nil {
		t.Fatalf("read journal_mode: %v", err)
	}
	if journalMode != "wal" {
		t.Fatalf("journal_mode = %q, want %q", journalMode, "wal")
	}

	var foreignKeys int
	if err := DB.QueryRow("PRAGMA foreign_keys;").Scan(&foreignKeys); err != nil {
		t.Fatalf("read foreign_keys: %v", err)
	}
	if foreignKeys != 1 {
		t.Fatalf("foreign_keys = %d, want 1", foreignKeys)
	}

	var busyTimeout int
	if err := DB.QueryRow("PRAGMA busy_timeout;").Scan(&busyTimeout); err != nil {
		t.Fatalf("read busy_timeout: %v", err)
	}
	if busyTimeout < 5000 {
		t.Fatalf("busy_timeout = %d, want >= 5000", busyTimeout)
	}
}

// Regression test for the actual bug this guards against: PRAGMA
// journal_mode=WAL does not return a SQL error when it fails to switch
// (e.g. no shared-memory support), it silently keeps the previous mode.
// An in-memory database is a real, always-available case where the
// switch to WAL can never succeed (SQLite reports "memory" regardless of
// what's requested), which is exactly what verifyPragmas must catch
// instead of Open() reporting success anyway.
func TestOpenFailsFastWhenWALDoesNotActuallyEngage(t *testing.T) {
	if err := Open(":memory:"); err == nil {
		_ = Close()
		t.Fatal("Open(\":memory:\") succeeded, want an error because WAL can never engage on an in-memory database")
	}
	if DB != nil {
		t.Fatal("DB package var should remain nil after a failed Open")
	}
}
