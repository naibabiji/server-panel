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
//
// A request with an honest Content-Length over the cap is rejected here,
// before it reaches AgentAuth or the handler. A request that lies about (or
// omits) Content-Length still gets stopped by the wrapped MaxBytesReader
// once the handler actually reads the body, just later and with a generic
// 400 from the JSON decoder instead of this handler's 413.
func MaxBodyBytes(n int64) gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.ContentLength > n {
			c.AbortWithStatusJSON(http.StatusRequestEntityTooLarge, gin.H{
				"success": false,
				"message": "请求体过大",
			})
			return
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, n)
		c.Next()
	}
}

type agentRateLimiter struct {
	mu       sync.Mutex
	limit    int
	window   time.Duration
	requests map[string][]time.Time
}

func newAgentRateLimiter(limit int, window time.Duration) *agentRateLimiter {
	return &agentRateLimiter{limit: limit, window: window, requests: make(map[string][]time.Time)}
}

func (l *agentRateLimiter) allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now()
	cutoff := now.Add(-l.window)
	kept := l.requests[key][:0]
	for _, t := range l.requests[key] {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	if len(kept) >= l.limit {
		l.requests[key] = kept
		return false
	}
	l.requests[key] = append(kept, now)
	return true
}

// globalAgentRateLimiter caps requests per resolved server ID, checked in
// AgentAuth after the API key has been validated against the DB but before
// the last_seen_at/is_online write. Keying by server ID (rather than the raw
// header value) keeps the map bounded by the number of servers actually
// registered, instead of growing once per distinct key an attacker sends. A
// legitimate agent reports once per its configured interval (default 60s)
// with up to 3 retries on failure, so this leaves generous headroom while
// still bounding what a leaked/replayed API key can do.
var globalAgentRateLimiter = newAgentRateLimiter(20, time.Minute)
