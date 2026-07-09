package executor

import "testing"

// TestBanThenUnbanClearsActiveBan verifies the full ban lifecycle: BanIP
// writes an active firewall_bans row, and UnbanIP both removes it from the
// active set and marks it unbanned so IsBanned-style checks no longer block.
func TestBanThenUnbanClearsActiveBan(t *testing.T) {
	db := newScanDefenseTestDB(t)
	oldInitialized := nftInitialized
	nftInitialized = false
	t.Cleanup(func() { nftInitialized = oldInitialized })

	const ip = "203.0.113.200"
	BanIP(ip, "too many login attempts", "panel", 24)

	var activeAfterBan int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM firewall_bans WHERE ip_address = ? AND unbanned_at IS NULL`, ip,
	).Scan(&activeAfterBan); err != nil {
		t.Fatalf("query after ban: %v", err)
	}
	if activeAfterBan != 1 {
		t.Fatalf("active bans after BanIP = %d, want 1", activeAfterBan)
	}

	UnbanIP(ip)

	var activeAfterUnban int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM firewall_bans WHERE ip_address = ? AND unbanned_at IS NULL`, ip,
	).Scan(&activeAfterUnban); err != nil {
		t.Fatalf("query after unban: %v", err)
	}
	if activeAfterUnban != 0 {
		t.Fatalf("active bans after UnbanIP = %d, want 0", activeAfterUnban)
	}
}

// TestBanIPSkipsWhitelisted verifies whitelisting an IP prevents it from ever
// being added to the banned set, even when BanIP is called directly.
func TestBanIPSkipsWhitelisted(t *testing.T) {
	db := newScanDefenseTestDB(t)
	oldInitialized := nftInitialized
	nftInitialized = false
	t.Cleanup(func() { nftInitialized = oldInitialized })

	execScanSQL(t, db, `INSERT INTO whitelist (ip_address, notes) VALUES ('198.51.100.7', 'wl')`)

	BanIP("198.51.100.7", "too many login attempts", "panel", 24)

	var active int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM firewall_bans WHERE ip_address = '198.51.100.7' AND unbanned_at IS NULL`,
	).Scan(&active); err != nil {
		t.Fatalf("query: %v", err)
	}
	if active != 0 {
		t.Fatalf("whitelisted IP should not be banned, active bans = %d", active)
	}
}
