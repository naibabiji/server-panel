package middleware

import (
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

// MaxBodyBytes rejects request bodies larger than n. Agent payloads are a
// small fixed set of numeric fields; there's no legitimate reason for one to
// approach this size, so it costs nothing while bounding how much memory a
// single request (authenticated or not) can force the server to allocate.
func MaxBodyBytes(n int64) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, n)
		c.Next()
	}
}

type agentRateLimiter struct {
	mu       sync.Mutex
	limit    int
	window   time.Duration
	requests map[int64][]time.Time
}

func newAgentRateLimiter(limit int, window time.Duration) *agentRateLimiter {
	return &agentRateLimiter{limit: limit, window: window, requests: make(map[int64][]time.Time)}
}

func (l *agentRateLimiter) allow(serverID int64) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now()
	cutoff := now.Add(-l.window)
	kept := l.requests[serverID][:0]
	for _, t := range l.requests[serverID] {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	if len(kept) >= l.limit {
		l.requests[serverID] = kept
		return false
	}
	l.requests[serverID] = append(kept, now)
	return true
}

// globalAgentRateLimiter caps requests per already-authenticated server ID.
// A legitimate agent reports once per its configured interval (default 60s)
// with up to 3 retries on failure, so this leaves generous headroom while
// still bounding what a leaked/replayed API key can do.
var globalAgentRateLimiter = newAgentRateLimiter(20, time.Minute)

// AgentRateLimit must run after AgentAuth, which sets agent_server_id.
func AgentRateLimit() gin.HandlerFunc {
	return func(c *gin.Context) {
		v, ok := c.Get("agent_server_id")
		if !ok {
			c.Next()
			return
		}
		serverID, ok := v.(int64)
		if !ok {
			c.Next()
			return
		}
		if !globalAgentRateLimiter.allow(serverID) {
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
				"success": false,
				"message": "请求过于频繁",
			})
			return
		}
		c.Next()
	}
}
