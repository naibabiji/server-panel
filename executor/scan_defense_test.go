package executor

import (
	"database/sql"
	"errors"
	"strings"
	"testing"

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
			return []byte("set " + nftSet4 + "\nset " + nftSet6 + "\nchain input { hook input; drop; }"), nil
		}
		return nil, nil
	}
	t.Cleanup(func() { runNftCommand = oldRun })

	if err := initNFTablesRules("{ 8444 }"); err != nil {
		t.Fatalf("initNFTablesRules() error = %v", err)
	}

	joined := strings.Join(calls, "\n")
	for _, want := range []string{
		"ip saddr @" + nftSet4 + " tcp dport { 8444 } drop",
		"ip6 saddr @" + nftSet6 + " tcp dport { 8444 } drop",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("nft calls missing %q:\n%s", want, joined)
		}
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
