package handlers

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	_ "modernc.org/sqlite"
)

func TestServerListDerivesAndFiltersStatusFromExpiryDate(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := newServerStatusTestDB(t)

	mustExecServerStatus(t, db, `INSERT INTO servers (id, name, expiry_date, status) VALUES
		(1, 'past-with-stale-active', date('now', '-1 day'), 'active'),
		(2, 'future-with-stale-expired', date('now', '+1 day'), 'expired'),
		(3, 'expires-today', date('now'), 'expired'),
		(4, 'no-expiry', '', 'expired')`)
	mustExecServerStatus(t, db, `INSERT INTO websites (server_id) VALUES (1), (1), (3)`)

	h := &ServerHandler{DB: db}
	items := requestServerListItems(t, h, "/api/servers?page_size=100")
	want := map[string]string{
		"past-with-stale-active":    "expired",
		"future-with-stale-expired": "active",
		"expires-today":             "active",
		"no-expiry":                 "active",
	}
	if len(items) != len(want) {
		t.Fatalf("item count = %d, want %d", len(items), len(want))
	}
	for _, item := range items {
		if item.Status != want[item.Name] {
			t.Errorf("server %q status = %q, want %q", item.Name, item.Status, want[item.Name])
		}
		if item.Name == "past-with-stale-active" && item.WebsiteCount != 2 {
			t.Errorf("server %q website_count = %d, want 2", item.Name, item.WebsiteCount)
		}
		if item.Name == "no-expiry" && item.WebsiteCount != 0 {
			t.Errorf("server %q website_count = %d, want 0", item.Name, item.WebsiteCount)
		}
	}

	expired := requestServerListItems(t, h, "/api/servers?page_size=100&status=expired")
	if len(expired) != 1 || expired[0].Name != "past-with-stale-active" {
		t.Fatalf("expired filter returned %+v, want only past-with-stale-active", expired)
	}
	active := requestServerListItems(t, h, "/api/servers?page_size=100&status=active")
	if len(active) != 3 {
		t.Fatalf("active filter item count = %d, want 3: %+v", len(active), active)
	}
}

type serverStatusListItem struct {
	Name         string `json:"name"`
	Status       string `json:"status"`
	WebsiteCount int    `json:"website_count"`
}

func requestServerListItems(t *testing.T, h *ServerHandler, target string) []serverStatusListItem {
	t.Helper()
	router := gin.New()
	router.GET("/api/servers", h.List)
	req := httptest.NewRequest(http.MethodGet, target, nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET %s status = %d, body=%s", target, w.Code, w.Body.String())
	}
	var response struct {
		Data struct {
			Items []serverStatusListItem `json:"items"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode GET %s response: %v, body=%s", target, err, w.Body.String())
	}
	return response.Data.Items
}

func newServerStatusTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	mustExecServerStatus(t, db, `CREATE TABLE servers (
		id INTEGER PRIMARY KEY, name TEXT NOT NULL, ip_address TEXT NOT NULL DEFAULT '',
		server_type TEXT NOT NULL DEFAULT '', os TEXT NOT NULL DEFAULT '', customer_id INTEGER,
		cpu_cores REAL NOT NULL DEFAULT 0, ram_gb REAL NOT NULL DEFAULT 0, disk_gb REAL NOT NULL DEFAULT 0,
		bandwidth TEXT NOT NULL DEFAULT '', provider_id INTEGER, location TEXT NOT NULL DEFAULT '',
		ssh_port INTEGER NOT NULL DEFAULT 22, ssh_username TEXT NOT NULL DEFAULT '',
		panel_type TEXT NOT NULL DEFAULT 'none', panel_url TEXT NOT NULL DEFAULT '', panel_username TEXT NOT NULL DEFAULT '',
		purchase_date TEXT NOT NULL DEFAULT '', expiry_date TEXT NOT NULL DEFAULT '', renewal_cycle TEXT NOT NULL DEFAULT '',
		auto_renewal INTEGER NOT NULL DEFAULT 0, purchase_price REAL NOT NULL DEFAULT 0, currency TEXT NOT NULL DEFAULT '',
		status TEXT NOT NULL DEFAULT 'active', agent_version TEXT NOT NULL DEFAULT '', last_seen_at DATETIME,
		is_online INTEGER NOT NULL DEFAULT 0, http_probe_enabled INTEGER NOT NULL DEFAULT 0,
		http_probe_healthy INTEGER, http_probe_last_at DATETIME, http_probe_last_error TEXT NOT NULL DEFAULT '',
		tcp_reachable INTEGER, tcp_reachable_checked_at DATETIME, status_page_enabled INTEGER NOT NULL DEFAULT 0,
		notes TEXT NOT NULL DEFAULT '', created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	)`)
	mustExecServerStatus(t, db, `CREATE TABLE customers (id INTEGER PRIMARY KEY, name TEXT NOT NULL)`)
	mustExecServerStatus(t, db, `CREATE TABLE providers (id INTEGER PRIMARY KEY, name TEXT NOT NULL)`)
	mustExecServerStatus(t, db, `CREATE TABLE websites (id INTEGER PRIMARY KEY, server_id INTEGER NOT NULL)`)
	return db
}

func mustExecServerStatus(t *testing.T, db *sql.DB, query string) {
	t.Helper()
	if _, err := db.Exec(query); err != nil {
		t.Fatalf("exec failed: %v\n%s", err, query)
	}
}
