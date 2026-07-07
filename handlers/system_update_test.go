package handlers

import "testing"

func TestParseAptUpgradable(t *testing.T) {
	output := `Listing...
curl/noble-security 8.5.0-2ubuntu10.6 amd64 [upgradable from: 8.5.0-2ubuntu10.5]
openssl/noble-updates 3.0.13-0ubuntu3.4 amd64 [upgradable from: 3.0.13-0ubuntu3.3]

`
	pkgs := parseAptUpgradable(output)
	if len(pkgs) != 2 {
		t.Fatalf("got %d packages, want 2: %#v", len(pkgs), pkgs)
	}
	if pkgs[0].Name != "curl" || pkgs[0].Repo != "noble-security" || pkgs[0].Version != "8.5.0-2ubuntu10.6" {
		t.Errorf("pkgs[0] = %#v, unexpected", pkgs[0])
	}
	if pkgs[1].Name != "openssl" || pkgs[1].Repo != "noble-updates" {
		t.Errorf("pkgs[1] = %#v, unexpected", pkgs[1])
	}
}

func TestParseAptUpgradableEmpty(t *testing.T) {
	if pkgs := parseAptUpgradable("Listing...\n"); len(pkgs) != 0 {
		t.Errorf("expected no packages, got %#v", pkgs)
	}
	if pkgs := parseAptUpgradable(""); len(pkgs) != 0 {
		t.Errorf("expected no packages for empty input, got %#v", pkgs)
	}
}

func TestLimitedOutput(t *testing.T) {
	var out limitedOutput
	out.limit = 5
	if n, err := out.Write([]byte("hello world")); err != nil || n != len("hello world") {
		t.Fatalf("Write returned (%d, %v)", n, err)
	}
	got := out.String()
	if got != "hello\n...[output truncated]" {
		t.Fatalf("limited output = %q", got)
	}
}

func TestLimitedOutputUnlimitedAndDiscard(t *testing.T) {
	unlimited := limitedOutput{limit: -1}
	_, _ = unlimited.Write([]byte("hello"))
	_, _ = unlimited.Write([]byte(" world"))
	if got := unlimited.String(); got != "hello world" {
		t.Fatalf("unlimited output = %q", got)
	}

	discard := limitedOutput{limit: 0}
	_, _ = discard.Write([]byte("discard me"))
	if got := discard.String(); got != "" {
		t.Fatalf("discard output = %q", got)
	}
}
