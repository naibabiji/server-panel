package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/naibabiji/server-panel/config"
	"golang.org/x/crypto/bcrypt"
)

type BasicAuthChecker struct {
	// Enabled controls whether HTTP Basic authentication is required. When
	// false, the middleware only enforces the IP ban (defense in depth) and
	// lets requests through without credential checks.
	Enabled       bool
	RecordAttempt func(ip string, attemptType string)
	IsBanned      func(ip string) bool
}

func BasicAuth(checker *BasicAuthChecker) gin.HandlerFunc {
	return func(c *gin.Context) {
		ip := ClientIP(c)

		// 封禁检查始终生效，无论 Basic Auth 是否开启（纵深防御）。
		if checker.IsBanned != nil && checker.IsBanned(ip) {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"success": false,
				"message": "IP已被临时封禁，请稍后再试",
			})
			return
		}

		if !checker.Enabled {
			c.Next()
			return
		}

		user, pass, ok := c.Request.BasicAuth()
		if !ok {
			c.Header("WWW-Authenticate", `Basic realm="Server Panel Authentication"`)
			c.AbortWithStatus(http.StatusUnauthorized)
			return
		}

		cfg := config.AppConfig
		if cfg == nil {
			c.Header("WWW-Authenticate", `Basic realm="Server Panel Authentication"`)
			c.AbortWithStatus(http.StatusUnauthorized)
			return
		}

		if user != cfg.BasicAuth.Username ||
			bcrypt.CompareHashAndPassword([]byte(cfg.BasicAuth.PasswordHash), []byte(pass)) != nil {
			if checker.RecordAttempt != nil {
				checker.RecordAttempt(ip, "basic_auth")
			}
			c.Header("WWW-Authenticate", `Basic realm="Server Panel Authentication"`)
			c.AbortWithStatus(http.StatusUnauthorized)
			return
		}

		c.Set("authenticated_user", user)
		c.Next()
	}
}
