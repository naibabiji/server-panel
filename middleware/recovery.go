package middleware

import (
	"log"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/naibabiji/server-panel/i18n"
)

func CustomRecovery() gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if err := recover(); err != nil {
				log.Printf("[PANIC] %v", err)
				c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
					"success": false,
					"message": i18n.TE(c.Request, "server_error.internal"),
				})
			}
		}()
		c.Next()
	}
}
