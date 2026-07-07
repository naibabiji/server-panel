package middleware

import (
	"net"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/naibabiji/server-panel/executor"
)

// ClientIP resolves the real visitor address for security decisions (bans,
// login-attempt counting, view-token binding). It defers to gin's own
// TrustedProxies-based ClientIP() for statically configured proxies (same-
// host reverse proxy, or an admin-listed remote one), and additionally
// recognizes Cloudflare's published edge ranges: when the direct peer is a
// known Cloudflare IP, it reads the visitor address from the CF-Connecting-IP
// header Cloudflare sets on every proxied request, rather than gin's default
// X-Forwarded-For handling (which would require trusting Cloudflare's entire,
// frequently-changing IP list via SetTrustedProxies).
//
// Every place that currently calls c.ClientIP() directly for a security
// decision should call this instead so CDN edge traffic doesn't collapse
// into one shared IP.
func ClientIP(c *gin.Context) string {
	if peer := net.ParseIP(c.RemoteIP()); peer != nil && executor.IsCloudflareIP(peer) {
		if cfIP := strings.TrimSpace(c.GetHeader("CF-Connecting-IP")); cfIP != "" {
			if parsed := net.ParseIP(cfIP); parsed != nil {
				return parsed.String()
			}
		}
	}
	return c.ClientIP()
}
