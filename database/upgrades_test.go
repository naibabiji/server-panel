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
