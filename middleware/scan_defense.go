package middleware

import (
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/naibabiji/server-panel/executor"
)

func ScanDefense(suffix string, banHours int) gin.HandlerFunc {
	return func(c *gin.Context) {
		ua := c.GetHeader("User-Agent")
		if ua != "" && executor.IsBrowserUserAgent(ua) {
			c.Next()
			return
		}

		path := c.Request.URL.Path
		if isKnownPublicOrPanelPath(path, suffix) {
			c.Next()
			return
		}

		ip := c.ClientIP()
		if executor.IsWhitelisted(ip) {
			c.Next()
			return
		}
		executor.BanIP(ip, "scan-"+c.Request.URL.Path, "scan_defense", banHours)
		c.AbortWithStatus(403)
	}
}

func isKnownPublicOrPanelPath(path string, suffix string) bool {
	panelPrefix := "/" + strings.Trim(suffix, "/")
	if path == "/" || path == "/favicon.ico" || path == "/healthz" {
		return true
	}
	if path == panelPrefix || strings.HasPrefix(path, panelPrefix+"/") {
		return true
	}
	if strings.HasPrefix(path, "/status/") || strings.HasPrefix(path, "/api/status/") {
		return true
	}
	return strings.HasPrefix(path, "/agent/")
}
