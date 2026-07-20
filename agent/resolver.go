package main

import (
	"context"
	"fmt"
	"net"
	"sync"
	"time"
)

// fallbackDNSServers are tried, in order, when the system resolver (whatever
// /etc/resolv.conf currently points at) fails to resolve the panel's
// hostname. A VPS's assigned DNS resolver can go bad (timeouts, connection
// refused) while the box's outbound network and the panel itself are both
// completely fine - the agent would otherwise sit there failing every report
// forever, making a healthy server look "offline" in the panel.
var fallbackDNSServers = []string{"1.1.1.1:53", "8.8.8.8:53"}

// lookupAttemptTimeout bounds a single resolver attempt - system resolver
// included. Without this, a hung system resolver (exactly the failure mode
// this file exists to work around: a VPS's DNS server accepting the query
// but never answering) would sit on the request's full context deadline
// and leave zero time for the public-DNS fallbacks or the cached-IP dial to
// ever run.
const lookupAttemptTimeout = 4 * time.Second

// lookupFunc resolves a hostname to a set of IPs. It's the seam tests use to
// replace real DNS/network calls with deterministic fakes.
type lookupFunc func(ctx context.Context, host string) ([]string, error)

func systemLookup(ctx context.Context, host string) ([]string, error) {
	return net.DefaultResolver.LookupHost(ctx, host)
}

// publicDNSLookup builds a lookupFunc that queries a single DNS server
// directly over UDP, bypassing the system resolver entirely.
func publicDNSLookup(server string) lookupFunc {
	return func(ctx context.Context, host string) ([]string, error) {
		r := &net.Resolver{
			PreferGo: true,
			Dial: func(ctx context.Context, _, _ string) (net.Conn, error) {
				d := net.Dialer{Timeout: lookupAttemptTimeout}
				return d.DialContext(ctx, "udp", server)
			},
		}
		return r.LookupHost(ctx, host)
	}
}

func defaultFallbackLookups() []lookupFunc {
	fbs := make([]lookupFunc, len(fallbackDNSServers))
	for i, server := range fallbackDNSServers {
		fbs[i] = publicDNSLookup(server)
	}
	return fbs
}

// resolveWithFallback tries system first, then each fallback lookup in
// order, returning the first non-empty result. Every attempt (system
// included) is bounded by lookupAttemptTimeout so a single hung resolver
// can't consume the whole budget and starve the others. The returned error
// is always non-nil when ips is empty, even if every attempt happened to
// return (nil, nil) - callers (dialContext) rely on that to know whether a
// cached IP fallback is actually needed.
func resolveWithFallback(ctx context.Context, host string, system lookupFunc, fallbacks []lookupFunc) ([]string, error) {
	lastErr := fmt.Errorf("no resolver returned an address for %q", host)

	sctx, cancel := context.WithTimeout(ctx, lookupAttemptTimeout)
	ips, err := system(sctx, host)
	cancel()
	if err == nil && len(ips) > 0 {
		return ips, nil
	}
	if err != nil {
		lastErr = err
	}

	for _, fb := range fallbacks {
		fctx, cancel := context.WithTimeout(ctx, lookupAttemptTimeout)
		ips, ferr := fb(fctx, host)
		cancel()
		if ferr == nil && len(ips) > 0 {
			return ips, nil
		}
		if ferr != nil {
			lastErr = ferr
		}
	}
	return nil, lastErr
}

// ipCache remembers, per host, the last IP a TCP dial actually succeeded
// against (not necessarily a full successful report - TLS/HTTP happen
// after dialContext returns, so a cached IP could in principle accept TCP
// but fail TLS) - used as a last-resort fallback for a dial attempt where
// every resolver (system and public) fails outright. Even an unverified-
// past-the-TCP-layer address is strictly better than giving up the report
// entirely when DNS is completely broken.
type ipCache struct {
	mu sync.Mutex
	ip map[string]string
}

func newIPCache() *ipCache { return &ipCache{ip: make(map[string]string)} }

func (c *ipCache) get(host string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ip[host]
}

func (c *ipCache) set(host, ip string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ip[host] = ip
}

// globalIPCache persists for the life of the agent process.
var globalIPCache = newIPCache()

// dialContext builds an http.Transport-compatible DialContext that resolves
// the target host via resolveWithFallback, falls back to cache's last-known
// address if every resolver fails, and records whichever address the
// connection actually succeeds on.
func dialContext(cache *ipCache, system lookupFunc, fallbacks []lookupFunc) func(ctx context.Context, network, addr string) (net.Conn, error) {
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, err
		}

		ips, resolveErr := resolveWithFallback(ctx, host, system, fallbacks)
		if len(ips) == 0 {
			cached := cache.get(host)
			if cached == "" {
				return nil, resolveErr
			}
			ips = []string{cached}
		}

		var lastErr error
		for _, ip := range ips {
			conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(ip, port))
			if err == nil {
				cache.set(host, ip)
				return conn, nil
			}
			lastErr = err
		}
		return nil, lastErr
	}
}

// resilientDialContext is what reporter.go wires into its http.Transport:
// the real system resolver, real public-DNS fallbacks, and the
// process-lifetime IP cache.
func resilientDialContext() func(ctx context.Context, network, addr string) (net.Conn, error) {
	return dialContext(globalIPCache, systemLookup, defaultFallbackLookups())
}
