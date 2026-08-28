package executor

import (
	"database/sql"
	"fmt"
	"log"
	"net"
	"os/exec"
	"strings"
	"time"

	"github.com/naibabiji/server-panel/database"
)

const (
	nftTable = "sp_filter"
	nftSet4  = "banned_ipv4"
	nftSet6  = "banned_ipv6"
)

var (
	nftPorts       = "8444"
	nftInitialized bool
	lookPath       = exec.LookPath
	runNftCommand  = func(args ...string) ([]byte, error) {
		return exec.Command("nft", args...).CombinedOutput()
	}
)

func SetNFTablesPorts(ports string) {
	nftPorts = ports
}

// InitNFTables creates the sp_filter table, set, chain, and rules if not exists.
func InitNFTables(tlsPort int) {
	nftInitialized = false
	ports := nftPortSet(tlsPort)
	if ports == "" {
		log.Printf("nftables scan defense disabled: no panel ports configured")
		return
	}
	SetNFTablesPorts(ports)
	if _, err := lookPath("nft"); err != nil {
		log.Printf("nftables not found, scan defense disabled")
		return
	}
	if err := initNFTablesRules(ports); err != nil {
		log.Printf("nftables scan defense disabled: %v", err)
		return
	}

	nftInitialized = true
	log.Printf("nftables scan defense initialized (ports %s)", ports)
	restoreActiveBans()
}

func initNFTablesRules(ports string) error {
	// This table belongs exclusively to Server Panel. Recreating it avoids
	// duplicate rules and removes the legacy IPv4-only banned_ips set during
	// upgrades; active database bans are restored immediately afterwards.
	_, _ = runNftCommand("delete", "table", "inet", nftTable)
	commands := [][]string{
		{"add", "table", "inet", nftTable},
		{"add", "set", "inet", nftTable, nftSet4, "{ type ipv4_addr; }"},
		{"add", "set", "inet", nftTable, nftSet6, "{ type ipv6_addr; }"},
		{"add", "chain", "inet", nftTable, "input", "{ type filter hook input priority 0; policy accept; }"},
		{"add", "rule", "inet", nftTable, "input", "ip", "saddr", "@" + nftSet4, "tcp", "dport", ports, "drop"},
		{"add", "rule", "inet", nftTable, "input", "ip6", "saddr", "@" + nftSet6, "tcp", "dport", ports, "drop"},
	}
	for _, args := range commands {
		if output, err := runNftCommand(args...); err != nil {
			return fmt.Errorf("nft %s failed: %s: %w", strings.Join(args, " "), strings.TrimSpace(string(output)), err)
		}
	}
	ruleset, err := runNftCommand("list", "table", "inet", nftTable)
	if err != nil {
		return fmt.Errorf("verify nftables table: %w", err)
	}
	for _, required := range []string{nftSet4, nftSet6, "hook input", "drop"} {
		if !strings.Contains(string(ruleset), required) {
			return fmt.Errorf("verify nftables table: missing %s", required)
		}
	}
	return nil
}

func nftPortSet(tlsPort int) string {
	if tlsPort <= 0 {
		return ""
	}
	return fmt.Sprintf("{ %d }", tlsPort)
}

// BanIP adds an IP to the nftables banned set and writes to firewall_bans.
func BanIP(ip, reason, source string, durationHours int) {
	if IsWhitelisted(ip) {
		return
	}
	// Never firewall off loopback: if the panel sits behind a same-host
	// reverse proxy and TrustedProxies isn't configured, every request looks
	// like it comes from 127.0.0.1/::1. Banning it would lock out everyone,
	// including the proxy itself, until the ban expires.
	if parsed := net.ParseIP(ip); parsed != nil && parsed.IsLoopback() {
		return
	}
	ensureBanRecord(ip, reason, source, durationHours)
	if !nftInitialized {
		return
	}
	setName, ok := nftSetForIP(ip)
	if !ok {
		return
	}
	if _, err := runNftCommand("add", "element", "inet", nftTable, setName,
		"{ "+ip+" }"); err != nil {
		log.Printf("failed to add %s to nftables banned set: %v", ip, err)
		return
	}
}

func ensureBanRecord(ip, reason, source string, durationHours int) {
	db := database.GetDB()
	if db != nil {
		var count int
		_ = db.QueryRow(`SELECT COUNT(*) FROM firewall_bans
			WHERE ip_address = ? AND unbanned_at IS NULL
			AND (expires_at IS NULL OR expires_at > datetime('now'))`, ip).Scan(&count)
		if count > 0 {
			return
		}
		db.Exec(`INSERT INTO firewall_bans (ip_address, reason, source, expires_at) VALUES (?, ?, ?, datetime('now', '+'||?||' hours'))`,
			ip, reason, source, durationHours)
	}
}

// UnbanIP removes an IP from nftables and marks it unbanned in DB.
func UnbanIP(ip string) {
	db := database.GetDB()
	if db == nil {
		return
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM firewall_bans
		WHERE ip_address = ? AND unbanned_at IS NULL
		AND (expires_at IS NULL OR expires_at > datetime('now'))`, ip).Scan(&count); err != nil || count == 0 {
		return
	}
	if nftInitialized && !deleteNftElement(ip) {
		log.Printf("failed to remove %s from nftables banned set", ip)
		return
	}
	db.Exec("UPDATE firewall_bans SET unbanned_at = CURRENT_TIMESTAMP WHERE ip_address = ? AND unbanned_at IS NULL", ip)
}

// UnbanAllIPs flushes all banned IPs from nftables and marks all active bans as unbanned.
func UnbanAllIPs() {
	if nftInitialized {
		_, _ = runNftCommand("flush", "set", "inet", nftTable, nftSet4)
		_, _ = runNftCommand("flush", "set", "inet", nftTable, nftSet6)
	}
	db := database.GetDB()
	if db != nil {
		db.Exec("UPDATE firewall_bans SET unbanned_at = CURRENT_TIMESTAMP WHERE unbanned_at IS NULL")
		db.Exec("DELETE FROM login_attempts")
	}
}

// StartBanCleanup runs a goroutine that periodically removes expired bans from nftables.
func StartBanCleanup(interval time.Duration) {
	go func() {
		for {
			time.Sleep(interval)
			cleanExpiredBans()
		}
	}()
}

func cleanExpiredBans() {
	db := database.GetDB()
	if db == nil {
		return
	}
	rows, err := db.Query("SELECT ip_address FROM firewall_bans WHERE expires_at <= datetime('now') AND unbanned_at IS NULL")
	if err != nil {
		return
	}
	defer rows.Close()

	var ips []string
	for rows.Next() {
		var ip string
		rows.Scan(&ip)
		ips = append(ips, ip)
	}
	rows.Close()

	if len(ips) == 0 {
		return
	}

	for _, ip := range ips {
		if nftInitialized && !deleteNftElement(ip) {
			log.Printf("failed to remove expired ban from nftables: ip=%s", ip)
			continue
		}
		db.Exec("UPDATE firewall_bans SET unbanned_at = CURRENT_TIMESTAMP WHERE ip_address = ? AND unbanned_at IS NULL", ip)
	}
}

func deleteNftElement(ip string) bool {
	setName, ok := nftSetForIP(ip)
	if !ok {
		return false
	}
	_, err := runNftCommand("delete", "element", "inet", nftTable, setName, "{ "+ip+" }")
	return err == nil
}

func nftSetForIP(ip string) (string, bool) {
	parsed := net.ParseIP(ip)
	if parsed == nil {
		return "", false
	}
	if parsed.To4() != nil {
		return nftSet4, true
	}
	return nftSet6, true
}

func restoreActiveBans() {
	db := database.GetDB()
	if db == nil || !nftInitialized {
		return
	}
	rows, err := db.Query(`SELECT ip_address FROM firewall_bans
		WHERE unbanned_at IS NULL AND (expires_at IS NULL OR expires_at > datetime('now'))`)
	if err != nil {
		log.Printf("failed to load active firewall bans: %v", err)
		return
	}
	defer rows.Close()

	restored := 0
	for rows.Next() {
		var ip string
		if err := rows.Scan(&ip); err != nil {
			continue
		}
		if IsWhitelisted(ip) {
			continue
		}
		setName, ok := nftSetForIP(ip)
		if !ok {
			continue
		}
		if _, err := runNftCommand("add", "element", "inet", nftTable, setName, "{ "+ip+" }"); err == nil {
			restored++
		}
	}
	if restored > 0 {
		log.Printf("restored %d active firewall bans to nftables", restored)
	}
}

// IsBrowserUserAgent checks if the User-Agent string belongs to a browser.
func IsBrowserUserAgent(ua string) bool {
	browsers := []string{"Mozilla", "Chrome", "Safari", "Firefox", "Edge", "Opera", "MSIE", "Trident"}
	for _, b := range browsers {
		if strings.Contains(ua, b) {
			return true
		}
	}
	return false
}

func IsWhitelisted(ip string) bool {
	db := database.GetDB()
	if db == nil || ip == "" {
		return false
	}

	parsed := net.ParseIP(ip)
	if parsed == nil {
		return false
	}

	rows, err := db.Query("SELECT ip_address FROM whitelist")
	if err != nil {
		return false
	}
	defer rows.Close()

	for rows.Next() {
		var entry string
		if err := rows.Scan(&entry); err != nil {
			continue
		}
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		if strings.Contains(entry, "/") {
			_, network, err := net.ParseCIDR(entry)
			if err == nil && network.Contains(parsed) {
				return true
			}
			continue
		}
		if parsed.Equal(net.ParseIP(entry)) {
			return true
		}
	}
	return false
}

// GetDB returns the database handle (helper for external callers).
func GetDB() *sql.DB {
	return database.GetDB()
}
