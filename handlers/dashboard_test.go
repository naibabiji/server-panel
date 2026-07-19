package handlers

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/naibabiji/server-panel/database"
	_ "modernc.org/sqlite"
)

// Regression test for the "overdue records vanish from the dashboard"
// bug: a server/website whose expiry_date is in the past but is still
// status='active' (i.e. never renewed/handled) must keep showing up,
// in its own overdue_* bucket, without crowding out items that are
// genuinely expiring soon in the next 30 days.
func TestDashboardGetExpiringSplitsOverdueFromUpcoming(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := newDashboardTestDB(t)

	mustExecDashboard(t, db, "INSERT INTO servers (id, name, expiry_date, status) VALUES (1, 'soon-server', date('now','+5 days'), 'active')")
	mustExecDashboard(t, db, "INSERT INTO servers (id, name, expiry_date, status) VALUES (2, 'overdue-server', date('now','-40 days'), 'active')")
	mustExecDashboard(t, db, "INSERT INTO websites (id, name, domain, server_id, expiry_date, status) VALUES (1, 'soon-site', 'soon.example', 1, date('now','+5 days'), 'active')")
	mustExecDashboard(t, db, "INSERT INTO websites (id, name, domain, server_id, expiry_date, status) VALUES (2, 'overdue-site', 'overdue.example', 1, date('now','-40 days'), 'active')")

	// Flood the overdue bucket with 11 mildly-overdue records, all less
	// overdue than "overdue-server" (-40 days), to prove that LIMIT 10 on
	// the overdue query is independent from LIMIT 10 on the "soon" query -
	// it must not crowd out soon-server, which sits in a separate result set.
	for i := 3; i <= 13; i++ {
		mustExecDashboard(t, db, "INSERT INTO servers (id, name, expiry_date, status) VALUES (?, ?, date('now','-2 days'), 'active')", i, fmt.Sprintf("mildly-overdue-%d", i))
	}

	h := &DashboardHandler{}
	w := performDashboardRequest(h.GetExpiring)
	if w.Code != http.StatusOK {
		t.Fatalf("GetExpiring status = %d, want %d, body=%s", w.Code, http.StatusOK, w.Body.String())
	}

	var resp struct {
		Data struct {
			Servers         []map[string]interface{} `json:"servers"`
			Websites        []map[string]interface{} `json:"websites"`
			OverdueServers  []map[string]interface{} `json:"overdue_servers"`
			OverdueWebsites []map[string]interface{} `json:"overdue_websites"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v, body=%s", err, w.Body.String())
	}

	foundSoonServer := false
	for _, s := range resp.Data.Servers {
		if s["name"] == "soon-server" {
			foundSoonServer = true
		}
		if days, ok := s["days_left"].(float64); ok && days < 0 {
			t.Fatalf("expiring servers list contains an overdue item: %+v", s)
		}
	}
	if !foundSoonServer {
		t.Fatalf("soon-server missing from expiring servers list (crowded out?): %+v", resp.Data.Servers)
	}

	foundOverdueServer := false
	for _, s := range resp.Data.OverdueServers {
		if s["name"] == "overdue-server" {
			foundOverdueServer = true
		}
	}
	if !foundOverdueServer {
		t.Fatalf("overdue-server missing from overdue servers list: %+v", resp.Data.OverdueServers)
	}

	foundSoonSite, foundOverdueSite := false, false
	for _, s := range resp.Data.Websites {
		if s["name"] == "soon-site" {
			foundSoonSite = true
		}
	}
	for _, s := range resp.Data.OverdueWebsites {
		if s["name"] == "overdue-site" {
			foundOverdueSite = true
		}
	}
	if !foundSoonSite {
		t.Fatalf("soon-site missing from expiring websites list: %+v", resp.Data.Websites)
	}
	if !foundOverdueSite {
		t.Fatalf("overdue-site missing from overdue websites list: %+v", resp.Data.OverdueWebsites)
	}
}

func TestDashboardGetStatsReportsOverdueSeparatelyFromExpiring(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := newDashboardTestDB(t)

	mustExecDashboard(t, db, "INSERT INTO servers (id, name, expiry_date, status) VALUES (1, 'soon', date('now','+5 days'), 'active')")
	mustExecDashboard(t, db, "INSERT INTO servers (id, name, expiry_date, status) VALUES (2, 'overdue', date('now','-5 days'), 'active')")

	h := &DashboardHandler{}
	w := performDashboardRequest(h.GetStats)
	if w.Code != http.StatusOK {
		t.Fatalf("GetStats status = %d, want %d, body=%s", w.Code, http.StatusOK, w.Body.String())
	}

	var resp struct {
		Data struct {
			ExpiringCount int `json:"expiring_count"`
			OverdueCount  int `json:"overdue_count"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v, body=%s", err, w.Body.String())
	}
	if resp.Data.ExpiringCount != 1 {
		t.Fatalf("expiring_count = %d, want 1", resp.Data.ExpiringCount)
	}
	if resp.Data.OverdueCount != 1 {
		t.Fatalf("overdue_count = %d, want 1", resp.Data.OverdueCount)
	}
}

func mustExecDashboard(t *testing.T, db *sql.DB, query string, args ...interface{}) {
	t.Helper()
	if _, err := db.Exec(query, args...); err != nil {
		t.Fatalf("exec %q: %v", query, err)
	}
}

func newDashboardTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	oldDB := database.DB
	database.DB = db
	t.Cleanup(func() { database.DB = oldDB })

	stmts := []string{
		`CREATE TABLE servers (
			id            INTEGER PRIMARY KEY AUTOINCREMENT,
			name          TEXT NOT NULL DEFAULT '',
			expiry_date   TEXT NOT NULL DEFAULT '',
			status        TEXT NOT NULL DEFAULT 'active',
			is_online     INTEGER NOT NULL DEFAULT 0,
			agent_version TEXT NOT NULL DEFAULT '',
			last_seen_at  DATETIME
		)`,
		`CREATE TABLE websites (
			id          INTEGER PRIMARY KEY AUTOINCREMENT,
			name        TEXT NOT NULL DEFAULT '',
			domain      TEXT NOT NULL DEFAULT '',
			server_id   INTEGER NOT NULL,
			expiry_date TEXT NOT NULL DEFAULT '',
			status      TEXT NOT NULL DEFAULT 'active'
		)`,
		`CREATE TABLE alert_log (
			id         INTEGER PRIMARY KEY AUTOINCREMENT,
			alert_type TEXT NOT NULL,
			resolved   INTEGER NOT NULL DEFAULT 0,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,
	}
	for _, stmt := range stmts {
		mustExecDashboard(t, db, stmt)
	}
	return db
}

func performDashboardRequest(handler gin.HandlerFunc) *httptest.ResponseRecorder {
	router := gin.New()
	router.GET("/api/dashboard/x", handler)

	req := httptest.NewRequest(http.MethodGet, "/api/dashboard/x", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}
