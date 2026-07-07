package middleware

import (
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
)

func AgentAuth(db *sql.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		apiKey := c.GetHeader("X-Agent-API-Key")
		if apiKey == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"success": false,
				"message": "Missing API key",
			})
			return
		}

		hash := sha256.Sum256([]byte(apiKey))
		hashStr := hex.EncodeToString(hash[:])

		var serverID int64
		err := db.QueryRow(
			"SELECT id FROM servers WHERE agent_api_key_hash = ? AND agent_api_key_hash <> ''",
			hashStr,
		).Scan(&serverID)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"success": false,
				"message": "Invalid API key",
			})
			return
		}

		// Rate-limit on the resolved server ID, not the raw header value:
		// keying by whatever a client sends before it's validated lets an
		// attacker force one new rate-limiter map entry per garbage key,
		// which is unbounded. Keying by server ID keeps the limiter's
		// memory bounded by the number of servers actually registered in
		// the DB, and still stops a flood of requests using the same valid
		// key from writing last_seen_at/is_online below.
		if !globalAgentRateLimiter.allow(strconv.FormatInt(serverID, 10)) {
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
				"success": false,
				"message": "请求过于频繁",
			})
			return
		}

		now := time.Now().UTC().Format("2006-01-02 15:04:05")
		_, _ = db.Exec(
			"UPDATE servers SET last_seen_at = ?, is_online = 1 WHERE id = ?",
			now, serverID,
		)

		c.Set("agent_server_id", serverID)
		c.Set("agent_request_time", time.Now())
		c.Next()
	}
}

// ConstantTimeCompare 用于额外需要常数时间比较的场景
func ConstantTimeCompare(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
