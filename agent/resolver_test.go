package main

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"
)

func TestResolveWithFallbackPrefersSystemResolver(t *testing.T) {
	fallbackCalled := false
	system := func(ctx context.Context, host string) ([]string, error) {
		return []string{"1.2.3.4"}, nil
	}
	fallback := func(ctx context.Context, host string) ([]string, error) {
		fallbackCalled = true
		return []string{"5.6.7.8"}, nil
	}

	ips, err := resolveWithFallback(context.Background(), "example.com", system, []lookupFunc{fallback})
	if err != nil {
		t.Fatalf("resolveWithFallback error: %v", err)
	}
	if len(ips) != 1 || ips[0] != "1.2.3.4" {
		t.Fatalf("ips = %v, want [1.2.3.4]", ips)
	}
	if fallbackCalled {
		t.Fatal("fallback should not be called when the system resolver succeeds")
	}
}

func TestResolveWithFallbackTriesFallbacksInOrder(t *testing.T) {
	system := func(ctx context.Context, host string) ([]string, error) {
		return nil, errors.New("system resolver down")
	}
	var calls []string
	fb1 := func(ctx context.Context, host string) ([]string, error) {
		calls = append(calls, "fb1")
		return nil, errors.New("fb1 failed")
	}
	fb2 := func(ctx context.Context, host string) ([]string, error) {
		calls = append(calls, "fb2")
		return []string{"9.9.9.9"}, nil
	}

	ips, err := resolveWithFallback(context.Background(), "example.com", system, []lookupFunc{fb1, fb2})
	if err != nil {
		t.Fatalf("resolveWithFallback error: %v", err)
	}
	if len(ips) != 1 || ips[0] != "9.9.9.9" {
		t.Fatalf("ips = %v, want [9.9.9.9]", ips)
	}
	if len(calls) != 2 || calls[0] != "fb1" || calls[1] != "fb2" {
		t.Fatalf("calls = %v, want [fb1 fb2]", calls)
	}
}

func TestResolveWithFallbackReturnsErrorWhenAllFail(t *testing.T) {
	system := func(ctx context.Context, host string) ([]string, error) {
		return nil, errors.New("system down")
	}
	fb := func(ctx context.Context, host string) ([]string, error) {
		return nil, errors.New("fallback down too")
	}

	_, err := resolveWithFallback(context.Background(), "example.com", system, []lookupFunc{fb})
	if err == nil {
		t.Fatal("expected an error when every resolver fails")
	}
}

// Regression test: a system resolver that hangs (rather than erroring
// promptly) must not be allowed to consume the entire parent context
// budget - otherwise the fallback DNS servers and the cached-IP dial never
// get a chance to run at all, which defeats the whole point of this file
// for exactly the failure mode it exists to handle (a DNS server that
// accepts the query but never answers).
func TestResolveWithFallbackBoundsHungSystemResolver(t *testing.T) {
	parentTimeout := 3 * lookupAttemptTimeout
	ctx, cancel := context.WithTimeout(context.Background(), parentTimeout)
	defer cancel()

	system := func(ctx context.Context, host string) ([]string, error) {
		<-ctx.Done() // simulate a resolver that hangs until its own deadline
		return nil, ctx.Err()
	}
	fallbackCalled := make(chan struct{}, 1)
	fallback := func(ctx context.Context, host string) ([]string, error) {
		fallbackCalled <- struct{}{}
		return []string{"9.9.9.9"}, nil
	}

	start := time.Now()
	ips, err := resolveWithFallback(ctx, "example.com", system, []lookupFunc{fallback})
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("resolveWithFallback error: %v", err)
	}
	if len(ips) != 1 || ips[0] != "9.9.9.9" {
		t.Fatalf("ips = %v, want [9.9.9.9]", ips)
	}
	select {
	case <-fallbackCalled:
	default:
		t.Fatal("fallback was never called - the hung system resolver consumed the whole budget")
	}
	if elapsed >= parentTimeout {
		t.Fatalf("resolveWithFallback took %v, want well under the parent timeout %v (system resolver must be bounded by lookupAttemptTimeout)", elapsed, parentTimeout)
	}
}

// Regression test: a resolver returning (nil, nil) - no error, no
// addresses - must not make resolveWithFallback return (nil, nil) too, or
// dialContext's "len(ips) == 0" fallback-to-cache branch would try to
// return a nil error alongside a nil connection.
func TestResolveWithFallbackReturnsNonNilErrorOnEmptyResult(t *testing.T) {
	empty := func(ctx context.Context, host string) ([]string, error) {
		return nil, nil
	}

	ips, err := resolveWithFallback(context.Background(), "example.com", empty, []lookupFunc{empty})
	if len(ips) != 0 {
		t.Fatalf("ips = %v, want empty", ips)
	}
	if err == nil {
		t.Fatal("expected a non-nil error when every resolver returns an empty result with no error")
	}
}

func TestDialContextFallsBackToCachedIPWhenResolutionFails(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	go func() {
		conn, err := ln.Accept()
		if err == nil {
			conn.Close()
		}
	}()

	_, port, _ := net.SplitHostPort(ln.Addr().String())

	cache := newIPCache()
	cache.set("example.com", "127.0.0.1")

	failing := func(ctx context.Context, host string) ([]string, error) {
		return nil, errors.New("resolution failed")
	}

	dial := dialContext(cache, failing, []lookupFunc{failing})
	conn, err := dial(context.Background(), "tcp", net.JoinHostPort("example.com", port))
	if err != nil {
		t.Fatalf("dial via cached IP failed: %v", err)
	}
	conn.Close()
}

func TestDialContextCachesSuccessfulIP(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	go func() {
		conn, err := ln.Accept()
		if err == nil {
			conn.Close()
		}
	}()

	_, port, _ := net.SplitHostPort(ln.Addr().String())

	cache := newIPCache()
	system := func(ctx context.Context, host string) ([]string, error) {
		return []string{"127.0.0.1"}, nil
	}

	dial := dialContext(cache, system, nil)
	conn, err := dial(context.Background(), "tcp", net.JoinHostPort("example.com", port))
	if err != nil {
		t.Fatalf("dial failed: %v", err)
	}
	conn.Close()

	if got := cache.get("example.com"); got != "127.0.0.1" {
		t.Fatalf("cached IP = %q, want 127.0.0.1", got)
	}
}

func TestDialContextReturnsErrorWithNoResolutionAndNoCache(t *testing.T) {
	cache := newIPCache()
	failing := func(ctx context.Context, host string) ([]string, error) {
		return nil, errors.New("no dns")
	}

	dial := dialContext(cache, failing, nil)
	_, err := dial(context.Background(), "tcp", "example.com:443")
	if err == nil {
		t.Fatal("expected an error when resolution fails and no cached IP exists")
	}
}
