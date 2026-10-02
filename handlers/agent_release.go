package handlers

import (
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/naibabiji/server-panel/executor"
	"github.com/naibabiji/server-panel/i18n"
	"github.com/naibabiji/server-panel/models"
	"github.com/naibabiji/server-panel/releasecheck"
)

// AgentReleaseHandler gives the panel-generated Agent install/upgrade
// commands a signature-verified Agent hash. The target server then only
// compares one SHA-256 that the panel already checked against the release
// signing key, so a GitHub proxy (which serves both binary and checksums)
// can never substitute the binary that later backs the root self-updater.
type AgentReleaseHandler struct {
	PanelVersion string

	// Test seams; nil means the real implementation.
	latestTag func() (string, error)
	fetcher   func(proxy string) func(string) ([]byte, error)
	verify    func(message, sigFile []byte) error
}

var agentReleaseArches = []string{"amd64", "arm64"}

// InstallInfo (POST, so the protected group's CSRF check applies - the CSRF
// middleware skips GET) returns {version, sha256: {amd64, arm64}} for the
// release the commands should install: the panel's own version (Agents follow
// the panel), or the latest release when the panel is a dev/prerelease build.
//
// The request makes the panel fetch from an admin-supplied proxy host, so
// every connection goes through releasecheck.PublicTransport: loopback,
// LAN/private, link-local (cloud metadata) and multicast targets are refused
// on the resolved IP, which also covers DNS rebinding.
func (h *AgentReleaseHandler) InstallInfo(c *gin.Context) {
	var req struct {
		GitHubProxy string `json:"github_proxy"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse(i18n.TE(c.Request, "errors.invalid_request")))
		return
	}
	proxy := strings.TrimSpace(req.GitHubProxy)
	if proxy != "" && !validAgentProxyURL(proxy) {
		c.JSON(http.StatusBadRequest, models.ErrorResponse(i18n.TE(c.Request, "errors.agent.invalid_proxy")))
		return
	}

	fail := func(err error) {
		c.JSON(http.StatusBadGateway, models.ErrorResponse(
			i18n.TE(c.Request, "errors.agent.release_info_failed", i18n.P{"error": err.Error()})))
	}

	tag := h.PanelVersion
	if !releasecheck.IsStableVersion(tag) {
		latest := h.latestTag
		if latest == nil {
			latest = func() (string, error) {
				r, err := executor.FetchLatestPanelRelease()
				if err != nil {
					return "", err
				}
				return r.TagName, nil
			}
		}
		var err error
		if tag, err = latest(); err != nil {
			fail(err)
			return
		}
	}

	newFetcher := h.fetcher
	if newFetcher == nil {
		newFetcher = func(proxy string) func(string) ([]byte, error) {
			return releasecheck.NewFetcher(proxy, releasecheck.PublicTransport(), 30*time.Second)
		}
	}
	verify := h.verify
	if verify == nil {
		verify = releasecheck.VerifySignatureFile
	}
	fetch := newFetcher(proxy)

	sums := make(map[string]string, len(agentReleaseArches))
	for _, arch := range agentReleaseArches {
		hash, err := releasecheck.VerifiedAgentChecksum(fetch, verify, proxy, tag, arch)
		if err != nil {
			fail(err)
			return
		}
		sums[arch] = hash
	}
	c.JSON(http.StatusOK, models.SuccessResponse(map[string]interface{}{
		"version": tag,
		"sha256":  sums,
	}))
}

// validAgentProxyURL accepts an http(s) URL with a host and no credentials.
// A literal IP host must be public; hostnames are checked on every
// connection by releasecheck.PublicTransport.
func validAgentProxyURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil {
		return false
	}
	if ip := net.ParseIP(u.Hostname()); ip != nil && !releasecheck.IsPublicIP(ip) {
		return false
	}
	return !strings.EqualFold(u.Hostname(), "localhost")
}
