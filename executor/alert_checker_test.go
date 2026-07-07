package executor

import (
	"database/sql"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

func TestWebsiteExpiryAlertUsesWebsiteIDAndResolves(t *testing.T) {
	db := newAlertTestDB(t)
	execAlertSQL(t, db, `INSERT INTO customers (id, email) VALUES (1, 'user@example.com')`)
	execAlertSQL(t, db, `INSERT INTO servers (id, name, customer_id, status) VALUES (10, 'server-1', 1, 'active')`)
	execAlertSQL(t, db, `INSERT INTO websites (id, name, server_id, customer_id, expiry_date, status) VALUES (20, 'site-1', 10, 1, date('now','+3 days'), 'active')`)
	execAlertSQL(t, db, `INSERT INTO alert_rules (alert_type, threshold_value, enabled) VALUES ('website_expiry', 7, 1)`)
	var matched int
	if err := db.QueryRow(`SELECT COUNT(*) FROM websites
		WHERE expiry_date != '' AND expiry_date <= date('now', ?) AND expiry_date >= date('now')
		AND status = 'active'`, "+7 days").Scan(&matched); err != nil {
		t.Fatalf("preflight query: %v", err)
	}
	if matched != 1 {
		var expiryDate string
		_ = db.QueryRow("SELECT expiry_date FROM websites WHERE id = 20").Scan(&expiryDate)
		t.Fatalf("preflight matched %d websites, expiry_date=%q", matched, expiryDate)
	}
	var ruleCount int
	if err := db.QueryRow("SELECT COUNT(*) FROM alert_rules WHERE alert_type = ? AND enabled = 1", "website_expiry").Scan(&ruleCount); err != nil {
		t.Fatalf("preflight rule query: %v", err)
	}
	if ruleCount != 1 {
		t.Fatalf("preflight ruleCount = %d, want 1", ruleCount)
	}

	checkExpiryAlerts(db, "website_expiry", "websites", "expiry_date")

	var serverID, websiteID int64
	var resolved int
	if err := db.QueryRow(`SELECT server_id, website_id, resolved FROM alert_log WHERE alert_type = 'website_expiry'`).Scan(&serverID, &websiteID, &resolved); err != nil {
		t.Fatalf("query alert: %v", err)
	}
	if serverID != 10 || websiteID != 20 {
		t.Fatalf("alert target = server %d website %d, want server 10 website 20", serverID, websiteID)
	}
	if resolved != 0 {
		t.Fatalf("resolved = %d, want 0", resolved)
	}

	execAlertSQL(t, db, `UPDATE websites SET expiry_date = date('now','+30 days') WHERE id = 20`)
	checkExpiryAlerts(db, "website_expiry", "websites", "expiry_date")

	if err := db.QueryRow(`SELECT resolved FROM alert_log WHERE alert_type = 'website_expiry'`).Scan(&resolved); err != nil {
		t.Fatalf("query resolved alert: %v", err)
	}
	if resolved != 1 {
		t.Fatalf("resolved after recovery = %d, want 1", resolved)
	}
}

func TestAlertRecipientsUseRuleEmailAndUserEmail(t *testing.T) {
	db := newAlertTestDB(t)
	execAlertSQL(t, db, `INSERT INTO customers (id, email) VALUES (1, 'user@example.com')`)
	execAlertSQL(t, db, `INSERT INTO servers (id, name, customer_id, status) VALUES (10, 'server-1', 1, 'active')`)
	execAlertSQL(t, db, `INSERT INTO alert_rules (alert_type, notify_user, notify_email, server_id, enabled) VALUES ('cpu_high', 1, 'ops@example.com', 10, 1)`)

	serverID := int64(10)
	got := alertRecipients(db, "cpu_high", &serverID, nil)
	want := []string{"ops@example.com", "user@example.com"}
	if len(got) != len(want) {
		t.Fatalf("recipients = %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("recipients = %#v, want %#v", got, want)
		}
	}
}

func newAlertTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dbName := strings.NewReplacer("/", "-", " ", "-", ":", "-").Replace(t.Name())
	db, err := sql.Open("sqlite", "file:"+dbName+"?mode=memory&cache=shared")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	stmts := []string{
		`CREATE TABLE customers (id INTEGER PRIMARY KEY, email TEXT NOT NULL DEFAULT '')`,
		`CREATE TABLE servers (
			id INTEGER PRIMARY KEY,
			name TEXT NOT NULL,
			customer_id INTEGER,
			status TEXT NOT NULL DEFAULT 'active',
			is_online INTEGER NOT NULL DEFAULT 0,
			last_seen_at DATETIME,
			http_probe_enabled INTEGER NOT NULL DEFAULT 0,
			http_probe_healthy INTEGER,
			http_probe_last_error TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE TABLE websites (
			id INTEGER PRIMARY KEY,
			name TEXT NOT NULL,
			server_id INTEGER NOT NULL,
			customer_id INTEGER,
			expiry_date TEXT,
			status TEXT NOT NULL DEFAULT 'active'
		)`,
		`CREATE TABLE metrics (
			server_id INTEGER NOT NULL,
			cpu_percent REAL,
			memory_percent REAL,
			disk_percent REAL,
			recorded_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE alert_rules (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			alert_type TEXT NOT NULL,
			name TEXT NOT NULL DEFAULT '',
			enabled INTEGER NOT NULL DEFAULT 1,
			threshold_value REAL NOT NULL DEFAULT 0,
			threshold_count INTEGER NOT NULL DEFAULT 3,
			notify_user INTEGER NOT NULL DEFAULT 0,
			notify_email TEXT NOT NULL DEFAULT '',
			server_id INTEGER
		)`,
		`CREATE TABLE alert_log (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			alert_type TEXT NOT NULL,
			server_id INTEGER,
			website_id INTEGER,
			level TEXT NOT NULL DEFAULT 'warning',
			message TEXT NOT NULL,
			resolved INTEGER NOT NULL DEFAULT 0,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,
	}
	for _, stmt := range stmts {
		execAlertSQL(t, db, stmt)
	}
	return db
}

func execAlertSQL(t *testing.T, db *sql.DB, stmt string, args ...interface{}) {
	t.Helper()
	if _, err := db.Exec(stmt, args...); err != nil {
		t.Fatalf("exec %q: %v", stmt, err)
	}
}
