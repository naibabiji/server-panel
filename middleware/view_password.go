package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

func ViewPasswordRequired() gin.HandlerFunc {
	return func(c *gin.Context) {
		unlocked, _ := c.Get("view_password_unlocked")
		if unlocked == nil || unlocked != true {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"success": false,
				"message": "请先输入查看密码",
			})
			return
		}
		c.Next()
	}
}
