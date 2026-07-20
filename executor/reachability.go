package executor

import (
	"database/sql"
	"log"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/naibabiji/server-panel/database"
	"github.com/naibabiji/server-panel/timeutil"
)

// reachabilityWebPorts are tried before ssh_port when checking whether a
// server is reachable at the network level despite its Agent having gone
// silent: they're what the server is actually there to serve, and open on
// nearly every monitored box, so they're checked first; SSH is the
// fallback for servers running no web service on 80/443. The first port
// that accepts a TCP connection marks the server reachable - this lets
// checkOfflineAlerts (alert_checker.go) tell apart "the Agent/DNS/network
// on that box broke, but the server itself is fine" from "the server is
// actually unreachable", which were previously indistinguishable.
var reachabilityWebPorts = []int{80, 443}

const reachabilityDialTimeout = 3 * time.Second
const reachabilityConcurrency = 8

// StartReachabilityChecker periodically probes, at the TCP level, every
// active server whose Agent heartbeat has gone stale (is_online = 0).
func StartReachabilityChecker(interval time.Duration) {
	go func() {
		for {
			time.Sleep(interval)
			checkReachability()
		}
	}()
}

type reachabilityTarget struct {
	id      int64
	ip      string
	sshPort int
}

func checkReachability() {
	db := database.GetDB()
	if db == nil {
		return
	}

	rows, err := db.Query(
		`SELECT id, ip_address, ssh_port FROM servers
		 WHERE is_online = 0 AND status = 'active' AND ip_address != ''
		 AND (agent_version != '' OR last_seen_at IS NOT NULL)`)
	if err != nil {
		log.Printf("reachability checker query failed: %v", err)
		return
	}

	var targets []reachabilityTarget
	for rows.Next() {
		var t reachabilityTarget
		if err := rows.Scan(&t.id, &t.ip, &t.sshPort); err == nil {
			targets = append(targets, t)
		}
	}
	rows.Close()

	probeReachability(db, targets)
}

func probeReachability(db *sql.DB, targets []reachabilityTarget) {
	now := timeutil.NowDisplay()
	sem := make(chan struct{}, reachabilityConcurrency)
	var wg sync.WaitGroup

	for _, t := range targets {
		wg.Add(1)
		sem <- struct{}{}
		go func(t reachabilityTarget) {
			defer wg.Done()
			defer func() { <-sem }()

			reachable := 0
			if isReachable(t.ip, t.sshPort) {
				reachable = 1
			}
			// Guarded on is_online = 0: if the Agent reported back in while
			// this probe was in flight, the server is no longer part of the
			// situation this check exists for, and a stale TCP verdict
			// shouldn't be written over its (now irrelevant) offline state.
			if _, err := db.Exec(
				`UPDATE servers SET tcp_reachable = ?, tcp_reachable_checked_at = ? WHERE id = ? AND is_online = 0`,
				reachable, now, t.id); err != nil {
				log.Printf("reachability checker update failed: server_id=%d: %v", t.id, err)
			}
		}(t)
	}
	wg.Wait()
}

// isReachable tries the web ports first, falling back to sshPort (default
// 22) only when neither answers.
func isReachable(ip string, sshPort int) bool {
	for _, port := range reachabilityWebPorts {
		if dialSucceeds(ip, port) {
			return true
		}
	}
	if sshPort <= 0 {
		sshPort = 22
	}
	return dialSucceeds(ip, sshPort)
}

func dialSucceeds(ip string, port int) bool {
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(ip, strconv.Itoa(port)), reachabilityDialTimeout)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}
