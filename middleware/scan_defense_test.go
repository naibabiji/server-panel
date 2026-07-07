package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestScanDefenseSkipsAgentRoutes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(ScanDefense("smoke", 24))
	r.POST("/agent/ping", func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})

	req := httptest.NewRequest(http.MethodPost, "/agent/ping", nil)
	req.Header.Set("User-Agent", "Go-http-client/1.1")
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("agent route status = %d, want %d", w.Code, http.StatusNoContent)
	}
}

func TestScanDefenseAllowsNonBrowserPanelRoutes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(ScanDefense("smoke", 24))
	r.GET("/smoke/api/auth/check", func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})

	req := httptest.NewRequest(http.MethodGet, "/smoke/api/auth/check", nil)
	req.Header.Set("User-Agent", "Go-http-client/1.1")
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("panel route status = %d, want %d", w.Code, http.StatusNoContent)
	}
}

func TestScanDefenseBlocksNonBrowserUnknownRoutes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(ScanDefense("smoke", 24))

	req := httptest.NewRequest(http.MethodGet, "/wp-login.php", nil)
	req.Header.Set("User-Agent", "Go-http-client/1.1")
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("unknown route status = %d, want %d", w.Code, http.StatusForbidden)
	}
}
