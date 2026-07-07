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

// cleanup drops map entries whose timestamps have all aged out of the
// window. allow() never removes a key by itself (it only trims each key's
// slice), so a limiter keyed by something an external party can vary - a
// client IP, for instance - needs this run periodically or the map grows
// for as long as the process is up.
func (l *agentRateLimiter) cleanup() {
	l.mu.Lock()
	defer l.mu.Unlock()

	cutoff := time.Now().Add(-l.window)
	for key, times := range l.requests {
		kept := times[:0]
		for _, t := range times {
			if t.After(cutoff) {
				kept = append(kept, t)
			}
		}
		if len(kept) == 0 {
			delete(l.requests, key)
		} else {
			l.requests[key] = kept
		}
	}
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

// globalAgentIPRateLimiter caps requests per client IP before AgentAuth runs
// at all, so a flood of requests carrying invalid/missing keys - which never
// reach globalAgentRateLimiter, since that's checked only after a key
// resolves to a server - can't force a DB lookup per request either. 60/min
// is far above what any real agent (or several agents behind a shared NAT/
// proxy IP) would ever send, so this is a coarse backstop, not a precise
// per-identity limit; that's what globalAgentRateLimiter is for. Its key
// space (client IPs) isn't bounded by our own data the way server IDs are,
// so it's swept by StartAgentRateLimiterCleanup instead of relying on the
// same eventual-reuse assumption as the server-ID limiter.
var globalAgentIPRateLimiter = newAgentRateLimiter(60, time.Minute)

// AgentIPRateLimit must run before AgentAuth.
func AgentIPRateLimit() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !globalAgentIPRateLimiter.allow(ClientIP(c)) {
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
				"success": false,
				"message": "请求过于频繁",
			})
			return
		}
		c.Next()
	}
}

// StartAgentRateLimiterCleanup periodically evicts aged-out entries from
// both agent rate limiters. Required for globalAgentIPRateLimiter, whose
// keys (client IPs) aren't bounded by our own data; harmless no-op work for
// globalAgentRateLimiter, whose keys are already bounded by server count.
func StartAgentRateLimiterCleanup(interval time.Duration) {
	go func() {
		for {
			time.Sleep(interval)
			globalAgentRateLimiter.cleanup()
			globalAgentIPRateLimiter.cleanup()
		}
	}()
}
