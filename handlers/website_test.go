package handlers

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/naibabiji/server-panel/database"
	_ "modernc.org/sqlite"
)

func TestWebsiteCreateRejectsCaseInsensitiveDuplicateDomain(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := newWebsiteTestDB(t)
	h := &WebsiteHandler{DB: db}

	w := performWebsiteRequest(h.Create, http.MethodPost, "/api/websites", "/api/websites",
		strings.NewReader(`{"domain":"Example.com","server_id":1}`))
	if w.Code != http.StatusOK {
		t.Fatalf("first Create status = %d, want %d, body=%s", w.Code, http.StatusOK, w.Body.String())
	}

	w = performWebsiteRequest(h.Create, http.MethodPost, "/api/websites", "/api/websites",
		strings.NewReader(`{"domain":"example.com","server_id":1}`))
	if w.Code != http.StatusConflict {
		t.Fatalf("duplicate (different case) Create status = %d, want %d, body=%s", w.Code, http.StatusConflict, w.Body.String())
	}

	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM websites").Scan(&count); err != nil {
		t.Fatalf("count websites: %v", err)
	}
	if count != 1 {
		t.Fatalf("websites count = %d, want 1", count)
	}
}

func TestWebsiteUpdateRejectsCaseInsensitiveDuplicateDomainWithOtherRow(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := newWebsiteTestDB(t)
	h := &WebsiteHandler{DB: db}

	mustExecWebsite(t, db, "INSERT INTO websites (id, domain, server_id) VALUES (1, 'example.com', 1)")
	mustExecWebsite(t, db, "INSERT INTO websites (id, domain, server_id) VALUES (2, 'other.com', 1)")

	w := performWebsiteRequest(h.Update, http.MethodPut, "/api/websites/:id", "/api/websites/2",
		strings.NewReader(`{"domain":"Example.com","server_id":1}`))
	if w.Code != http.StatusConflict {
		t.Fatalf("rename to existing domain (different case) status = %d, want %d, body=%s", w.Code, http.StatusConflict, w.Body.String())
	}

	var domain string
	if err := db.QueryRow("SELECT domain FROM websites WHERE id = 2").Scan(&domain); err != nil {
		t.Fatalf("query website 2: %v", err)
	}
	if domain != "other.com" {
		t.Fatalf("website 2 domain = %q, want unchanged %q", domain, "other.com")
	}
}

func TestWebsiteUpdateAllowsKeepingOwnDomain(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := newWebsiteTestDB(t)
	h := &WebsiteHandler{DB: db}

	mustExecWebsite(t, db, "INSERT INTO websites (id, domain, server_id) VALUES (1, 'example.com', 1)")

	w := performWebsiteRequest(h.Update, http.MethodPut, "/api/websites/:id", "/api/websites/1",
		strings.NewReader(`{"domain":"example.com","server_id":1,"name":"renamed"}`))
	if w.Code != http.StatusOK {
		t.Fatalf("update keeping own domain status = %d, want %d, body=%s", w.Code, http.StatusOK, w.Body.String())
	}
}

func mustExecWebsite(t *testing.T, db *sql.DB, query string) {
	t.Helper()
	if _, err := db.Exec(query); err != nil {
		t.Fatalf("exec %q: %v", query, err)
	}
}

func newWebsiteTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	oldDB := database.DB
	database.DB = db
	t.Cleanup(func() { database.DB = oldDB })

	_, err = db.Exec(`CREATE TABLE websites (
		id                 INTEGER PRIMARY KEY AUTOINCREMENT,
		name               TEXT NOT NULL DEFAULT '',
		domain             TEXT NOT NULL,
		site_type          TEXT NOT NULL DEFAULT '',
		server_id          INTEGER NOT NULL,
		customer_id        INTEGER,
		panel_type         TEXT NOT NULL DEFAULT 'none',
		panel_url          TEXT NOT NULL DEFAULT '',
		panel_username     TEXT NOT NULL DEFAULT '',
		panel_password_enc TEXT NOT NULL DEFAULT '',
		expiry_date        TEXT,
		status             TEXT NOT NULL DEFAULT 'active',
		notes              TEXT NOT NULL DEFAULT '',
		created_at         DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at         DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	)`)
	if err != nil {
		t.Fatalf("create websites table: %v", err)
	}

	_, err = db.Exec(`CREATE TABLE settings (skey TEXT PRIMARY KEY, svalue TEXT NOT NULL DEFAULT '')`)
	if err != nil {
		t.Fatalf("create settings table: %v", err)
	}
	// Website Create/Update require the view-password gate to be set up
	// before they'll touch the DB at all (isViewPasswordSetup reads this).
	mustExecWebsite(t, db, "INSERT INTO settings (skey, svalue) VALUES ('view_password_hash', 'test-hash')")

	return db
}

func performWebsiteRequest(handler gin.HandlerFunc, method string, route string, target string, body *strings.Reader) *httptest.ResponseRecorder {
	router := gin.New()
	router.Handle(method, route, handler)

	req := httptest.NewRequest(method, target, body)
	req.Header.Set("Content-Type", "application/json")

	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}
