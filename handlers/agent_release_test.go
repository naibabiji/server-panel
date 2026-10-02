package handlers

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func fakeAgentRelease(sigOK bool) (func(string) func(string) ([]byte, error), func([]byte, []byte) error, *[]string) {
	var fetched []string
	files := map[string]string{
		"checksums-amd64.txt":     strings.Repeat("a", 64) + "  server-panel-agent-linux-amd64\n",
		"checksums-amd64.txt.sig": "sig",
		"checksums-arm64.txt":     strings.Repeat("b", 64) + "  server-panel-agent-linux-arm64\n",
		"checksums-arm64.txt.sig": "sig",
	}
	fetcher := func(string) func(string) ([]byte, error) {
		return func(u string) ([]byte, error) {
			fetched = append(fetched, u)
			if body, ok := files[u[strings.LastIndex(u, "/")+1:]]; ok {
				return []byte(body), nil
			}
			return nil, errors.New("404")
		}
	}
	verify := func([]byte, []byte) error {
		if !sigOK {
			return errors.New("release signature verification failed")
		}
		return nil
	}
	return fetcher, verify, &fetched
}

func postInstallInfo(t *testing.T, h *AgentReleaseHandler, proxy string) (int, map[string]interface{}) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/api/agent/install-info", h.InstallInfo)
	body, _ := json.Marshal(map[string]string{"github_proxy": proxy})
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/agent/install-info", bytes.NewReader(body)))
	var resp map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	return w.Code, resp
}

func TestInstallInfoReturnsSignedHashesForPanelVersion(t *testing.T) {
	fetcher, verify, fetched := fakeAgentRelease(true)
	h := &AgentReleaseHandler{PanelVersion: "v1.5.0", fetcher: fetcher, verify: verify,
		latestTag: func() (string, error) { t.Fatal("stable panel must not look up latest"); return "", nil }}

	code, resp := postInstallInfo(t, h, "https://gh.example.com")
	if code != http.StatusOK {
		t.Fatalf("status = %d, resp=%v", code, resp)
	}
	data := resp["data"].(map[string]interface{})
	sums := data["sha256"].(map[string]interface{})
	if data["version"] != "v1.5.0" || sums["amd64"] != strings.Repeat("a", 64) || sums["arm64"] != strings.Repeat("b", 64) {
		t.Fatalf("data = %v", data)
	}
	if !strings.HasPrefix((*fetched)[0], "https://gh.example.com/https://github.com/naibabiji/server-panel/releases/download/v1.5.0/") {
		t.Fatalf("fetched %q, want proxied panel-version URL", (*fetched)[0])
	}
}

// A proxy that swaps binary and checksums cannot forge the signature, so no
// hash is handed to the install command at all.
func TestInstallInfoRejectsUnsignedChecksums(t *testing.T) {
	fetcher, verify, _ := fakeAgentRelease(false)
	h := &AgentReleaseHandler{PanelVersion: "v1.5.0", fetcher: fetcher, verify: verify}
	code, resp := postInstallInfo(t, h, "")
	if code != http.StatusBadGateway {
		t.Fatalf("status = %d, resp=%v", code, resp)
	}
	if _, ok := resp["data"]; ok && resp["data"] != nil {
		t.Fatalf("returned data despite bad signature: %v", resp["data"])
	}
}

func TestInstallInfoUsesLatestForDevPanelAndValidatesProxy(t *testing.T) {
	fetcher, verify, fetched := fakeAgentRelease(true)
	h := &AgentReleaseHandler{PanelVersion: "dev", fetcher: fetcher, verify: verify,
		latestTag: func() (string, error) { return "v1.4.9", nil }}
	if code, resp := postInstallInfo(t, h, ""); code != http.StatusOK ||
		resp["data"].(map[string]interface{})["version"] != "v1.4.9" {
		t.Fatalf("status = %d, resp=%v", code, resp)
	}
	n := len(*fetched)
	for _, bad := range []string{
		"ftp://x", "javascript:alert(1)", "not a url",
		"http://127.0.0.1:8444", "http://[::1]:8444", "http://localhost:8444", "http://169.254.169.254",
		"http://192.168.0.1", "http://10.0.0.1", "http://100.64.0.1", "http://0.0.0.0", "https://user:pw@gh.example.com",
	} {
		if code, _ := postInstallInfo(t, h, bad); code != http.StatusBadRequest {
			t.Errorf("proxy %q: status = %d, want 400", bad, code)
		}
	}
	if len(*fetched) != n {
		t.Fatal("invalid proxy still triggered downloads")
	}
}
