package router

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/naibabiji/server-panel/config"
	"github.com/naibabiji/server-panel/database"
	"github.com/naibabiji/server-panel/middleware"
)

// TestAgentInstallInfoRouteRequiresSessionAndCSRF exercises the real
// SetupRouter middleware chain. install-info makes the panel fetch from an
// admin-supplied host, so it must be a CSRF-checked POST (the CSRF
// middleware skips GET) behind the session check.
func TestAgentInstallInfoRouteRequiresSessionAndCSRF(t *testing.T) {
	gin.SetMode(gin.TestMode)
	if err := database.Open(filepath.Join(t.TempDir(), "panel.db")); err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if err := database.RunMigrations(); err != nil {
		t.Fatalf("migrations: %v", err)
	}
	if err := database.RunUpgrades(); err != nil {
		t.Fatalf("upgrades: %v", err)
	}
	db := database.GetDB()
	if _, err := db.Exec(`INSERT OR REPLACE INTO settings (skey, svalue) VALUES ('view_password_hash', 'set')`); err != nil {
		t.Fatalf("seed view password: %v", err)
	}

	cfg := &config.Config{}
	cfg.Panel.RandomSuffix = "routetest"
	cfg.Panel.Version = "dev"
	cfg.Security = config.SecurityConfig{MaxLoginAttempts: 5, AttemptWindowMinutes: 5, BanDurationHours: 1}
	repo := os.DirFS("..")
	r := SetupRouter(cfg, db, repo, repo)

	const path = "/routetest/api/agent/install-info"
	for _, route := range r.Routes() {
		if route.Path == path && route.Method != http.MethodPost {
			t.Fatalf("install-info registered for %s; it must be POST-only", route.Method)
		}
	}

	session := middleware.GlobalSessionStore.Create("admin")
	t.Cleanup(func() { middleware.GlobalSessionStore.Delete(session.Token) })
	// A loopback proxy is rejected by validation, so a request that gets
	// past session + CSRF returns 400 without any network access.
	body := []byte(`{"github_proxy":"http://127.0.0.1:1"}`)
	post := func(withSession, withCSRF bool) int {
		req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if withSession {
			req.AddCookie(&http.Cookie{Name: "sp_session", Value: session.Token})
		}
		if withCSRF {
			req.AddCookie(&http.Cookie{Name: "csrf_token", Value: "tok"})
			req.Header.Set("X-CSRF-Token", "tok")
		}
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w.Code
	}

	if code := post(false, true); code != http.StatusUnauthorized {
		t.Errorf("no session: status = %d, want 401", code)
	}
	if code := post(true, false); code != http.StatusForbidden {
		t.Errorf("no CSRF token: status = %d, want 403", code)
	}
	if code := post(true, true); code != http.StatusBadRequest {
		t.Errorf("session + CSRF with loopback proxy: status = %d, want 400", code)
	}
}
