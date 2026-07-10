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

func TestCreateAlertRuleAcceptsNumericPayload(t *testing.T) {
	db := newAlertHandlerTestDB(t)
	h := &AlertHandler{DB: db}
	w := performAlertRuleRequest(h.CreateRule, http.MethodPost, "/api/alerts/rules",
		`{"alert_type":"cpu_high","name":"CPU test","enabled":1,"threshold_value":90,"threshold_count":3,"notify_user":0,"notify_email":""}`)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}

	var alertType, name string
	var enabled, thresholdCount int
	var thresholdValue float64
	if err := db.QueryRow(`SELECT alert_type, name, enabled, threshold_value, threshold_count FROM alert_rules`).Scan(
		&alertType, &name, &enabled, &thresholdValue, &thresholdCount); err != nil {
		t.Fatalf("query alert rule: %v", err)
	}
	if alertType != "cpu_high" || name != "CPU test" || enabled != 1 || thresholdValue != 90 || thresholdCount != 3 {
		t.Fatalf("stored rule = %q %q %d %.1f %d", alertType, name, enabled, thresholdValue, thresholdCount)
	}
}

func TestCreateAlertRuleRejectsStringNumericPayload(t *testing.T) {
	db := newAlertHandlerTestDB(t)
	h := &AlertHandler{DB: db}
	w := performAlertRuleRequest(h.CreateRule, http.MethodPost, "/api/alerts/rules",
		`{"alert_type":"cpu_high","name":"CPU test","enabled":1,"threshold_value":"90","threshold_count":"3","notify_user":0,"notify_email":""}`)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d, body = %s", w.Code, http.StatusBadRequest, w.Body.String())
	}
}

func TestCreateAlertRuleNormalizesHTTPProbeThreshold(t *testing.T) {
	db := newAlertHandlerTestDB(t)
	h := &AlertHandler{DB: db}
	w := performAlertRuleRequest(h.CreateRule, http.MethodPost, "/api/alerts/rules",
		`{"alert_type":"http_probe_down","name":"","enabled":1,"threshold_value":60,"threshold_count":0,"notify_user":0,"notify_email":" ops@example.com "}`)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}

	var name, notifyEmail string
	var thresholdValue float64
	var thresholdCount int
	if err := db.QueryRow(`SELECT name, threshold_value, threshold_count, notify_email FROM alert_rules`).Scan(
		&name, &thresholdValue, &thresholdCount, &notifyEmail); err != nil {
		t.Fatalf("query alert rule: %v", err)
	}
	if name != "HTTP 探测异常" || thresholdValue != 0 || thresholdCount != 3 || notifyEmail != "ops@example.com" {
		t.Fatalf("stored rule = %q %.1f %d %q", name, thresholdValue, thresholdCount, notifyEmail)
	}
}

func newAlertHandlerTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+strings.ReplaceAll(t.Name(), "/", "-")+"?mode=memory&cache=shared")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`CREATE TABLE alert_rules (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		alert_type TEXT NOT NULL,
		name TEXT NOT NULL DEFAULT '',
		enabled INTEGER NOT NULL DEFAULT 1,
		threshold_value REAL NOT NULL DEFAULT 0,
		threshold_count INTEGER NOT NULL DEFAULT 3,
		notify_user INTEGER NOT NULL DEFAULT 0,
		notify_email TEXT NOT NULL DEFAULT '',
		server_id INTEGER,
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	)`); err != nil {
		t.Fatalf("create alert_rules: %v", err)
	}
	return db
}

func performAlertRuleRequest(handler gin.HandlerFunc, method string, target string, body string) *httptest.ResponseRecorder {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Handle(method, target, handler)
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}
