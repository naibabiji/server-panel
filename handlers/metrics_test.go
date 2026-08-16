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

type metricsOverviewResponse struct {
	Success bool `json:"success"`
	Data    []struct {
		ID           int64   `json:"id"`
		Name         string  `json:"name"`
		IPAddress    string  `json:"ip_address"`
		ProviderName string  `json:"provider_name"`
		CPUCores     float64 `json:"cpu_cores"`
		CPUPercent   float64 `json:"cpu_percent"`
		RecordedAt   string  `json:"recorded_at"`
	} `json:"data"`
}

func TestMetricsGetOverviewReturnsLatestMetricPerServer(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := newMetricsTestDB(t)

	mustExecMetrics(t, db, `INSERT INTO providers (id, name) VALUES (1, 'Zulu')`)
	mustExecMetrics(t, db, `INSERT INTO servers (id, name, ip_address, provider_id, agent_version, cpu_cores) VALUES
		(1, 'alpha', '192.0.2.1', 1, '1.0', 4),
		(2, 'beta', '192.0.2.2', NULL, '1.0', 2)`)
	mustExecMetrics(t, db, `INSERT INTO metrics (server_id, cpu_percent, recorded_at) VALUES
		(1, 10, '2026-08-08 10:00:00'),
		(1, 20, '2026-08-08 11:00:00'),
		(2, 30, '2026-08-08 09:00:00'),
		(2, 40, '2026-08-08 12:00:00')`)

	resp := getMetricsOverview(t, db)
	if len(resp.Data) != 2 {
		t.Fatalf("overview item count = %d, want 2: %+v", len(resp.Data), resp.Data)
	}
	byID := make(map[int64]float64, len(resp.Data))
	for _, item := range resp.Data {
		byID[item.ID] = item.CPUPercent
	}
	if byID[1] != 20 || byID[2] != 40 {
		t.Fatalf("latest CPU values = %+v, want server 1=20 and server 2=40", byID)
	}
	if resp.Data[0].CPUCores != 4 || resp.Data[1].CPUCores != 2 {
		t.Fatalf("overview CPU cores = [%v, %v], want [4, 2]", resp.Data[0].CPUCores, resp.Data[1].CPUCores)
	}
}

func TestMetricsGetOverviewUsesNewestIDWhenTimestampsMatch(t *testing.T) {
	db := newMetricsTestDB(t)
	mustExecMetrics(t, db, `INSERT INTO servers (id, name, ip_address, agent_version) VALUES (1, 'alpha', '192.0.2.1', '1.0')`)
	mustExecMetrics(t, db, `INSERT INTO metrics (server_id, cpu_percent, recorded_at) VALUES
		(1, 10, '2026-08-08 10:00:00'),
		(1, 99, '2026-08-08 10:00:00')`)

	resp := getMetricsOverview(t, db)
	if len(resp.Data) != 1 || resp.Data[0].CPUPercent != 99 {
		t.Fatalf("overview data = %+v, want later inserted metric with CPU 99", resp.Data)
	}
}

func TestMetricsGetOverviewPreservesFilteringSortingAndResponseFields(t *testing.T) {
	db := newMetricsTestDB(t)
	mustExecMetrics(t, db, `INSERT INTO providers (id, name) VALUES (1, 'Alpha Provider')`)
	mustExecMetrics(t, db, `INSERT INTO servers
		(id, name, ip_address, provider_id, agent_version, last_seen_at) VALUES
		(1, 'with-agent', '192.0.2.1', 1, '1.0', NULL),
		(2, 'seen-only', '192.0.2.2', NULL, '', '2026-08-08 10:00:00'),
		(3, 'metric-only', '192.0.2.3', NULL, '', NULL),
		(4, 'hidden', '192.0.2.4', NULL, '', NULL)`)
	mustExecMetrics(t, db, `INSERT INTO metrics (server_id, cpu_percent, recorded_at) VALUES (3, 55, '2026-08-08 10:00:00')`)

	resp := getMetricsOverview(t, db)
	if !resp.Success {
		t.Fatal("overview response success = false, want true")
	}
	if len(resp.Data) != 3 {
		t.Fatalf("overview item count = %d, want 3: %+v", len(resp.Data), resp.Data)
	}
	if resp.Data[0].Name != "with-agent" || resp.Data[0].ProviderName != "Alpha Provider" {
		t.Fatalf("first sorted item = %+v, want provider-backed server first", resp.Data[0])
	}
	if resp.Data[1].Name != "metric-only" || resp.Data[2].Name != "seen-only" {
		t.Fatalf("unset-provider items not sorted by name: %+v", resp.Data)
	}
	if resp.Data[2].IPAddress != "192.0.2.2" {
		t.Fatalf("ip_address = %q, want response field preserved", resp.Data[2].IPAddress)
	}
	for _, item := range resp.Data {
		if item.Name == "hidden" {
			t.Fatal("server without agent, last_seen, or metrics unexpectedly returned")
		}
	}
}

func getMetricsOverview(t *testing.T, db *sql.DB) metricsOverviewResponse {
	t.Helper()
	router := gin.New()
	router.GET("/api/monitor/overview", (&MetricsHandler{DB: db}).GetOverview)

	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/monitor/overview", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("GetOverview status = %d, want %d, body=%s", w.Code, http.StatusOK, w.Body.String())
	}

	var resp metricsOverviewResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal overview response: %v, body=%s", err, w.Body.String())
	}
	return resp
}

func newMetricsTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open metrics test db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	statements := []string{
		`CREATE TABLE providers (id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT NOT NULL UNIQUE)`,
		`CREATE TABLE servers (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL DEFAULT '',
			ip_address TEXT NOT NULL DEFAULT '',
			is_online INTEGER NOT NULL DEFAULT 0,
			last_seen_at DATETIME,
			cpu_cores REAL NOT NULL DEFAULT 0,
			provider_id INTEGER,
			agent_version TEXT NOT NULL DEFAULT '',
			http_probe_enabled INTEGER NOT NULL DEFAULT 0,
			http_probe_healthy INTEGER,
			http_probe_last_at DATETIME,
			tcp_reachable INTEGER
		)`,
		`CREATE TABLE metrics (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			server_id INTEGER NOT NULL,
			cpu_percent REAL,
			memory_percent REAL,
			disk_percent REAL,
			net_rx_bytes INTEGER,
			net_tx_bytes INTEGER,
			load_avg_1 REAL,
			uptime_seconds INTEGER,
			recorded_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE INDEX idx_metrics_server_time ON metrics(server_id, recorded_at)`,
	}
	for _, statement := range statements {
		mustExecMetrics(t, db, statement)
	}
	return db
}

func mustExecMetrics(t *testing.T, db *sql.DB, query string, args ...interface{}) {
	t.Helper()
	if _, err := db.Exec(query, args...); err != nil {
		t.Fatalf("exec %q: %v", query, err)
	}
}
