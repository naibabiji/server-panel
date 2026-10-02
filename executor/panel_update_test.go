package executor

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/naibabiji/server-panel/config"
)

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

func TestHealthURLUsesACMEDomainForSNI(t *testing.T) {
	cfg := &config.Config{}
	cfg.Panel.TLSPort = 443
	cfg.Panel.TLSMode = "acme"
	cfg.Panel.Domain = "panel.example.com"
	if got := healthURL(cfg); got != "https://panel.example.com:443/healthz" {
		t.Fatalf("healthURL() = %q", got)
	}
	if got := healthDialAddress(cfg); got != "127.0.0.1:443" {
		t.Fatalf("healthDialAddress() = %q", got)
	}
}

func TestHealthURLUsesLoopbackForNonACME(t *testing.T) {
	cfg := &config.Config{}
	cfg.Panel.TLSPort = 8444
	cfg.Panel.TLSMode = "self_signed"
	if got := healthURL(cfg); got != "https://127.0.0.1:8444/healthz" {
		t.Fatalf("healthURL() = %q", got)
	}
}
