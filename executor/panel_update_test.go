package executor

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCompareVersions(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"v1.2.3", "v1.2.3", 0},
		{"v1.2.4", "v1.2.3", 1},
		{"v1.2.3", "v1.2.4", -1},
		{"v1.3.0", "v1.2.9", 1},
		{"v2.0.0", "v1.9.9", 1},
		{"v1.2.3", "v1.2.3-rc.1", 1},
		{"v1.2.3-rc.1", "v1.2.3", -1},
		{"v1.2.3-rc.2", "v1.2.3-rc.1", 1},
		{"v1.2.3-alpha", "v1.2.3-beta", -1},
		{"v1.2.3-alpha.1", "v1.2.3-alpha", 1},
		{"1.2.3", "v1.2.3", 0}, // leading "v" optional
	}
	for _, c := range cases {
		if got := CompareVersions(c.a, c.b); got != c.want {
			t.Errorf("CompareVersions(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestIsStableVersion(t *testing.T) {
	cases := []struct {
		tag  string
		want bool
	}{
		{"v1.2.3", true},
		{"1.2.3", true},
		{"v1.2.3-rc.1", false},
		{"v1.2", false},
		{"v1.2.3.4", false},
		{"vX.Y.Z", false},
	}
	for _, c := range cases {
		if got := IsStableVersion(c.tag); got != c.want {
			t.Errorf("IsStableVersion(%q) = %v, want %v", c.tag, got, c.want)
		}
	}
}

func TestIsPatchBump(t *testing.T) {
	cases := []struct {
		current, latest string
		want            bool
	}{
		{"v1.2.3", "v1.2.4", true},
		{"v1.2.3", "v1.3.0", false},
		{"v1.2.3", "v2.0.0", false},
		{"v1.2.3", "v1.2.3", false},
		{"v1.2.9", "v1.2.10", true},
	}
	for _, c := range cases {
		if got := IsPatchBump(c.current, c.latest); got != c.want {
			t.Errorf("IsPatchBump(%q, %q) = %v, want %v", c.current, c.latest, got, c.want)
		}
	}
}

func TestPanelAssetNames(t *testing.T) {
	binary, checksums, sig, err := PanelAssetNames()
	if err != nil {
		t.Fatalf("PanelAssetNames() error: %v", err)
	}
	if binary == "" || checksums == "" || sig == "" {
		t.Fatalf("PanelAssetNames() returned empty names: %q %q %q", binary, checksums, sig)
	}
	if checksums+".sig" != sig {
		t.Errorf("signature name %q should be checksums name %q + .sig", sig, checksums)
	}
}

func TestFindChecksumForFile(t *testing.T) {
	content := "abc123  server-panel-linux-amd64\ndef456  server-panel-agent-linux-amd64\n"
	hash, err := findChecksumForFile(content, "server-panel-linux-amd64")
	if err != nil {
		t.Fatalf("findChecksumForFile() error: %v", err)
	}
	if hash != "abc123" {
		t.Errorf("hash = %q, want abc123", hash)
	}

	if _, err := findChecksumForFile(content, "does-not-exist"); err == nil {
		t.Error("expected error for missing filename, got nil")
	}
}

func TestWithinAutoUpdateWindow(t *testing.T) {
	if !withinAutoUpdateWindow("not-a-window") {
		t.Error("malformed window should default to allowed (true)")
	}
}

func TestParseClock(t *testing.T) {
	cases := []struct {
		in      string
		wantMin int
		wantOk  bool
	}{
		{"03:00", 180, true},
		{"23:59", 1439, true},
		{"24:00", 0, false},
		{"bad", 0, false},
	}
	for _, c := range cases {
		got, ok := parseClock(c.in)
		if ok != c.wantOk || (ok && got != c.wantMin) {
			t.Errorf("parseClock(%q) = (%d, %v), want (%d, %v)", c.in, got, ok, c.wantMin, c.wantOk)
		}
	}
}

func TestBinarySupportsWatchdog(t *testing.T) {
	dir := t.TempDir()
	withWatchdog := filepath.Join(dir, "with-watchdog")
	withoutWatchdog := filepath.Join(dir, "without-watchdog")

	if err := os.WriteFile(withWatchdog, []byte("#!/bin/sh\nprintf '%s\\n' 'Usage: server-panel --update-watchdog plan'\n"), 0755); err != nil {
		t.Fatalf("write with-watchdog: %v", err)
	}
	if err := os.WriteFile(withoutWatchdog, []byte("#!/bin/sh\nprintf '%s\\n' 'Usage: server-panel --config path'\n"), 0755); err != nil {
		t.Fatalf("write without-watchdog: %v", err)
	}

	if !binarySupportsWatchdog(withWatchdog) {
		t.Fatal("expected with-watchdog helper to support watchdog")
	}
	if binarySupportsWatchdog(withoutWatchdog) {
		t.Fatal("expected without-watchdog helper to not support watchdog")
	}
}
