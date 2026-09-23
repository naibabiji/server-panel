package executor

import (
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/naibabiji/server-panel/database"
	_ "modernc.org/sqlite"
)

func TestIsWhitelistedSupportsExactIPAndCIDR(t *testing.T) {
	db := newScanDefenseTestDB(t)
	execScanSQL(t, db, `INSERT INTO whitelist (ip_address, notes) VALUES ('127.0.0.1', 'local')`)
	execScanSQL(t, db, `INSERT INTO whitelist (ip_address, notes) VALUES ('10.0.0.0/8', 'private')`)

	if !IsWhitelisted("127.0.0.1") {
		t.Fatal("127.0.0.1 should be whitelisted")
	}
	if !IsWhitelisted("10.12.3.4") {
		t.Fatal("10.12.3.4 should match CIDR whitelist")
	}
	if IsWhitelisted("192.0.2.10") {
		t.Fatal("192.0.2.10 should not be whitelisted")
	}
}

func TestNFTPortSetUsesOnlyTLSPort(t *testing.T) {
	if got := nftPortSet(0); got != "" {
		t.Fatalf("nftPortSet(0) = %q, want empty string", got)
	}
	if got := nftPortSet(8444); got != "{ 8444 }" {
		t.Fatalf("nftPortSet(8444) = %q, want { 8444 }", got)
	}
}

func TestNFTSetForIPSupportsIPv4AndIPv6(t *testing.T) {
	tests := []struct {
		ip      string
		wantSet string
		wantOK  bool
	}{
		{ip: "203.0.113.10", wantSet: nftSet4, wantOK: true},
		{ip: "2001:db8::10", wantSet: nftSet6, wantOK: true},
		{ip: "not-an-ip", wantOK: false},
	}
	for _, tt := range tests {
		gotSet, gotOK := nftSetForIP(tt.ip)
		if gotSet != tt.wantSet || gotOK != tt.wantOK {
			t.Errorf("nftSetForIP(%q) = (%q, %v), want (%q, %v)", tt.ip, gotSet, gotOK, tt.wantSet, tt.wantOK)
		}
	}
}

func TestInitNFTablesStaysDisabledWhenRuleSetupFails(t *testing.T) {
	oldLookPath := lookPath
	oldRun := runNftCommand
	oldInitialized := nftInitialized
	lookPath = func(string) (string, error) { return "/usr/sbin/nft", nil }
	runNftCommand = func(...string) ([]byte, error) { return []byte("permission denied"), errors.New("exit status 1") }
	nftInitialized = true
	t.Cleanup(func() {
		lookPath = oldLookPath
		runNftCommand = oldRun
		nftInitialized = oldInitialized
	})

	InitNFTables(8444)

	if nftInitialized {
		t.Fatal("nftInitialized = true after rule setup failure, want false")
	}
}

func TestInitNFTablesRulesCreatesIPv4AndIPv6Drops(t *testing.T) {
	oldRun := runNftCommand
	var calls []string
	runNftCommand = func(args ...string) ([]byte, error) {
		call := strings.Join(args, " ")
		calls = append(calls, call)
		if call == "list table inet "+nftTable {
			return []byte("set " + nftSet4 + " { flags timeout; size 65536; }\nset " + nftSet6 + " { flags timeout; size 65536; }\nchain input { hook input; drop; }"), nil
		}
		return nil, nil
	}
	t.Cleanup(func() { runNftCommand = oldRun })

	if err := initNFTablesRules("{ 8444 }"); err != nil {
		t.Fatalf("initNFTablesRules() error = %v", err)
	}

	joined := strings.Join(calls, "\n")
	for _, want := range []string{
		"add set inet " + nftTable + " " + nftSet4 + " { type ipv4_addr; flags timeout; size 65536; }",
		"add set inet " + nftTable + " " + nftSet6 + " { type ipv6_addr; flags timeout; size 65536; }",
		"ip saddr @" + nftSet4 + " tcp dport { 8444 } drop",
		"ip6 saddr @" + nftSet6 + " tcp dport { 8444 } drop",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("nft calls missing %q:\n%s", want, joined)
		}
	}
}

func TestInitNFTablesRulesRejectsLegacyUnboundedSets(t *testing.T) {
	oldRun := runNftCommand
	runNftCommand = func(args ...string) ([]byte, error) {
		if strings.Join(args, " ") == "list table inet "+nftTable {
			return []byte("set " + nftSet4 + "\nset " + nftSet6 + "\nchain input { hook input; drop; }"), nil
		}
		return nil, nil
	}
	t.Cleanup(func() { runNftCommand = oldRun })

	if err := initNFTablesRules("{ 8444 }"); err == nil || !strings.Contains(err.Error(), "flags timeout") {
		t.Fatalf("initNFTablesRules() error = %v, want missing timeout validation", err)
	}
}

func TestBanIPRecordsDatabaseBanWhenNFTablesDisabled(t *testing.T) {
	db := newScanDefenseTestDB(t)
	oldInitialized := nftInitialized
	nftInitialized = false
	t.Cleanup(func() { nftInitialized = oldInitialized })

	BanIP("203.0.113.10", "too many login attempts", "panel", 24)

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM firewall_bans
		WHERE ip_address = '203.0.113.10' AND source = 'panel' AND unbanned_at IS NULL`).Scan(&count); err != nil {
		t.Fatalf("query firewall_bans: %v", err)
	}
	if count != 1 {
		t.Fatalf("active ban count = %d, want 1", count)
	}
}

func TestEnsureBanRecordTruncatesReasonAtUTF8Boundary(t *testing.T) {
	db := newScanDefenseTestDB(t)
	reason := strings.Repeat("a", 253) + "你好"

	if _, ok := ensureBanRecord("203.0.113.11", reason, "scan_defense", 24); !ok {
		t.Fatal("ensureBanRecord() failed")
	}

	var stored string
	if err := db.QueryRow("SELECT reason FROM firewall_bans WHERE ip_address = ?", "203.0.113.11").Scan(&stored); err != nil {
		t.Fatalf("query reason: %v", err)
	}
	if len(stored) > maxBanReasonBytes {
		t.Fatalf("stored reason length = %d, want <= %d", len(stored), maxBanReasonBytes)
	}
	if !utf8.ValidString(stored) {
		t.Fatalf("stored reason is not valid UTF-8: %q", stored)
	}
	if stored != strings.Repeat("a", 253)+"你" {
		t.Fatalf("stored reason = %q, want boundary-safe truncation", stored)
	}
}

func TestBanIPUsesTimeoutAndLogsDatabaseBanEvenWhenNFTSetIsFull(t *testing.T) {
	db := newScanDefenseTestDB(t)
	oldRun := runNftCommand
	oldInitialized := nftInitialized
	nftInitialized = true
	var call string
	runNftCommand = func(args ...string) ([]byte, error) {
		call = strings.Join(args, " ")
		return []byte("set is full"), errors.New("exit status 1")
	}
	t.Cleanup(func() {
		runNftCommand = oldRun
		nftInitialized = oldInitialized
	})

	BanIP("203.0.113.12", "scan", "scan_defense", 2)

	if !strings.Contains(call, "timeout ") || !strings.HasSuffix(call, "s }") {
		t.Fatalf("nft add call = %q, want per-element timeout", call)
	}
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM firewall_bans WHERE ip_address = ?", "203.0.113.12").Scan(&count); err != nil {
		t.Fatalf("query ban: %v", err)
	}
	if count != 1 {
		t.Fatalf("database ban count = %d, want 1", count)
	}
	if !nftInitialized {
		t.Fatal("nftInitialized changed after a full-set add failure")
	}
}

func TestBanIPNonPositiveDurationCreatesPermanentDatabaseAndNFTBan(t *testing.T) {
	db := newScanDefenseTestDB(t)
	oldRun := runNftCommand
	oldInitialized := nftInitialized
	nftInitialized = true
	var call string
	runNftCommand = func(args ...string) ([]byte, error) {
		call = strings.Join(args, " ")
		return nil, nil
	}
	t.Cleanup(func() {
		runNftCommand = oldRun
		nftInitialized = oldInitialized
	})

	BanIP("2001:db8::12", "manual", "panel", 0)

	var expiry sql.NullString
	if err := db.QueryRow("SELECT expires_at FROM firewall_bans WHERE ip_address = ?", "2001:db8::12").Scan(&expiry); err != nil {
		t.Fatalf("query expiry: %v", err)
	}
	if expiry.Valid {
		t.Fatalf("expires_at = %q, want NULL permanent ban", expiry.String)
	}
	if strings.Contains(call, "timeout") {
		t.Fatalf("permanent nft element unexpectedly has timeout: %q", call)
	}
}

func TestRestoreActiveBansUsesRemainingTimeoutAndPermanentSemantics(t *testing.T) {
	db := newScanDefenseTestDB(t)
	expiresAt := time.Now().UTC().Add(2 * time.Hour).Format("2006-01-02 15:04:05")
	execScanSQL(t, db, `INSERT INTO firewall_bans (ip_address, reason, source, expires_at) VALUES (?, '', 'panel', ?)`, "203.0.113.20", expiresAt)
	execScanSQL(t, db, `INSERT INTO firewall_bans (ip_address, reason, source, expires_at) VALUES (?, '', 'panel', NULL)`, "2001:db8::20")
	oldRun := runNftCommand
	oldInitialized := nftInitialized
	nftInitialized = true
	var calls []string
	runNftCommand = func(args ...string) ([]byte, error) {
		calls = append(calls, strings.Join(args, " "))
		return nil, nil
	}
	t.Cleanup(func() {
		runNftCommand = oldRun
		nftInitialized = oldInitialized
	})

	restoreActiveBans()

	joined := strings.Join(calls, "\n")
	if !strings.Contains(joined, "203.0.113.20 timeout ") {
		t.Fatalf("finite restored ban missing timeout:\n%s", joined)
	}
	for _, call := range calls {
		if strings.Contains(call, "2001:db8::20") && strings.Contains(call, "timeout") {
			t.Fatalf("permanent restored ban unexpectedly has timeout: %q", call)
		}
	}
}

func TestCleanExpiredBansMarksDatabaseExpiredWhenKernelElementAlreadyTimedOut(t *testing.T) {
	db := newScanDefenseTestDB(t)
	execScanSQL(t, db, `INSERT INTO firewall_bans (ip_address, reason, source, expires_at) VALUES ('203.0.113.30', '', 'panel', datetime('now', '-1 hour'))`)
	oldRun := runNftCommand
	oldInitialized := nftInitialized
	nftInitialized = true
	runNftCommand = func(...string) ([]byte, error) {
		return []byte("No such file or directory"), errors.New("exit status 1")
	}
	t.Cleanup(func() {
		runNftCommand = oldRun
		nftInitialized = oldInitialized
	})

	cleanExpiredBans()

	var unbanned sql.NullString
	if err := db.QueryRow("SELECT unbanned_at FROM firewall_bans WHERE ip_address = '203.0.113.30'").Scan(&unbanned); err != nil {
		t.Fatalf("query unbanned_at: %v", err)
	}
	if !unbanned.Valid {
		t.Fatal("expired database ban was not marked unbanned after kernel timeout")
	}
}

func newScanDefenseTestDB(t *testing.T) *sql.DB {
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

	execScanSQL(t, db, `CREATE TABLE whitelist (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		ip_address TEXT NOT NULL UNIQUE,
		notes TEXT NOT NULL DEFAULT '',
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	)`)
	execScanSQL(t, db, `CREATE TABLE firewall_bans (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		ip_address TEXT NOT NULL,
		reason TEXT NOT NULL,
		source TEXT NOT NULL,
		expires_at DATETIME,
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		unbanned_at DATETIME
	)`)
	return db
}

func execScanSQL(t *testing.T, db *sql.DB, stmt string, args ...interface{}) {
	t.Helper()
	if _, err := db.Exec(stmt, args...); err != nil {
		t.Fatalf("exec %q: %v", stmt, err)
	}
}
