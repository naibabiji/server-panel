package middleware

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/naibabiji/server-panel/models"
)

func TestSessionRequiredRefreshesCookieMaxAge(t *testing.T) {
	gin.SetMode(gin.TestMode)

	session := GlobalSessionStore.Create("admin")
	t.Cleanup(func() { GlobalSessionStore.Delete(session.Token) })

	router := gin.New()
	router.GET("/protected", SessionRequired(), func(c *gin.Context) {
		c.Status(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.AddCookie(&http.Cookie{Name: "sp_session", Value: session.Token})
	req.Header.Set("Accept", "application/json")

	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var refreshed *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == "sp_session" {
			refreshed = c
		}
	}
	if refreshed == nil {
		t.Fatal("SessionRequired did not refresh the sp_session cookie on a valid request")
	}
	if refreshed.Value != session.Token {
		t.Errorf("refreshed cookie value = %q, want %q", refreshed.Value, session.Token)
	}
	if refreshed.MaxAge != 1800 {
		t.Errorf("refreshed cookie MaxAge = %d, want 1800 (so the browser's copy stays in sync with the server-side sliding expiry)", refreshed.MaxAge)
	}
}

func TestSessionRequiredRejectsMissingCookie(t *testing.T) {
	gin.SetMode(gin.TestMode)

	router := gin.New()
	router.GET("/protected", SessionRequired(), func(c *gin.Context) {
		c.Status(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("Accept", "application/json")

	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusUnauthorized)
	}
	if !strings.Contains(w.Body.String(), "请先登录") {
		t.Errorf("body = %q, want it to mention 请先登录", w.Body.String())
	}
	var response models.ApiResponse
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.ErrorCode != models.ErrorCodeSessionRequired {
		t.Errorf("error_code = %q, want %q", response.ErrorCode, models.ErrorCodeSessionRequired)
	}
}

func TestSessionRequiredMarksExpiredSession(t *testing.T) {
	gin.SetMode(gin.TestMode)

	router := gin.New()
	router.GET("/protected", SessionRequired(), func(c *gin.Context) {
		c.Status(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.AddCookie(&http.Cookie{Name: "sp_session", Value: "expired-or-unknown"})
	req.Header.Set("Accept", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	var response models.ApiResponse
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusUnauthorized)
	}
	if response.ErrorCode != models.ErrorCodeSessionExpired {
		t.Errorf("error_code = %q, want %q", response.ErrorCode, models.ErrorCodeSessionExpired)
	}
}
