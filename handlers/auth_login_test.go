package handlers

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/naibabiji/server-panel/database"
	"github.com/naibabiji/server-panel/middleware"
	"golang.org/x/crypto/bcrypt"
	_ "modernc.org/sqlite"
)

// TestLoginClearsAttemptsOnSuccess is an integration test for the
// "success does not reset counter" bug fix: after a successful authentication
// the failed-attempt rows for that IP must be wiped, so a user who previously
// failed several times is not banned on their very next mistake.
func TestLoginClearsAttemptsOnSuccess(t *testing.T) {
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

	execAuthSQL(t, db, `CREATE TABLE admin_users (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		username TEXT NOT NULL UNIQUE,
		password_hash TEXT NOT NULL,
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP)`)
	execAuthSQL(t, db, `CREATE TABLE login_attempts (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		ip_address TEXT NOT NULL,
		attempt_type TEXT NOT NULL,
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP)`)
	execAuthSQL(t, db, `CREATE TABLE firewall_bans (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		ip_address TEXT NOT NULL,
		reason TEXT NOT NULL,
		source TEXT NOT NULL,
		expires_at DATETIME,
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		unbanned_at DATETIME)`)

	hash, _ := bcrypt.GenerateFromPassword([]byte("secret"), bcrypt.DefaultCost)
	execAuthSQL(t, db, `INSERT INTO admin_users (username, password_hash) VALUES ('admin', ?)`, string(hash))

	tracker := middleware.NewLoginAttemptTracker(db, 3, 5, 24)
	const ip = "192.0.2.30"
	// 模拟登录前已失败 2 次（未达阈值）
	tracker.RecordAttempt(ip, "web_login")
	tracker.RecordAttempt(ip, "web_login")

	var before int
	if err := db.QueryRow(`SELECT COUNT(*) FROM login_attempts WHERE ip_address = ?`, ip).Scan(&before); err != nil {
		t.Fatalf("count before: %v", err)
	}
	if before != 2 {
		t.Fatalf("pre-existing attempts = %d, want 2", before)
	}

	h := &AuthHandler{AttemptTracker: tracker}
	r := gin.New()
	r.POST("/api/auth/login", h.Login)

	req := httptest.NewRequest(http.MethodPost, "/api/auth/login",
		strings.NewReader(`{"username":"admin","password":"secret"}`))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = ip + ":1234"
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("login: got %d, want 200; body=%s", w.Code, w.Body.String())
	}

	var after int
	if err := db.QueryRow(`SELECT COUNT(*) FROM login_attempts WHERE ip_address = ?`, ip).Scan(&after); err != nil {
		t.Fatalf("count after: %v", err)
	}
	if after != 0 {
		t.Fatalf("attempts after successful login = %d, want 0", after)
	}
	if tracker.IsBanned(ip) {
		t.Fatal("IP must not be banned after successful login cleared its attempts")
	}
}

func execAuthSQL(t *testing.T, db *sql.DB, stmt string, args ...interface{}) {
	t.Helper()
	if _, err := db.Exec(stmt, args...); err != nil {
		t.Fatalf("exec %q: %v", stmt, err)
	}
}
