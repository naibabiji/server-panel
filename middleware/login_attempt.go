package middleware

import (
	"database/sql"
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

	_, _ = t.DB.Exec(
		"INSERT INTO login_attempts (ip_address, attempt_type) VALUES (?, ?)",
		ip, attemptType,
	)

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
