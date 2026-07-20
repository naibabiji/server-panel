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

// Regression test: an item that goes overdue without ever being renewed must
// keep its expiry alert open, not have it silently auto-resolved just
// because it fell out of the "next N days" window.
func TestServerExpiryAlertStaysOpenWhenOverdueAndUnrenewed(t *testing.T) {
	db := newAlertTestDB(t)
	execAlertSQL(t, db, `INSERT INTO customers (id, email) VALUES (1, 'user@example.com')`)
	execAlertSQL(t, db, `INSERT INTO servers (id, name, customer_id, status, expiry_date) VALUES (10, 'server-1', 1, 'active', date('now','-3 days'))`)
	execAlertSQL(t, db, `INSERT INTO alert_rules (alert_type, threshold_value, enabled) VALUES ('server_expiry', 7, 1)`)

	checkExpiryAlerts(db, "server_expiry", "servers", "expiry_date")

	var resolved int
	if err := db.QueryRow(`SELECT resolved FROM alert_log WHERE alert_type = 'server_expiry' AND server_id = 10`).Scan(&resolved); err != nil {
		t.Fatalf("query alert: %v", err)
	}
	if resolved != 0 {
		t.Fatalf("resolved = %d, want 0 (overdue server must not be silently resolved)", resolved)
	}

	// Re-running the check (as the ticker does every interval) must not
	// flip it to resolved just because the item is still overdue.
	checkExpiryAlerts(db, "server_expiry", "servers", "expiry_date")
	if err := db.QueryRow(`SELECT resolved FROM alert_log WHERE alert_type = 'server_expiry' AND server_id = 10`).Scan(&resolved); err != nil {
		t.Fatalf("query alert after re-run: %v", err)
	}
	if resolved != 0 {
		t.Fatalf("resolved after re-run = %d, want 0", resolved)
	}
}

// Regression test: the same unresolved alert row must track a website's
// actual state as it moves from "about to expire" to "overdue" - not
// freeze at whatever wording/level applied when it first triggered, and
// not create a second row for the same target.
func TestServerExpiryAlertMessageEscalatesWhenItGoesOverdue(t *testing.T) {
	db := newAlertTestDB(t)
	execAlertSQL(t, db, `INSERT INTO customers (id, email) VALUES (1, 'user@example.com')`)
	execAlertSQL(t, db, `INSERT INTO servers (id, name, customer_id, status, expiry_date) VALUES (10, 'server-1', 1, 'active', date('now','+3 days'))`)
	execAlertSQL(t, db, `INSERT INTO alert_rules (alert_type, threshold_value, enabled) VALUES ('server_expiry', 7, 1)`)

	checkExpiryAlerts(db, "server_expiry", "servers", "expiry_date")

	var message, level string
	var resolved int
	if err := db.QueryRow(`SELECT message, level, resolved FROM alert_log WHERE alert_type = 'server_expiry' AND server_id = 10`).Scan(&message, &level, &resolved); err != nil {
		t.Fatalf("query alert: %v", err)
	}
	if !strings.Contains(message, "3 天后到期") {
		t.Fatalf("initial message = %q, want it to mention 3 天后到期", message)
	}
	if level != "warning" {
		t.Fatalf("initial level = %q, want warning", level)
	}

	execAlertSQL(t, db, `UPDATE servers SET expiry_date = date('now','-2 days') WHERE id = 10`)
	checkExpiryAlerts(db, "server_expiry", "servers", "expiry_date")

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM alert_log WHERE alert_type = 'server_expiry' AND server_id = 10`).Scan(&count); err != nil {
		t.Fatalf("count alerts: %v", err)
	}
	if count != 1 {
		t.Fatalf("alert_log rows for this target = %d, want 1 (must update in place, not duplicate)", count)
	}

	if err := db.QueryRow(`SELECT message, level, resolved FROM alert_log WHERE alert_type = 'server_expiry' AND server_id = 10`).Scan(&message, &level, &resolved); err != nil {
		t.Fatalf("query alert after going overdue: %v", err)
	}
	if !strings.Contains(message, "已过期 2 天") {
		t.Fatalf("message after going overdue = %q, want it to mention 已过期 2 天", message)
	}
	if level != "critical" {
		t.Fatalf("level after going overdue = %q, want critical", level)
	}
	if resolved != 0 {
		t.Fatalf("resolved = %d, want 0", resolved)
	}
}

// Regression test: a transient failure reading rules/items must abort the
// whole check, not resolve alerts for targets it failed to re-confirm as
// still expiring. Simulated here by dropping the servers table out from
// under an in-flight check.
func TestCheckExpiryAlertsDoesNotResolveOnQueryFailure(t *testing.T) {
	db := newAlertTestDB(t)
	execAlertSQL(t, db, `INSERT INTO customers (id, email) VALUES (1, 'user@example.com')`)
	execAlertSQL(t, db, `INSERT INTO servers (id, name, customer_id, status, expiry_date) VALUES (10, 'server-1', 1, 'active', date('now','-2 days'))`)
	execAlertSQL(t, db, `INSERT INTO alert_rules (alert_type, threshold_value, enabled) VALUES ('server_expiry', 7, 1)`)

	checkExpiryAlerts(db, "server_expiry", "servers", "expiry_date")
	var resolved int
	if err := db.QueryRow(`SELECT resolved FROM alert_log WHERE alert_type = 'server_expiry' AND server_id = 10`).Scan(&resolved); err != nil {
		t.Fatalf("query alert before failure: %v", err)
	}
	if resolved != 0 {
		t.Fatalf("resolved before failure = %d, want 0", resolved)
	}

	execAlertSQL(t, db, `DROP TABLE servers`)
	checkExpiryAlerts(db, "server_expiry", "servers", "expiry_date")

	if err := db.QueryRow(`SELECT resolved FROM alert_log WHERE alert_type = 'server_expiry' AND server_id = 10`).Scan(&resolved); err != nil {
		t.Fatalf("query alert after failure: %v", err)
	}
	if resolved != 0 {
		t.Fatalf("resolved after a failed read = %d, want 0 (a read failure must never resolve live alerts)", resolved)
	}
}

// Regression test: with zero enabled server_expiry rules, an overdue
// server must not get an alert at all - enabled=0/no-rule means "this
// alert type is off", not "fall back to a default 7-day window".
func TestCheckExpiryAlertsCreatesNothingWithoutAnEnabledRule(t *testing.T) {
	db := newAlertTestDB(t)
	execAlertSQL(t, db, `INSERT INTO customers (id, email) VALUES (1, 'user@example.com')`)
	execAlertSQL(t, db, `INSERT INTO servers (id, name, customer_id, status, expiry_date) VALUES (10, 'server-1', 1, 'active', date('now','-3 days'))`)
	// No alert_rules row at all for server_expiry.

	checkExpiryAlerts(db, "server_expiry", "servers", "expiry_date")

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM alert_log WHERE alert_type = 'server_expiry'`).Scan(&count); err != nil {
		t.Fatalf("count alerts: %v", err)
	}
	if count != 0 {
		t.Fatalf("alert_log rows = %d, want 0 (no enabled rule means no alerting)", count)
	}
}

// Regression test: an already-open expiry alert must be resolved once its
// rule is disabled, not left open forever just because the overdue server
// itself never changed.
func TestCheckExpiryAlertsResolvesExistingAlertWhenRuleIsDisabled(t *testing.T) {
	db := newAlertTestDB(t)
	execAlertSQL(t, db, `INSERT INTO customers (id, email) VALUES (1, 'user@example.com')`)
	execAlertSQL(t, db, `INSERT INTO servers (id, name, customer_id, status, expiry_date) VALUES (10, 'server-1', 1, 'active', date('now','-3 days'))`)
	execAlertSQL(t, db, `INSERT INTO alert_rules (alert_type, threshold_value, enabled) VALUES ('server_expiry', 7, 1)`)

	checkExpiryAlerts(db, "server_expiry", "servers", "expiry_date")
	var resolved int
	if err := db.QueryRow(`SELECT resolved FROM alert_log WHERE alert_type = 'server_expiry' AND server_id = 10`).Scan(&resolved); err != nil {
		t.Fatalf("query alert before disabling rule: %v", err)
	}
	if resolved != 0 {
		t.Fatalf("resolved before disabling rule = %d, want 0", resolved)
	}

	execAlertSQL(t, db, `UPDATE alert_rules SET enabled = 0 WHERE alert_type = 'server_expiry'`)
	checkExpiryAlerts(db, "server_expiry", "servers", "expiry_date")

	if err := db.QueryRow(`SELECT resolved FROM alert_log WHERE alert_type = 'server_expiry' AND server_id = 10`).Scan(&resolved); err != nil {
		t.Fatalf("query alert after disabling rule: %v", err)
	}
	if resolved != 1 {
		t.Fatalf("resolved after disabling rule = %d, want 1", resolved)
	}
}

// Regression test: a server whose Agent heartbeat went silent but that is
// still TCP-reachable must get the lower-severity "agent_offline" alert,
// not "server_offline" - the two situations have very different causes
// (probe/DNS/network on the box vs. the box itself being down) and mixing
// them up sends whoever's paged chasing an outage that isn't happening.
// Only a *fresh, post-heartbeat-loss* confirmed-unreachable TCP result may
// produce the critical "server_offline" alert; anything unconfirmed (never
// checked, or a check too old / predating this outage) must not.
func TestCheckOfflineAlertsSplitsByTCPReachability(t *testing.T) {
	db := newAlertTestDB(t)
	execAlertSQL(t, db, `INSERT INTO alert_rules (alert_type, threshold_value, enabled) VALUES ('server_offline', 5, 1)`)
	execAlertSQL(t, db, `INSERT INTO servers (id, name, status, is_online, last_seen_at, tcp_reachable, tcp_reachable_checked_at)
		VALUES (10, 'reachable-agent-down', 'active', 0, datetime('now', '-10 minutes'), 1, datetime('now', '-1 minutes'))`)
	execAlertSQL(t, db, `INSERT INTO servers (id, name, status, is_online, last_seen_at, tcp_reachable, tcp_reachable_checked_at)
		VALUES (20, 'truly-offline', 'active', 0, datetime('now', '-10 minutes'), 0, datetime('now', '-1 minutes'))`)
	execAlertSQL(t, db, `INSERT INTO servers (id, name, status, is_online, last_seen_at, tcp_reachable, tcp_reachable_checked_at)
		VALUES (30, 'not-yet-checked', 'active', 0, datetime('now', '-10 minutes'), NULL, NULL)`)
	// tcp_reachable_checked_at predates last_seen_at: this "unreachable"
	// result is left over from a previous outage, before the agent came
	// back online and went silent again - it says nothing about the
	// current outage and must not be trusted.
	execAlertSQL(t, db, `INSERT INTO servers (id, name, status, is_online, last_seen_at, tcp_reachable, tcp_reachable_checked_at)
		VALUES (40, 'stale-check-predates-outage', 'active', 0, datetime('now', '-10 minutes'), 0, datetime('now', '-20 minutes'))`)
	// tcp_reachable_checked_at is after last_seen_at (so it's about this
	// outage) but older than reachabilityFreshnessWindow - too old to trust
	// as a description of the server's reachability right now.
	execAlertSQL(t, db, `INSERT INTO servers (id, name, status, is_online, last_seen_at, tcp_reachable, tcp_reachable_checked_at)
		VALUES (50, 'stale-check-too-old', 'active', 0, datetime('now', '-30 minutes'), 0, datetime('now', '-25 minutes'))`)

	checkOfflineAlerts(db)

	assertAlertType := func(serverID int64, wantType string, wantResolved int) {
		t.Helper()
		var alertType string
		var resolved int
		if err := db.QueryRow(`SELECT alert_type, resolved FROM alert_log WHERE server_id = ?`, serverID).Scan(&alertType, &resolved); err != nil {
			t.Fatalf("query alert for server %d: %v", serverID, err)
		}
		if alertType != wantType {
			t.Fatalf("server %d alert_type = %q, want %q", serverID, alertType, wantType)
		}
		if resolved != wantResolved {
			t.Fatalf("server %d resolved = %d, want %d", serverID, resolved, wantResolved)
		}
	}

	assertAlertType(10, "agent_offline", 0)
	assertAlertType(20, "server_offline", 0)
	assertAlertType(30, "agent_offline", 0)
	assertAlertType(40, "agent_offline", 0)
	assertAlertType(50, "agent_offline", 0)

	// Once the agent starts reporting again and the TCP check confirms it,
	// the "agent_offline" alert for server 10 must resolve rather than
	// linger open.
	execAlertSQL(t, db, `UPDATE servers SET is_online = 1, tcp_reachable = 1 WHERE id = 10`)
	checkOfflineAlerts(db)
	assertAlertType(10, "agent_offline", 1)
}

// Regression test: an alert_log row of alert_type "agent_offline" gets no
// alert_rules row of its own (users only ever configure "server_offline" -
// see checkOfflineAlerts), so its notification routing must be looked up
// under "server_offline" instead of silently falling back to the admin
// default because nothing matches "agent_offline".
func TestAgentOfflineNotificationUsesServerOfflineRule(t *testing.T) {
	db := newAlertTestDB(t)
	execAlertSQL(t, db, `INSERT INTO customers (id, email) VALUES (1, 'user@example.com')`)
	execAlertSQL(t, db, `INSERT INTO servers (id, name, customer_id, status) VALUES (10, 'server-1', 1, 'active')`)
	execAlertSQL(t, db, `INSERT INTO alert_rules (alert_type, notify_user, notify_email, server_id, enabled) VALUES ('server_offline', 1, 'ops@example.com', 10, 1)`)

	serverID := int64(10)
	if got := alertRecipients(db, "agent_offline", &serverID, nil); len(got) != 1 || got[0] != "" {
		t.Fatalf("lookup under agent_offline = %#v, want just the admin-default empty recipient (no rule of that type can ever exist)", got)
	}

	got := alertRecipients(db, "server_offline", &serverID, nil)
	want := []string{"ops@example.com", "user@example.com"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("lookup under server_offline = %#v, want %#v", got, want)
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
			expiry_date TEXT NOT NULL DEFAULT '',
			is_online INTEGER NOT NULL DEFAULT 0,
			last_seen_at DATETIME,
			http_probe_enabled INTEGER NOT NULL DEFAULT 0,
			http_probe_healthy INTEGER,
			http_probe_last_error TEXT NOT NULL DEFAULT '',
			tcp_reachable INTEGER,
			tcp_reachable_checked_at DATETIME
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
