package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/naibabiji/server-panel/config"
	"golang.org/x/crypto/bcrypt"
)

type BasicAuthChecker struct {
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

		// BasicAuth 是否启用由运行时配置决定（设置界面可动态开关，保存后立即生效）。
		// 配置缺失时默认开启，避免误关安全防护。
		enabled := true
		if cfg := config.AppConfig; cfg != nil {
			enabled = cfg.Security.BasicAuthEnabled
		}
		if !enabled {
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
