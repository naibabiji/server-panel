package executor

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// Regression test for the "site is actually up but the panel reports HTTP
// 探测异常" false-positive: a single slow/failed response within one probe
// cycle must not immediately flip the result to unhealthy - only running
// out of retries should.
func TestCheckDomainRecoversFromATransientFailureWithinOneCycle(t *testing.T) {
	t.Parallel()

	var attempts int32
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&attempts, 1) <= 2 {
			// Simulate a slow/failed response by never writing headers
			// until the client gives up (short timeout below forces this
			// quickly instead of actually sleeping past a real deadline).
			<-r.Context().Done()
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	domain := strings.TrimPrefix(srv.URL, "https://")
	healthy, errMsg := checkDomainWithRetryDelay(domain, 1, 10*time.Millisecond)
	if !healthy {
		t.Fatalf("checkDomain() healthy = false (err=%q), want true after recovering within retries", errMsg)
	}
	if got := atomic.LoadInt32(&attempts); got < 3 {
		t.Fatalf("attempts = %d, want at least 3 (2 failures + 1 success)", got)
	}
}

// A site that fails every attempt in the cycle must still end up reported
// unhealthy - retries dampen false positives, they don't mask a real outage.
func TestCheckDomainStillReportsUnhealthyAfterExhaustingRetries(t *testing.T) {
	t.Parallel()

	var attempts int32
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&attempts, 1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	domain := strings.TrimPrefix(srv.URL, "https://")
	healthy, errMsg := checkDomainWithRetryDelay(domain, 2, 10*time.Millisecond)
	if healthy {
		t.Fatal("checkDomain() healthy = true, want false for a site that fails every attempt")
	}
	if errMsg != "HTTP 500" {
		t.Fatalf("errMsg = %q, want %q", errMsg, "HTTP 500")
	}
	if got := atomic.LoadInt32(&attempts); got != probeRetryAttempts {
		t.Fatalf("attempts = %d, want exactly %d", got, probeRetryAttempts)
	}
}

func TestCheckDomainSucceedsImmediatelyWithoutRetryingOnFirstSuccess(t *testing.T) {
	t.Parallel()

	var attempts int32
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&attempts, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	domain := strings.TrimPrefix(srv.URL, "https://")
	healthy, _ := checkDomainWithRetryDelay(domain, 2, 10*time.Millisecond)
	if !healthy {
		t.Fatal("checkDomain() healthy = false, want true")
	}
	if got := atomic.LoadInt32(&attempts); got != 1 {
		t.Fatalf("attempts = %d, want 1 (should not retry after an immediate success)", got)
	}
}

// Regression test for the serial-probing pile-up risk: several genuinely
// unreachable sites in one probe round must not make the whole round take
// roughly (number of down sites) * (single check's worst-case time). Bounded
// concurrency should keep the round close to one check's worst-case time
// regardless of how many sites are down at once (as long as it's within
// probeConcurrency).
func TestProbeServersRunsChecksConcurrentlyNotSerially(t *testing.T) {
	t.Parallel()

	const downSites = 4
	perCheckDelay := 300 * time.Millisecond

	// mode=memory&cache=shared (not bare ":memory:"): a plain in-memory DSN
	// gives every pooled connection its own private database, so writes
	// from one goroutine's connection wouldn't be visible when another
	// connection reads it back - exactly the concurrency this test exercises.
	// The name is derived from t.Name() (matching the convention already
	// used in newAlertTestDB in this package) so this shared-cache database
	// can never collide with one from another test/run.
	dbName := strings.NewReplacer("/", "-", " ", "-", ":", "-").Replace(t.Name())
	db, err := sql.Open("sqlite", "file:"+dbName+"?mode=memory&cache=shared")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	execAlertSQL(t, db, `CREATE TABLE servers (
		id                     INTEGER PRIMARY KEY,
		http_probe_healthy     INTEGER,
		http_probe_last_at     TEXT,
		http_probe_last_error  TEXT NOT NULL DEFAULT ''
	)`)
	execAlertSQL(t, db, `CREATE TABLE websites (
		id        INTEGER PRIMARY KEY,
		server_id INTEGER NOT NULL,
		domain    TEXT NOT NULL,
		status    TEXT NOT NULL DEFAULT 'active'
	)`)

	var hits int32
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		time.Sleep(perCheckDelay)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	domain := strings.TrimPrefix(srv.URL, "https://")

	var serverIDs []int64
	for i := int64(1); i <= downSites; i++ {
		execAlertSQL(t, db, `INSERT INTO servers (id) VALUES (?)`, i)
		execAlertSQL(t, db, `INSERT INTO websites (server_id, domain) VALUES (?, ?)`, i, domain)
		serverIDs = append(serverIDs, i)
	}

	start := time.Now()
	probeServers(db, serverIDs, 5)
	elapsed := time.Since(start)

	if got := atomic.LoadInt32(&hits); got != downSites {
		t.Fatalf("hits = %d, want %d", got, downSites)
	}
	// Serial would take ~downSites*perCheckDelay (~1.2s); concurrent
	// should stay close to one perCheckDelay. Generous upper bound (3x
	// plus a fixed cushion) to leave room for goroutine scheduling/race
	// detector overhead on a loaded CI box while still catching a
	// regression to serial (which would be ~4x, not just slow).
	if elapsed > perCheckDelay*3+200*time.Millisecond {
		t.Fatalf("elapsed = %v, want well under serial time (~%v) - probing does not appear concurrent", elapsed, perCheckDelay*downSites)
	}

	var healthyCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM servers WHERE http_probe_healthy = 1`).Scan(&healthyCount); err != nil {
		t.Fatalf("count healthy: %v", err)
	}
	if healthyCount != downSites {
		t.Fatalf("healthy servers = %d, want %d (all should have been probed and updated)", healthyCount, downSites)
	}
}
