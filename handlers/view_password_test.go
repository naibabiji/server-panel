package handlers

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/naibabiji/server-panel/database"
	"golang.org/x/crypto/bcrypt"
	_ "modernc.org/sqlite"
)

func TestUnlockWrongViewPasswordIsBusinessError(t *testing.T) {
	gin.SetMode(gin.TestMode)

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

	if _, err := db.Exec(`CREATE TABLE settings (skey TEXT PRIMARY KEY, svalue TEXT NOT NULL)`); err != nil {
		t.Fatalf("create settings table: %v", err)
	}
	hash, err := bcrypt.GenerateFromPassword([]byte("correct-password"), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO settings (skey, svalue) VALUES ('view_password_hash', ?)`, string(hash)); err != nil {
		t.Fatalf("insert view password: %v", err)
	}

	const clientIP = "192.0.2.55"
	unlockAttemptsMu.Lock()
	delete(unlockAttempts, clientIP)
	unlockAttemptsMu.Unlock()
	t.Cleanup(func() {
		unlockAttemptsMu.Lock()
		delete(unlockAttempts, clientIP)
		unlockAttemptsMu.Unlock()
	})

	router := gin.New()
	router.POST("/api/view-password/unlock", (&ViewPasswordHandler{}).Unlock)
	req := httptest.NewRequest(http.MethodPost, "/api/view-password/unlock", strings.NewReader(`{"password":"wrong-password"}`))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = clientIP + ":1234"
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d; body=%s", w.Code, http.StatusForbidden, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "还剩 4 次尝试") {
		t.Errorf("body = %q, want remaining-attempts error", w.Body.String())
	}
	if strings.Contains(w.Body.String(), "SESSION_") {
		t.Errorf("body = %q, business error must not contain a session error code", w.Body.String())
	}
}

func TestForcedResetHelpersClearProviderPrivateNotes(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, stmt := range []string{
		`CREATE TABLE servers (ssh_password_enc TEXT NOT NULL, panel_password_enc TEXT NOT NULL)`,
		`CREATE TABLE websites (panel_password_enc TEXT NOT NULL)`,
		`CREATE TABLE providers (private_notes_enc TEXT NOT NULL)`,
		`INSERT INTO servers VALUES ('ssh-secret', 'panel-secret')`,
		`INSERT INTO websites VALUES ('website-secret')`,
		`INSERT INTO providers VALUES ('provider-secret')`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("setup %q: %v", stmt, err)
		}
	}

	cleared, err := clearSavedSecrets(db)
	if err != nil {
		t.Fatalf("clearSavedSecrets: %v", err)
	}
	if cleared != 2 {
		t.Fatalf("cleared = %d, want 2", cleared)
	}
	var providerBeforeForce string
	if err := db.QueryRow(`SELECT private_notes_enc FROM providers`).Scan(&providerBeforeForce); err != nil {
		t.Fatal(err)
	}
	if providerBeforeForce != "provider-secret" {
		t.Fatalf("failed-attempt cleanup unexpectedly cleared provider note: %q", providerBeforeForce)
	}
	if err := clearProviderPrivateNotes(db); err != nil {
		t.Fatalf("clearProviderPrivateNotes: %v", err)
	}
	for _, query := range []string{
		`SELECT ssh_password_enc || panel_password_enc FROM servers`,
		`SELECT panel_password_enc FROM websites`,
		`SELECT private_notes_enc FROM providers`,
	} {
		var value string
		if err := db.QueryRow(query).Scan(&value); err != nil {
			t.Fatal(err)
		}
		if value != "" {
			t.Fatalf("query %q retained secret %q", query, value)
		}
	}
}
