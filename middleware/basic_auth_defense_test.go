package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/naibabiji/server-panel/config"
	"golang.org/x/crypto/bcrypt"
)

// TestBasicAuthDefenseBansAfterFailedAttempts exercises the real enforcement
// path: the BasicAuth middleware records failed authentications and, once the
// threshold is reached, subsequent requests from that IP are rejected with
// HTTP 403 instead of being allowed to keep guessing credentials.
func TestBasicAuthDefenseBansAfterFailedAttempts(t *testing.T) {
	db := newLoginTestDB(t)
	tracker := NewLoginAttemptTracker(db, 2, 5, 24)

	hash, err := bcrypt.GenerateFromPassword([]byte("correct-horse"), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("bcrypt: %v", err)
	}
	oldCfg := config.AppConfig
	config.AppConfig = &config.Config{
		BasicAuth: config.BasicAuthConfig{Username: "admin", PasswordHash: string(hash)},
		Security:  config.SecurityConfig{BasicAuthEnabled: true},
	}
	t.Cleanup(func() { config.AppConfig = oldCfg })

	r := gin.New()
	r.Use(BasicAuth(&BasicAuthChecker{
		RecordAttempt: tracker.RecordAttempt,
		IsBanned:      tracker.IsBanned,
	}))
	r.GET("/ping", func(c *gin.Context) { c.String(http.StatusOK, "ok") })

	// Missing credentials -> 401 but NOT counted as an attempt (by design).
	noCreds := httptest.NewRequest(http.MethodGet, "/ping", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, noCreds)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("missing creds: got %d, want 401", w.Code)
	}
	if tracker.IsBanned("192.0.2.5") {
		t.Fatal("missing creds must not trigger a ban")
	}

	// Wrong credentials DO count. After MaxLoginAttempts (2) bad guesses the
	// IP is banned and the next request returns 403.
	const ip = "192.0.2.5"
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodGet, "/ping", nil)
		req.RemoteAddr = ip + ":12345"
		req.SetBasicAuth("admin", "wrong")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("wrong creds attempt %d: got %d, want 401", i, w.Code)
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/ping", nil)
	req.RemoteAddr = ip + ":12345"
	req.SetBasicAuth("admin", "wrong")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("after threshold: got %d, want 403", w.Code)
	}

	// A correct password still fails while banned — defense takes precedence.
	good := httptest.NewRequest(http.MethodGet, "/ping", nil)
	good.RemoteAddr = ip + ":12345"
	good.SetBasicAuth("admin", "correct-horse")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, good)
	if w.Code != http.StatusForbidden {
		t.Fatalf("banned IP with correct creds: got %d, want 403", w.Code)
	}
}

// TestBasicAuthAllowsValidCredentials verifies legitimate traffic is not
// disrupted by the defense when credentials are correct.
func TestBasicAuthAllowsValidCredentials(t *testing.T) {
	db := newLoginTestDB(t)
	tracker := NewLoginAttemptTracker(db, 2, 5, 24)

	hash, _ := bcrypt.GenerateFromPassword([]byte("correct-horse"), bcrypt.DefaultCost)
	oldCfg := config.AppConfig
	config.AppConfig = &config.Config{
		BasicAuth: config.BasicAuthConfig{Username: "admin", PasswordHash: string(hash)},
		Security:  config.SecurityConfig{BasicAuthEnabled: true},
	}
	t.Cleanup(func() { config.AppConfig = oldCfg })

	r := gin.New()
	r.Use(BasicAuth(&BasicAuthChecker{
		RecordAttempt: tracker.RecordAttempt,
		IsBanned:      tracker.IsBanned,
	}))
	r.GET("/ping", func(c *gin.Context) { c.String(http.StatusOK, "ok") })

	req := httptest.NewRequest(http.MethodGet, "/ping", nil)
	req.RemoteAddr = "192.0.2.9:12345"
	req.SetBasicAuth("admin", "correct-horse")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("valid creds: got %d, want 200", w.Code)
	}
}

// TestBasicAuthDisabledPassesThrough verifies that when basic_auth_enabled is
// false the middleware no longer requires credentials, while the IP ban is
// still enforced (defense in depth).
func TestBasicAuthDisabledPassesThrough(t *testing.T) {
	db := newLoginTestDB(t)
	tracker := NewLoginAttemptTracker(db, 2, 5, 24)

	oldCfg := config.AppConfig
	config.AppConfig = &config.Config{
		BasicAuth: config.BasicAuthConfig{Username: "admin", PasswordHash: "irrelevant"},
		Security:  config.SecurityConfig{BasicAuthEnabled: false},
	}
	t.Cleanup(func() { config.AppConfig = oldCfg })

	r := gin.New()
	r.Use(BasicAuth(&BasicAuthChecker{
		RecordAttempt: tracker.RecordAttempt,
		IsBanned:      tracker.IsBanned,
	}))
	r.GET("/ping", func(c *gin.Context) { c.String(http.StatusOK, "ok") })

	// No credentials -> still allowed (Basic Auth disabled).
	req := httptest.NewRequest(http.MethodGet, "/ping", nil)
	req.RemoteAddr = "192.0.2.20:12345"
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("disabled basic auth: got %d, want 200", w.Code)
	}

	// A banned IP is still blocked with 403 even with Basic Auth off.
	const bannedIP = "203.0.113.250"
	tracker.RecordAttempt(bannedIP, "web")
	tracker.RecordAttempt(bannedIP, "web")
	tracker.RecordAttempt(bannedIP, "web")
	bannedReq := httptest.NewRequest(http.MethodGet, "/ping", nil)
	bannedReq.RemoteAddr = bannedIP + ":12345"
	bannedReq.SetBasicAuth("admin", "whatever")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, bannedReq)
	if w.Code != http.StatusForbidden {
		t.Fatalf("banned IP with basic auth disabled: got %d, want 403", w.Code)
	}
}
