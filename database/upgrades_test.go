package database

import (
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"
)

func TestCurrentSchemaVersionUsesSemanticComparison(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	oldDB := DB
	DB = db
	t.Cleanup(func() {
		DB = oldDB
		_ = db.Close()
	})

	if _, err := DB.Exec(`CREATE TABLE schema_version (
		version TEXT NOT NULL,
		updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	)`); err != nil {
		t.Fatalf("create schema_version: %v", err)
	}
	for _, version := range []string{"1.0.0", "1.9.0", "1.10.0"} {
		if _, err := DB.Exec("INSERT INTO schema_version (version) VALUES (?)", version); err != nil {
			t.Fatalf("insert version %s: %v", version, err)
		}
	}

	got, err := currentSchemaVersion()
	if err != nil {
		t.Fatalf("currentSchemaVersion: %v", err)
	}
	if got != "1.10.0" {
		t.Fatalf("currentSchemaVersion() = %q, want 1.10.0", got)
	}
}

func withTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	oldDB := DB
	DB = db
	t.Cleanup(func() {
		DB = oldDB
		_ = db.Close()
	})
	return db
}

// Legacy install: servers table predates the tcp_reachable columns, so the
// upgrade must add them via ALTER TABLE.
func TestAddTCPReachabilityColumnsAddsMissingColumns(t *testing.T) {
	withTestDB(t)
	if _, err := DB.Exec(`CREATE TABLE servers (id INTEGER PRIMARY KEY)`); err != nil {
		t.Fatalf("create servers: %v", err)
	}

	if err := addTCPReachabilityColumns(); err != nil {
		t.Fatalf("addTCPReachabilityColumns: %v", err)
	}

	for _, col := range []string{"tcp_reachable", "tcp_reachable_checked_at"} {
		exists, err := columnExists("servers", col)
		if err != nil {
			t.Fatalf("columnExists(%q): %v", col, err)
		}
		if !exists {
			t.Fatalf("column %q was not added", col)
		}
	}
}

// Fresh install: migrations.go's CREATE TABLE already includes both columns
// (they're part of the baseline schema for new installs), so the upgrade
// must be a no-op instead of erroring on an already-existing column.
func TestAddTCPReachabilityColumnsIsIdempotentOnFreshSchema(t *testing.T) {
	withTestDB(t)
	if _, err := DB.Exec(`CREATE TABLE servers (
		id INTEGER PRIMARY KEY,
		tcp_reachable INTEGER,
		tcp_reachable_checked_at DATETIME
	)`); err != nil {
		t.Fatalf("create servers: %v", err)
	}

	if err := addTCPReachabilityColumns(); err != nil {
		t.Fatalf("addTCPReachabilityColumns on fresh schema: %v", err)
	}
	// Running it twice must also be safe - RunUpgrades tracks schema_version
	// so this shouldn't normally happen, but the function itself should
	// still be safely re-runnable.
	if err := addTCPReachabilityColumns(); err != nil {
		t.Fatalf("addTCPReachabilityColumns second run: %v", err)
	}
}
