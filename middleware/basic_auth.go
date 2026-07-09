package middleware

import (
	"net/http"
	"sync/atomic"

	"github.com/gin-gonic/gin"
	"github.com/naibabiji/server-panel/config"
	"golang.org/x/crypto/bcrypt"
)

// basicAuthEnabled 控制 BasicAuth 是否启用，使用 atomic.Bool 避免与设置保存
// 时的并发读写产生数据竞争（每个请求都会读取该值）。
var basicAuthEnabled atomic.Bool

func init() {
	// 默认开启，避免误关安全防护。
	basicAuthEnabled.Store(true)
}

// SetBasicAuthEnabled 更新 BasicAuth 启用状态（配置加载 / 设置保存时调用）。
func SetBasicAuthEnabled(v bool) {
	basicAuthEnabled.Store(v)
}

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

		// BasicAuth 是否启用由运行时状态决定（设置界面可动态开关，保存后立即生效）。
		if !basicAuthEnabled.Load() {
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
