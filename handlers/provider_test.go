package handlers

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	_ "modernc.org/sqlite"
)

func TestProviderCreateRejectsCaseInsensitiveDuplicate(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := newProviderTestDB(t)
	h := &ProviderHandler{DB: db}

	w := performProviderRequest(h.Create, http.MethodPost, "/api/providers", "/api/providers",
		strings.NewReader(`{"name":"DigitalOcean"}`))
	if w.Code != http.StatusOK {
		t.Fatalf("first Create status = %d, want %d, body=%s", w.Code, http.StatusOK, w.Body.String())
	}

	w = performProviderRequest(h.Create, http.MethodPost, "/api/providers", "/api/providers",
		strings.NewReader(`{"name":"digitalocean"}`))
	if w.Code != http.StatusConflict {
		t.Fatalf("duplicate (different case) Create status = %d, want %d, body=%s", w.Code, http.StatusConflict, w.Body.String())
	}

	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM providers").Scan(&count); err != nil {
		t.Fatalf("count providers: %v", err)
	}
	if count != 1 {
		t.Fatalf("providers count = %d, want 1", count)
	}
}

func TestProviderUpdateRejectsCaseInsensitiveDuplicateWithOtherRow(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := newProviderTestDB(t)
	h := &ProviderHandler{DB: db}

	mustExec(t, db, "INSERT INTO providers (id, name) VALUES (1, 'DigitalOcean')")
	mustExec(t, db, "INSERT INTO providers (id, name) VALUES (2, 'Vultr')")

	w := performProviderRequest(h.Update, http.MethodPut, "/api/providers/:id", "/api/providers/2",
		strings.NewReader(`{"name":"digitalocean"}`))
	if w.Code != http.StatusConflict {
		t.Fatalf("rename to existing name (different case) status = %d, want %d, body=%s", w.Code, http.StatusConflict, w.Body.String())
	}

	var name string
	if err := db.QueryRow("SELECT name FROM providers WHERE id = 2").Scan(&name); err != nil {
		t.Fatalf("query provider 2: %v", err)
	}
	if name != "Vultr" {
		t.Fatalf("provider 2 name = %q, want unchanged %q", name, "Vultr")
	}
}

func TestProviderUpdateAllowsRenamingToOwnName(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := newProviderTestDB(t)
	h := &ProviderHandler{DB: db}

	mustExec(t, db, "INSERT INTO providers (id, name) VALUES (1, 'DigitalOcean')")

	w := performProviderRequest(h.Update, http.MethodPut, "/api/providers/:id", "/api/providers/1",
		strings.NewReader(`{"name":"DigitalOcean","website":"https://digitalocean.com"}`))
	if w.Code != http.StatusOK {
		t.Fatalf("rename to own name status = %d, want %d, body=%s", w.Code, http.StatusOK, w.Body.String())
	}
}

func TestProviderClearPrivateNotesRequiresTokenAndClearsValue(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := newProviderTestDB(t)
	h := &ProviderHandler{DB: db}

	mustExec(t, db, "INSERT INTO settings (skey, svalue) VALUES ('view_password_hash', 'set')")
	mustExec(t, db, "INSERT INTO providers (id, name, private_notes_enc) VALUES (1, 'DigitalOcean', 'encrypted')")

	const sessionToken = "session-1"

	w := performProviderRequestWithViewToken(h.ClearPrivateNotes, http.MethodDelete, "/api/providers/:id/secrets/private-notes", "/api/providers/1/secrets/private-notes", sessionToken, "")
	if w.Code != http.StatusForbidden {
		t.Fatalf("ClearPrivateNotes without token status = %d, want %d, body=%s", w.Code, http.StatusForbidden, w.Body.String())
	}

	viewToken, err := CreateViewToken(sessionToken, "192.0.2.1")
	if err != nil {
		t.Fatalf("CreateViewToken: %v", err)
	}
	w = performProviderRequestWithViewToken(h.ClearPrivateNotes, http.MethodDelete, "/api/providers/:id/secrets/private-notes", "/api/providers/1/secrets/private-notes", sessionToken, viewToken)
	if w.Code != http.StatusOK {
		t.Fatalf("ClearPrivateNotes status = %d, want %d, body=%s", w.Code, http.StatusOK, w.Body.String())
	}

	var encrypted string
	if err := db.QueryRow("SELECT private_notes_enc FROM providers WHERE id = 1").Scan(&encrypted); err != nil {
		t.Fatalf("query private_notes_enc: %v", err)
	}
	if encrypted != "" {
		t.Fatalf("private_notes_enc = %q, want empty", encrypted)
	}
}

func mustExec(t *testing.T, db *sql.DB, query string) {
	t.Helper()
	if _, err := db.Exec(query); err != nil {
		t.Fatalf("exec %q: %v", query, err)
	}
}

func newProviderTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	_, err = db.Exec(`CREATE TABLE providers (
		id                INTEGER PRIMARY KEY AUTOINCREMENT,
		name              TEXT NOT NULL UNIQUE,
		website           TEXT NOT NULL DEFAULT '',
		contact           TEXT NOT NULL DEFAULT '',
		private_notes_enc TEXT NOT NULL DEFAULT '',
		notes             TEXT NOT NULL DEFAULT '',
		created_at        DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at        DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	)`)
	if err != nil {
		t.Fatalf("create providers table: %v", err)
	}
	_, err = db.Exec(`CREATE TABLE settings (
		skey   TEXT PRIMARY KEY,
		svalue TEXT NOT NULL DEFAULT ''
	)`)
	if err != nil {
		t.Fatalf("create settings table: %v", err)
	}
	return db
}

func performProviderRequest(handler gin.HandlerFunc, method string, route string, target string, body *strings.Reader) *httptest.ResponseRecorder {
	router := gin.New()
	router.Handle(method, route, handler)

	req := httptest.NewRequest(method, target, body)
	req.Header.Set("Content-Type", "application/json")

	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}

func performProviderRequestWithViewToken(handler gin.HandlerFunc, method string, route string, target string, sessionToken string, viewToken string) *httptest.ResponseRecorder {
	router := gin.New()
	router.Handle(method, route, func(c *gin.Context) {
		if sessionToken != "" {
			c.Set("session_token", sessionToken)
		}
		handler(c)
	})

	req := httptest.NewRequest(method, target, strings.NewReader(""))
	req.RemoteAddr = "192.0.2.1:1234"
	req.Header.Set("Content-Type", "application/json")
	if viewToken != "" {
		req.Header.Set("X-View-Token", viewToken)
	}

	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}
