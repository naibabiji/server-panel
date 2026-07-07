package middleware

import "github.com/gin-gonic/gin"

func IsSecureRequest(c *gin.Context) bool {
	if c.Request.TLS != nil {
		return true
	}
	return c.GetHeader("X-Forwarded-Proto") == "https" || c.GetHeader("X-Forwarded-Ssl") == "on"
}
