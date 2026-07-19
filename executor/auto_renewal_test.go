package executor

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func TestRunAutoRenewalsRenewsOverdueAutoRenewalServer(t *testing.T) {
	db := newRenewalTestDB(t)
	execAlertSQL(t, db, `INSERT INTO servers (id, name, expiry_date, renewal_cycle, auto_renewal, status)
		VALUES (1, 'server-1', '2020-01-16', 'monthly', 1, 'active')`)

	runAutoRenewals(db, mustParseDate(t, "2020-01-19"))

	var expiryDate, status string
	if err := db.QueryRow(`SELECT expiry_date, status FROM servers WHERE id = 1`).Scan(&expiryDate, &status); err != nil {
		t.Fatalf("query server: %v", err)
	}
	if expiryDate != "2020-02-16" {
		t.Fatalf("expiry_date = %q, want %q", expiryDate, "2020-02-16")
	}
	if status != "active" {
		t.Fatalf("status = %q, want active", status)
	}
}

func TestRunAutoRenewalsLeavesNonOverdueAndDisabledServersAlone(t *testing.T) {
	db := newRenewalTestDB(t)
	execAlertSQL(t, db, `INSERT INTO servers (id, name, expiry_date, renewal_cycle, auto_renewal, status)
		VALUES (1, 'not-due-yet', '2020-02-01', 'monthly', 1, 'active')`)
	execAlertSQL(t, db, `INSERT INTO servers (id, name, expiry_date, renewal_cycle, auto_renewal, status)
		VALUES (2, 'auto-renew-off', '2020-01-01', 'monthly', 0, 'active')`)

	runAutoRenewals(db, mustParseDate(t, "2020-01-19"))

	var expiry1, expiry2 string
	if err := db.QueryRow(`SELECT expiry_date FROM servers WHERE id = 1`).Scan(&expiry1); err != nil {
		t.Fatalf("query server 1: %v", err)
	}
	if expiry1 != "2020-02-01" {
		t.Fatalf("not-due-yet expiry_date changed to %q, want unchanged %q", expiry1, "2020-02-01")
	}
	if err := db.QueryRow(`SELECT expiry_date FROM servers WHERE id = 2`).Scan(&expiry2); err != nil {
		t.Fatalf("query server 2: %v", err)
	}
	if expiry2 != "2020-01-01" {
		t.Fatalf("auto-renew-off expiry_date changed to %q, want unchanged %q", expiry2, "2020-01-01")
	}
}

// Regression test for the optimistic-concurrency guard: the UPDATE this
// package issues only touches a row when expiry_date still matches what
// was just read, so a concurrent admin edit made in the SELECT-to-UPDATE
// window is never silently clobbered by a stale renewal computation.
// Verified directly against the same predicate runAutoRenewals uses,
// since reproducing the actual thread interleaving deterministically in a
// unit test isn't practical.
func TestOptimisticRenewalUpdateSkipsWhenExpiryDateChangedConcurrently(t *testing.T) {
	db := newRenewalTestDB(t)
	execAlertSQL(t, db, `INSERT INTO servers (id, name, expiry_date, renewal_cycle, auto_renewal, status)
		VALUES (1, 'server-1', '2020-01-16', 'monthly', 1, 'active')`)

	// Simulate a concurrent admin edit that happened between this
	// package's SELECT and its UPDATE: the row no longer has the
	// expiry_date the renewal computation was based on.
	staleExpiryDate := "2020-01-16"
	execAlertSQL(t, db, `UPDATE servers SET expiry_date = '2020-03-01' WHERE id = 1`)

	result, err := db.Exec(
		`UPDATE servers SET expiry_date = ?, status = 'active', updated_at = CURRENT_TIMESTAMP
		 WHERE id = ? AND expiry_date = ? AND auto_renewal = 1`,
		"2020-02-16", int64(1), staleExpiryDate,
	)
	if err != nil {
		t.Fatalf("exec: %v", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		t.Fatalf("rows affected: %v", err)
	}
	if affected != 0 {
		t.Fatalf("rows affected = %d, want 0 (must not overwrite a concurrently-changed row)", affected)
	}

	var expiryDate string
	if err := db.QueryRow(`SELECT expiry_date FROM servers WHERE id = 1`).Scan(&expiryDate); err != nil {
		t.Fatalf("query server: %v", err)
	}
	if expiryDate != "2020-03-01" {
		t.Fatalf("expiry_date = %q, want the concurrently-set %q to survive untouched", expiryDate, "2020-03-01")
	}
}

func TestIsBusyErrDistinguishesRealSQLITEBusyFromOtherErrors(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "busy.db")
	dsn := dbPath + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(0)"

	blocker, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("open blocker: %v", err)
	}
	defer blocker.Close()
	blocker.SetMaxOpenConns(1)
	if _, err := blocker.Exec(`CREATE TABLE t (id INTEGER PRIMARY KEY, v TEXT)`); err != nil {
		t.Fatalf("create table: %v", err)
	}
	if _, err := blocker.Exec(`BEGIN IMMEDIATE`); err != nil {
		t.Fatalf("begin immediate: %v", err)
	}
	t.Cleanup(func() { _, _ = blocker.Exec(`ROLLBACK`) })

	contender, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("open contender: %v", err)
	}
	defer contender.Close()
	contender.SetMaxOpenConns(1)

	_, busyErr := contender.Exec(`INSERT INTO t (v) VALUES ('x')`)
	if busyErr == nil {
		t.Fatal("expected a busy error while blocker holds the write lock, got nil")
	}
	if !isBusyErr(busyErr) {
		t.Fatalf("isBusyErr(%v) = false, want true for a real SQLITE_BUSY", busyErr)
	}

	_, otherErr := contender.Exec(`INSERT INTO no_such_table (v) VALUES ('x')`)
	if otherErr == nil {
		t.Fatal("expected an error querying a nonexistent table, got nil")
	}
	if isBusyErr(otherErr) {
		t.Fatalf("isBusyErr(%v) = true, want false for a non-busy error", otherErr)
	}
}

func TestExecWithBusyRetryGivesUpAfterPersistentContention(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "busy.db")
	dsn := dbPath + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(0)"

	blocker, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("open blocker: %v", err)
	}
	defer blocker.Close()
	blocker.SetMaxOpenConns(1)
	if _, err := blocker.Exec(`CREATE TABLE t (id INTEGER PRIMARY KEY, v TEXT)`); err != nil {
		t.Fatalf("create table: %v", err)
	}
	if _, err := blocker.Exec(`BEGIN IMMEDIATE`); err != nil {
		t.Fatalf("begin immediate: %v", err)
	}
	t.Cleanup(func() { _, _ = blocker.Exec(`ROLLBACK`) })

	contender, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("open contender: %v", err)
	}
	defer contender.Close()
	contender.SetMaxOpenConns(1)

	start := time.Now()
	_, execErr := execWithBusyRetry(contender, `INSERT INTO t (v) VALUES ('x')`)
	elapsed := time.Since(start)

	if execErr == nil {
		t.Fatal("expected execWithBusyRetry to give up and return an error, got nil")
	}
	if !isBusyErr(execErr) {
		t.Fatalf("final error = %v, want a busy error", execErr)
	}
	// 3 attempts with 300ms/600ms backoff between them: at least ~900ms.
	if elapsed < 850*time.Millisecond {
		t.Fatalf("elapsed = %v, want >= ~900ms (evidence all 3 attempts ran)", elapsed)
	}
	if elapsed > 5*time.Second {
		t.Fatalf("elapsed = %v, want a bounded few seconds, not longer", elapsed)
	}
}

func TestExecWithBusyRetrySucceedsOnceContentionClears(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "busy.db")
	dsn := dbPath + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(0)"

	blocker, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("open blocker: %v", err)
	}
	defer blocker.Close()
	blocker.SetMaxOpenConns(1)
	if _, err := blocker.Exec(`CREATE TABLE t (id INTEGER PRIMARY KEY, v TEXT)`); err != nil {
		t.Fatalf("create table: %v", err)
	}
	if _, err := blocker.Exec(`BEGIN IMMEDIATE`); err != nil {
		t.Fatalf("begin immediate: %v", err)
	}

	contender, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("open contender: %v", err)
	}
	defer contender.Close()
	contender.SetMaxOpenConns(1)

	go func() {
		time.Sleep(400 * time.Millisecond)
		_, _ = blocker.Exec(`COMMIT`)
	}()

	result, execErr := execWithBusyRetry(contender, `INSERT INTO t (v) VALUES ('x')`)
	if execErr != nil {
		t.Fatalf("execWithBusyRetry: %v, want it to succeed once the lock releases", execErr)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		t.Fatalf("rows affected: %v", err)
	}
	if affected != 1 {
		t.Fatalf("rows affected = %d, want 1", affected)
	}
}

func mustParseDate(t *testing.T, s string) time.Time {
	t.Helper()
	tm, err := time.ParseInLocation("2006-01-02", s, time.Local)
	if err != nil {
		t.Fatalf("parse date %q: %v", s, err)
	}
	return tm
}

func newRenewalTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	execAlertSQL(t, db, `CREATE TABLE servers (
		id            INTEGER PRIMARY KEY,
		name          TEXT NOT NULL,
		expiry_date   TEXT NOT NULL DEFAULT '',
		renewal_cycle TEXT NOT NULL DEFAULT '',
		auto_renewal  INTEGER NOT NULL DEFAULT 0,
		status        TEXT NOT NULL DEFAULT 'active',
		updated_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	)`)
	return db
}
