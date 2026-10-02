package handlers

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	_ "modernc.org/sqlite"
)

func TestAgentUpdateTarget(t *testing.T) {
	cases := []struct {
		panel, agent string
		autoUpdate   bool
		want         string
	}{
		{"v1.5.0", "v1.4.9", true, "v1.5.0"},
		{"v1.5.0", "dev", true, "v1.5.0"},
		{"v1.5.0", "v1.4.9", false, ""}, // no updater installed
		{"v1.5.0", "v1.5.0", true, ""},  // already current
		{"v1.5.0", "v1.6.0", true, ""},  // never downgrade
		{"dev", "v1.4.9", true, ""},     // dev panel never issues targets
		{"v1.5.0-rc.1", "v1.4.9", true, ""},
	}
	for _, c := range cases {
		if got := agentUpdateTarget(c.panel, c.agent, c.autoUpdate); got != c.want {
			t.Errorf("agentUpdateTarget(%q, %q, %v) = %q, want %q", c.panel, c.agent, c.autoUpdate, got, c.want)
		}
	}
}

func TestReceiveMetricsStoresUpdateStatusAndReturnsTarget(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	for _, stmt := range []string{
		`CREATE TABLE servers (id INTEGER PRIMARY KEY, agent_version TEXT NOT NULL DEFAULT '',
			agent_auto_update INTEGER NOT NULL DEFAULT 0, agent_update_error TEXT NOT NULL DEFAULT '')`,
		`CREATE TABLE metrics (server_id INTEGER, cpu_percent REAL, memory_percent REAL, memory_used INTEGER,
			memory_total INTEGER, disk_percent REAL, disk_used INTEGER, disk_total INTEGER, net_rx_bytes INTEGER,
			net_tx_bytes INTEGER, load_avg_1 REAL, load_avg_5 REAL, load_avg_15 REAL, uptime_seconds INTEGER,
			ingest_latency_us INTEGER)`,
		`INSERT INTO servers (id) VALUES (7)`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("exec %q: %v", stmt, err)
		}
	}

	h := &AgentDataHandler{DB: db, PanelVersion: "v1.5.0"}
	router := gin.New()
	router.POST("/agent/metrics", func(c *gin.Context) { c.Set("agent_server_id", int64(7)) }, h.ReceiveMetrics)

	post := func(payload string) map[string]interface{} {
		t.Helper()
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/agent/metrics", bytes.NewBufferString(payload)))
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, body=%s", w.Code, w.Body.String())
		}
		var resp struct {
			Data map[string]interface{} `json:"data"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode: %v", err)
		}
		return resp.Data
	}

	// Old Agent: no updater fields, gets no target, is marked not update-capable.
	if data := post(`{"agent_version":"v1.4.9"}`); data["agent_update"] != nil {
		t.Fatalf("old Agent got agent_update %v", data["agent_update"])
	}

	data := post(`{"agent_version":"v1.4.9","auto_update":true,"update_error":"download failed"}`)
	target, _ := data["agent_update"].(map[string]interface{})
	if target["version"] != "v1.5.0" {
		t.Fatalf("agent_update = %v, want version v1.5.0", data["agent_update"])
	}
	var autoUpdate int
	var updateError string
	if err := db.QueryRow(`SELECT agent_auto_update, agent_update_error FROM servers WHERE id = 7`).Scan(&autoUpdate, &updateError); err != nil {
		t.Fatalf("query: %v", err)
	}
	if autoUpdate != 1 || updateError != "download failed" {
		t.Fatalf("stored (%d, %q), want (1, \"download failed\")", autoUpdate, updateError)
	}
}

func TestReceiveMetricsTruncatesUpdateErrorOnRuneBoundary(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	for _, stmt := range []string{
		`CREATE TABLE servers (id INTEGER PRIMARY KEY, agent_version TEXT NOT NULL DEFAULT '',
			agent_auto_update INTEGER NOT NULL DEFAULT 0, agent_update_error TEXT NOT NULL DEFAULT '')`,
		`CREATE TABLE metrics (server_id INTEGER, cpu_percent REAL, memory_percent REAL, memory_used INTEGER,
			memory_total INTEGER, disk_percent REAL, disk_used INTEGER, disk_total INTEGER, net_rx_bytes INTEGER,
			net_tx_bytes INTEGER, load_avg_1 REAL, load_avg_5 REAL, load_avg_15 REAL, uptime_seconds INTEGER,
			ingest_latency_us INTEGER)`,
		`INSERT INTO servers (id) VALUES (7)`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("exec %q: %v", stmt, err)
		}
	}
	h := &AgentDataHandler{DB: db, PanelVersion: "v1.5.0"}
	router := gin.New()
	router.POST("/agent/metrics", func(c *gin.Context) { c.Set("agent_server_id", int64(7)) }, h.ReceiveMetrics)

	body, _ := json.Marshal(map[string]interface{}{
		"agent_version": "v1.4.9", "auto_update": true, "update_error": strings.Repeat("下载失败", 100),
	})
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/agent/metrics", bytes.NewReader(body)))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	var stored string
	if err := db.QueryRow(`SELECT agent_update_error FROM servers WHERE id = 7`).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if len(stored) > maxAgentUpdateErrorLen || !utf8.ValidString(stored) || stored == "" {
		t.Fatalf("stored %d bytes, valid=%v", len(stored), utf8.ValidString(stored))
	}
}
