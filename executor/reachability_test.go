package executor

import (
	"net"
	"testing"
)

func TestIsReachableSucceedsOnWebPort(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	go acceptAndClose(ln)

	_, portStr, _ := net.SplitHostPort(ln.Addr().String())
	port := mustAtoi(t, portStr)

	orig := reachabilityWebPorts
	reachabilityWebPorts = []int{port}
	defer func() { reachabilityWebPorts = orig }()

	if !isReachable("127.0.0.1", 22) {
		t.Fatal("expected isReachable to succeed via the listening web port")
	}
}

func TestIsReachableFallsBackToSSHPort(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	go acceptAndClose(ln)

	_, portStr, _ := net.SplitHostPort(ln.Addr().String())
	sshPort := mustAtoi(t, portStr)

	orig := reachabilityWebPorts
	reachabilityWebPorts = []int{1} // nothing listens on port 1
	defer func() { reachabilityWebPorts = orig }()

	if !isReachable("127.0.0.1", sshPort) {
		t.Fatal("expected isReachable to fall back to the SSH port when web ports are closed")
	}
}

func TestIsReachableFailsWhenNothingListens(t *testing.T) {
	orig := reachabilityWebPorts
	reachabilityWebPorts = []int{1}
	defer func() { reachabilityWebPorts = orig }()

	if isReachable("127.0.0.1", 2) {
		t.Fatal("expected isReachable to fail when no port accepts a connection")
	}
}

func TestProbeReachabilityUpdatesServerRow(t *testing.T) {
	db := newAlertTestDB(t)
	execAlertSQL(t, db, `ALTER TABLE servers ADD COLUMN ip_address TEXT NOT NULL DEFAULT ''`)
	execAlertSQL(t, db, `ALTER TABLE servers ADD COLUMN ssh_port INTEGER NOT NULL DEFAULT 22`)
	execAlertSQL(t, db, `INSERT INTO servers (id, name, status, ip_address, ssh_port) VALUES (1, 'srv-1', 'active', '127.0.0.1', 22)`)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	go acceptAndClose(ln)

	_, portStr, _ := net.SplitHostPort(ln.Addr().String())
	port := mustAtoi(t, portStr)

	orig := reachabilityWebPorts
	reachabilityWebPorts = []int{port}
	defer func() { reachabilityWebPorts = orig }()

	probeReachability(db, []reachabilityTarget{{id: 1, ip: "127.0.0.1", sshPort: 22}})

	var reachable int
	var checkedAt string
	if err := db.QueryRow(`SELECT tcp_reachable, tcp_reachable_checked_at FROM servers WHERE id = 1`).Scan(&reachable, &checkedAt); err != nil {
		t.Fatalf("query: %v", err)
	}
	if reachable != 1 {
		t.Fatalf("tcp_reachable = %d, want 1", reachable)
	}
	if checkedAt == "" {
		t.Fatal("tcp_reachable_checked_at was not set")
	}
}

func acceptAndClose(ln net.Listener) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		conn.Close()
	}
}

func mustAtoi(t *testing.T, s string) int {
	t.Helper()
	var n int
	for _, c := range s {
		if c < '0' || c > '9' {
			t.Fatalf("not a number: %q", s)
		}
		n = n*10 + int(c-'0')
	}
	return n
}
