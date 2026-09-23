package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestServerCreateAndUpdateRejectUnsafePanelURL(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &ServerHandler{}
	tests := []struct {
		name    string
		method  string
		route   string
		target  string
		handler gin.HandlerFunc
	}{
		{name: "create", method: http.MethodPost, route: "/api/servers", target: "/api/servers", handler: h.Create},
		{name: "update", method: http.MethodPut, route: "/api/servers/:id", target: "/api/servers/1", handler: h.Update},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := performURLValidationRequest(tt.handler, tt.method, tt.route, tt.target,
				`{"name":"server","panel_url":"javascript:alert(1)"}`)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d, body=%s", w.Code, http.StatusBadRequest, w.Body.String())
			}
			if !strings.Contains(w.Body.String(), "http://") {
				t.Fatalf("response does not contain localized URL guidance: %s", w.Body.String())
			}
		})
	}
}

func TestWebsiteCreateAndUpdateRejectUnsafePanelURL(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &WebsiteHandler{}
	tests := []struct {
		name    string
		method  string
		route   string
		target  string
		handler gin.HandlerFunc
	}{
		{name: "create", method: http.MethodPost, route: "/api/websites", target: "/api/websites", handler: h.Create},
		{name: "update", method: http.MethodPut, route: "/api/websites/:id", target: "/api/websites/1", handler: h.Update},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := performURLValidationRequest(tt.handler, tt.method, tt.route, tt.target,
				`{"domain":"example.com","server_id":1,"panel_url":"data:text/html,<script>alert(1)</script>"}`)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d, body=%s", w.Code, http.StatusBadRequest, w.Body.String())
			}
		})
	}
}

func TestValidateOptionalHTTPURLAcceptsEmptyHTTPAndHTTPS(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, value := range []string{"", "  ", "http://example.com", "https://example.com:8443/path"} {
		t.Run(value, func(t *testing.T) {
			router := gin.New()
			router.POST("/", func(c *gin.Context) {
				candidate := value
				if !validateOptionalHTTPURL(c, &candidate) {
					return
				}
				c.Status(http.StatusNoContent)
			})
			w := httptest.NewRecorder()
			router.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/", nil))
			if w.Code != http.StatusNoContent {
				t.Fatalf("status = %d, want %d, body=%s", w.Code, http.StatusNoContent, w.Body.String())
			}
		})
	}
}

func performURLValidationRequest(handler gin.HandlerFunc, method, route, target, body string) *httptest.ResponseRecorder {
	router := gin.New()
	router.Handle(method, route, handler)
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}
