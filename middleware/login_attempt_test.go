package middleware

import (
	"database/sql"
	"testing"

	"github.com/naibabiji/server-panel/database"
	_ "modernc.org/sqlite"
)

// newLoginTestDB spins up an in-memory SQLite with the schema the login
// attempt tracker and ban logic depend on, and points the global
// database.DB at it (executor.BanIP reads from database.GetDB()).
func newLoginTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	oldDB := database.DB
	database.DB = db
	t.Cleanup(func() {
		database.DB = oldDB
		_ = db.Close()
	})

	execLoginSQL(t, db, `CREATE TABLE login_attempts (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		ip_address TEXT NOT NULL,
		attempt_type TEXT NOT NULL,
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP)`)
	execLoginSQL(t, db, `CREATE TABLE firewall_bans (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		ip_address TEXT NOT NULL,
		reason TEXT NOT NULL,
		source TEXT NOT NULL,
		expires_at DATETIME,
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		unbanned_at DATETIME)`)
	execLoginSQL(t, db, `CREATE TABLE whitelist (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		ip_address TEXT NOT NULL UNIQUE,
		notes TEXT NOT NULL DEFAULT '',
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP)`)
	return db
}

func execLoginSQL(t *testing.T, db *sql.DB, stmt string, args ...interface{}) {
	t.Helper()
	if _, err := db.Exec(stmt, args...); err != nil {
		t.Fatalf("exec %q: %v", stmt, err)
	}
}

// TestLoginAttemptTrackerBansAfterMaxAttempts verifies the core defense:
// after MaxLoginAttempts failed attempts from one IP, that IP is banned and
// a firewall_bans row is written.
func TestLoginAttemptTrackerBansAfterMaxAttempts(t *testing.T) {
	db := newLoginTestDB(t)
	tracker := NewLoginAttemptTracker(db, 3, 5, 24)

	const ip = "203.0.113.50"
	if tracker.IsBanned(ip) {
		t.Fatal("fresh IP should not be banned")
	}

	tracker.RecordAttempt(ip, "web")
	tracker.RecordAttempt(ip, "web")
	if tracker.IsBanned(ip) {
		t.Fatal("should not be banned after 2 attempts when max is 3")
	}

	tracker.RecordAttempt(ip, "web")
	if !tracker.IsBanned(ip) {
		t.Fatal("should be banned after 3 failed attempts")
	}

	var count int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM firewall_bans WHERE ip_address = ? AND source = 'panel' AND unbanned_at IS NULL`,
		ip,
	).Scan(&count); err != nil {
		t.Fatalf("query ban row: %v", err)
	}
	if count != 1 {
		t.Fatalf("active firewall_bans rows = %d, want 1", count)
	}
}

// TestLoginAttemptTrackerWhitelistedIPNotBanned verifies a whitelisted IP can
// never be banned by the brute-force tracker (BanIP skips whitelisted IPs).
func TestLoginAttemptTrackerWhitelistedIPNotBanned(t *testing.T) {
	db := newLoginTestDB(t)
	execLoginSQL(t, db, `INSERT INTO whitelist (ip_address, notes) VALUES ('198.51.100.7', 'whitelisted')`)
	tracker := NewLoginAttemptTracker(db, 2, 5, 24)

	const ip = "198.51.100.7"
	for i := 0; i < 5; i++ {
		tracker.RecordAttempt(ip, "web")
	}
	if tracker.IsBanned(ip) {
		t.Fatal("whitelisted IP must never be banned, even after many failures")
	}
}

// TestLoginAttemptTrackerLoopbackNotBanned verifies loopback is never banned,
// so a same-host reverse proxy setup cannot lock everyone out.
func TestLoginAttemptTrackerLoopbackNotBanned(t *testing.T) {
	db := newLoginTestDB(t)
	tracker := NewLoginAttemptTracker(db, 2, 5, 24)

	const ip = "127.0.0.1"
	for i := 0; i < 5; i++ {
		tracker.RecordAttempt(ip, "web")
	}
	if tracker.IsBanned(ip) {
		t.Fatal("loopback must never be banned")
	}
}

// TestLoginAttemptTrackerIgnoresStaleAttempts verifies that failures outside
// the attempt window do not count toward the ban threshold.
func TestLoginAttemptTrackerIgnoresStaleAttempts(t *testing.T) {
	db := newLoginTestDB(t)
	tracker := NewLoginAttemptTracker(db, 3, 5, 24)

	const ip = "203.0.113.99"
	// 10 attempts far in the past, outside the 5-minute window.
	for i := 0; i < 10; i++ {
		execLoginSQL(t, db,
			`INSERT INTO login_attempts (ip_address, attempt_type, created_at) VALUES (?, 'web', '2000-01-01 00:00:00')`,
			ip)
	}
	if tracker.IsBanned(ip) {
		t.Fatal("stale attempts alone must not cause a ban")
	}

	tracker.RecordAttempt(ip, "web")
	tracker.RecordAttempt(ip, "web")
	if tracker.IsBanned(ip) {
		t.Fatal("only 2 fresh attempts, should not be banned yet (max 3)")
	}
	tracker.RecordAttempt(ip, "web")
	if !tracker.IsBanned(ip) {
		t.Fatal("3 fresh attempts should trigger the ban despite the stale rows")
	}
}

// TestLoginAttemptTrackerClearAttemptsResetsThreshold verifies the fix for the
// "success does not reset counter" bug: after ClearAttempts, a single new
// failure must not immediately ban, but re-accumulating to the threshold still
// bans. This mirrors what happens on a successful login.
func TestLoginAttemptTrackerClearAttemptsResetsThreshold(t *testing.T) {
	db := newLoginTestDB(t)
	tracker := NewLoginAttemptTracker(db, 3, 5, 24)

	const ip = "203.0.113.77"
	tracker.RecordAttempt(ip, "web")
	tracker.RecordAttempt(ip, "web")
	// 模拟登录成功：清空该 IP 失败计数
	tracker.ClearAttempts(ip)

	// 一次新失败不应立刻封禁（窗口已被重置）
	tracker.RecordAttempt(ip, "web")
	if tracker.IsBanned(ip) {
		t.Fatal("after ClearAttempts a single new failure should not ban")
	}

	// 重新累计到阈值仍应封禁
	tracker.RecordAttempt(ip, "web")
	tracker.RecordAttempt(ip, "web")
	if !tracker.IsBanned(ip) {
		t.Fatal("re-accumulating to the threshold should still ban")
	}
}
