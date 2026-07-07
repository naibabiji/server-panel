package handlers

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"
	_ "modernc.org/sqlite"
)

func TestStatusPagePasswordGate(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := newStatusPageTestDB(t)
	hash, err := bcrypt.GenerateFromPassword([]byte("secret-pass"), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	_, err = db.Exec(`INSERT INTO servers (
		id, name, is_online, http_probe_healthy, http_probe_last_at,
		status_page_enabled, status_page_token, status_page_password
	) VALUES (1, 'prod-1', 1, 1, '2026-05-28 12:00:00', 1, 'public-token', ?)`, string(hash))
	if err != nil {
		t.Fatalf("insert server: %v", err)
	}

	h := &StatusPageHandler{DB: db}

	w := performStatusRequest(h.GetInfo, http.MethodGet, "/api/status/:token/info", "/api/status/public-token/info", nil, "")
	if w.Code != http.StatusForbidden {
		t.Fatalf("GetInfo without verification status = %d, want %d", w.Code, http.StatusForbidden)
	}

	w = performStatusRequest(h.VerifyPassword, http.MethodPost, "/api/status/:token/verify", "/api/status/public-token/verify", strings.NewReader(`{"password":"bad"}`), "")
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("VerifyPassword with bad password status = %d, want %d", w.Code, http.StatusUnauthorized)
	}

	w = performStatusRequest(h.VerifyPassword, http.MethodPost, "/api/status/:token/verify", "/api/status/public-token/verify", strings.NewReader(`{"password":"secret-pass"}`), "")
	if w.Code != http.StatusOK {
		t.Fatalf("VerifyPassword with good password status = %d, want %d", w.Code, http.StatusOK)
	}
	cookies := w.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("VerifyPassword did not set an auth cookie")
	}

	w = performStatusRequest(h.GetInfo, http.MethodGet, "/api/status/:token/info", "/api/status/public-token/info", nil, cookies[0].String())
	if w.Code != http.StatusOK {
		t.Fatalf("GetInfo with verification cookie status = %d, want %d", w.Code, http.StatusOK)
	}
}

func TestStatusPageWithoutPasswordAllowsAccess(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := newStatusPageTestDB(t)
	_, err := db.Exec(`INSERT INTO servers (
		id, name, is_online, http_probe_healthy, http_probe_last_at,
		status_page_enabled, status_page_token, status_page_password
	) VALUES (1, 'prod-1', 1, 1, '2026-05-28 12:00:00', 1, 'public-token', '')`)
	if err != nil {
		t.Fatalf("insert server: %v", err)
	}

	h := &StatusPageHandler{DB: db}
	w := performStatusRequest(h.GetInfo, http.MethodGet, "/api/status/:token/info", "/api/status/public-token/info", nil, "")
	if w.Code != http.StatusOK {
		t.Fatalf("GetInfo without password status = %d, want %d", w.Code, http.StatusOK)
	}
}

func newStatusPageTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	_, err = db.Exec(`CREATE TABLE servers (
		id INTEGER PRIMARY KEY,
		name TEXT NOT NULL,
		is_online INTEGER NOT NULL,
		http_probe_healthy INTEGER,
		http_probe_last_at TEXT NOT NULL DEFAULT '',
		status_page_enabled INTEGER NOT NULL,
		status_page_token TEXT NOT NULL,
		status_page_password TEXT NOT NULL DEFAULT ''
	)`)
	if err != nil {
		t.Fatalf("create servers table: %v", err)
	}
	return db
}

func performStatusRequest(handler gin.HandlerFunc, method string, route string, target string, body *strings.Reader, cookie string) *httptest.ResponseRecorder {
	router := gin.New()
	router.Handle(method, route, handler)

	var reqBody *strings.Reader
	if body == nil {
		reqBody = strings.NewReader("")
	} else {
		reqBody = body
	}
	req := httptest.NewRequest(method, target, reqBody)
	req.Header.Set("Content-Type", "application/json")
	if cookie != "" {
		req.Header.Set("Cookie", cookie)
	}

	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}
