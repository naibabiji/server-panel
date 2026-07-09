package middleware

import (
	"database/sql"
	"log"
	"sync"
	"time"

	"github.com/naibabiji/server-panel/executor"
	"github.com/naibabiji/server-panel/timeutil"
)

type LoginAttemptTracker struct {
	DB               *sql.DB
	MaxAttempts      int
	AttemptWindow    time.Duration
	BanDurationHours int
	mu               sync.Mutex
}

func NewLoginAttemptTracker(db *sql.DB, maxAttempts int, windowMinutes int, banHours int) *LoginAttemptTracker {
	return &LoginAttemptTracker{
		DB:               db,
		MaxAttempts:      maxAttempts,
		AttemptWindow:    time.Duration(windowMinutes) * time.Minute,
		BanDurationHours: banHours,
	}
}

func (t *LoginAttemptTracker) RecordAttempt(ip string, attemptType string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if _, err := t.DB.Exec(
		"INSERT INTO login_attempts (ip_address, attempt_type) VALUES (?, ?)",
		ip, attemptType,
	); err != nil {
		log.Printf("login attempt record failed for %s: %v", ip, err)
	}

	count := t.countRecent(ip)
	if count >= t.MaxAttempts {
		t.banIP(ip, attemptType)
	}
}

func (t *LoginAttemptTracker) IsBanned(ip string) bool {
	var count int
	err := t.DB.QueryRow(
		`SELECT COUNT(*) FROM firewall_bans
		 WHERE ip_address = ?
		 AND unbanned_at IS NULL
		 AND (expires_at IS NULL OR expires_at > datetime('now'))`,
		ip,
	).Scan(&count)
	if err != nil {
		// 封禁查询失败时采用 fail-open（返回 false），避免数据库瞬时故障导致全员 403；
		// 但记录日志以便排查，封禁仅为纵深防御，主认证不依赖此查询。
		log.Printf("is-banned check failed for %s: %v", ip, err)
		return false
	}
	return count > 0
}

func (t *LoginAttemptTracker) countRecent(ip string) int {
	var count int
	cutoff := timeutil.Display(time.Now().Add(-t.AttemptWindow))
	err := t.DB.QueryRow(
		`SELECT COUNT(*) FROM login_attempts
		 WHERE ip_address = ? AND created_at > ?`,
		ip, cutoff,
	).Scan(&count)
	if err != nil {
		log.Printf("count recent login attempts failed for %s: %v", ip, err)
		return 0
	}
	return count
}

func (t *LoginAttemptTracker) banIP(ip string, attemptType string) {
	reason := "panel_" + attemptType + ": 连续多次认证失败"
	executor.BanIP(ip, reason, "panel", t.BanDurationHours)
}

func (t *LoginAttemptTracker) CleanupOldAttempts() {
	cutoff := timeutil.Display(time.Now().Add(-t.AttemptWindow))
	_, _ = t.DB.Exec("DELETE FROM login_attempts WHERE created_at < ?", cutoff)
}

// ClearAttempts removes all recorded login attempts for an IP, regardless of
// attempt_type (web_login / basic_auth). It is called after any successful
// authentication for that IP so a streak of prior failures does not linger and
// trip the ban threshold on the user's very next mistake. Because the ban is
// keyed by IP rather than by auth method, clearing every type for the IP is
// intentional: a successful login via one method resets the shared streak.
func (t *LoginAttemptTracker) ClearAttempts(ip string) {
	if _, err := t.DB.Exec("DELETE FROM login_attempts WHERE ip_address = ?", ip); err != nil {
		log.Printf("clear login attempts failed for %s: %v", ip, err)
	}
}
