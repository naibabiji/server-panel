package executor

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func TestCleanupMetricHistoryVacuumsAfterThreshold(t *testing.T) {
	metricCleanupVacuumMu.Lock()
	metricCleanupLastVacuum = time.Time{}
	metricCleanupVacuumMu.Unlock()
	t.Cleanup(func() {
		metricCleanupVacuumMu.Lock()
		metricCleanupLastVacuum = time.Time{}
		metricCleanupVacuumMu.Unlock()
	})
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "metrics.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()
	for _, stmt := range []string{
		`CREATE TABLE metrics (id INTEGER PRIMARY KEY, payload TEXT, recorded_at DATETIME)`,
		`CREATE TABLE host_metrics (id INTEGER PRIMARY KEY, payload TEXT, recorded_at DATETIME)`,
		`CREATE TABLE settings (skey TEXT PRIMARY KEY, svalue TEXT NOT NULL DEFAULT '')`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("create metric table: %v", err)
		}
	}
	payload := strings.Repeat("x", 4096)
	for i := int64(0); i < metricCleanupVacuumThreshold; i++ {
		if _, err := db.Exec(`INSERT INTO metrics (payload, recorded_at) VALUES (?, datetime('now', '-60 days'))`, payload); err != nil {
			t.Fatalf("insert old metric %d: %v", i, err)
		}
	}

	var pagesBefore int
	if err := db.QueryRow("PRAGMA page_count").Scan(&pagesBefore); err != nil {
		t.Fatalf("read page count before cleanup: %v", err)
	}
	cleanupMetricHistory(db, 30)

	var rows, pagesAfter, freePages int
	if err := db.QueryRow("SELECT COUNT(*) FROM metrics").Scan(&rows); err != nil {
		t.Fatalf("count metrics after cleanup: %v", err)
	}
	if err := db.QueryRow("PRAGMA page_count").Scan(&pagesAfter); err != nil {
		t.Fatalf("read page count after cleanup: %v", err)
	}
	if err := db.QueryRow("PRAGMA freelist_count").Scan(&freePages); err != nil {
		t.Fatalf("read freelist after cleanup: %v", err)
	}
	if rows != 0 {
		t.Fatalf("old metric rows after cleanup = %d, want 0", rows)
	}
	if pagesAfter >= pagesBefore {
		t.Fatalf("page count after cleanup = %d, want less than %d", pagesAfter, pagesBefore)
	}
	if freePages != 0 {
		t.Fatalf("freelist pages after VACUUM = %d, want 0", freePages)
	}

	for i := int64(0); i < metricCleanupVacuumThreshold; i++ {
		if _, err := db.Exec(`INSERT INTO metrics (payload, recorded_at) VALUES (?, datetime('now', '-60 days'))`, payload); err != nil {
			t.Fatalf("insert second batch metric %d: %v", i, err)
		}
	}
	// Clear the process-local cache to simulate a panel restart; the durable
	// settings timestamp must still prevent another VACUUM within 24 hours.
	metricCleanupVacuumMu.Lock()
	metricCleanupLastVacuum = time.Time{}
	metricCleanupVacuumMu.Unlock()
	cleanupMetricHistory(db, 30)
	if err := db.QueryRow("PRAGMA freelist_count").Scan(&freePages); err != nil {
		t.Fatalf("read freelist after throttled cleanup: %v", err)
	}
	if freePages == 0 {
		t.Fatal("second cleanup VACUUM was not throttled within 24 hours")
	}
}
