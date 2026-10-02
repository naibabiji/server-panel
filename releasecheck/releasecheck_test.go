package releasecheck

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// The .sig format must match tools/sign-checksums: base64 text of the raw
// Ed25519 signature (the Agent updater once verified the text as raw bytes,
// which would have rejected every real release).
func TestVerifySignatureFileUsesSignToolFormat(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	msg := []byte("abc  server-panel-agent-linux-amd64\n")
	sig := ed25519.Sign(priv, msg)
	sigFile := []byte(base64.StdEncoding.EncodeToString(sig) + "\n")

	if err := verifySignatureFile(pub, msg, sigFile); err != nil {
		t.Fatalf("valid sig file rejected: %v", err)
	}
	if err := verifySignatureFile(pub, append(msg, 'x'), sigFile); !errors.Is(err, ErrSignatureMismatch) {
		t.Fatalf("tampered message: err = %v, want ErrSignatureMismatch", err)
	}
	if err := verifySignatureFile(pub, msg, sig); !errors.Is(err, ErrSignatureFormat) && !errors.Is(err, ErrSignatureMismatch) {
		t.Fatalf("raw signature bytes: err = %v, want rejection", err)
	}
	if err := VerifySignatureFile(msg, sigFile); !errors.Is(err, ErrSignatureMismatch) {
		t.Fatalf("embedded key accepted a signature from another key: %v", err)
	}
}

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

func TestChecksumFor(t *testing.T) {
	content := "abc123  server-panel-linux-amd64\ndef456  server-panel-agent-linux-amd64\n"
	hash, err := ChecksumFor(content, "server-panel-linux-amd64")
	if err != nil {
		t.Fatalf("ChecksumFor() error: %v", err)
	}
	if hash != "abc123" {
		t.Errorf("hash = %q, want abc123", hash)
	}

	if _, err := ChecksumFor(content, "does-not-exist"); err == nil {
		t.Error("expected error for missing filename, got nil")
	}
}

func TestReleaseRedirectAllowed(t *testing.T) {
	cases := []struct {
		url, proxyHost string
		want           bool
	}{
		{"https://github.com/naibabiji/server-panel/releases/download/v1/x", "", true},
		{"https://objects.githubusercontent.com/x", "", true},
		{"https://release-assets.githubusercontent.com/x", "", true},
		{"http://objects.githubusercontent.com/x", "", false},
		{"http://169.254.169.254/latest/meta-data", "", false},
		{"https://127.0.0.1/x", "", false},
		{"https://evilgithubusercontent.com/x", "", false},
		{"https://gh.example.com/https://github.com/x", "gh.example.com", true},
		{"http://169.254.169.254/x", "gh.example.com", false},
	}
	for _, c := range cases {
		u, err := url.Parse(c.url)
		if err != nil {
			t.Fatal(err)
		}
		if got := redirectAllowed(u, c.proxyHost); got != c.want {
			t.Errorf("redirectAllowed(%q, %q) = %v, want %v", c.url, c.proxyHost, got, c.want)
		}
	}
}

func TestIsPublicIP(t *testing.T) {
	for _, c := range []struct {
		ip   string
		want bool
	}{
		{"1.1.1.1", true}, {"140.82.112.3", true}, {"2606:4700::1111", true},
		{"127.0.0.1", false}, {"::1", false}, {"10.1.2.3", false}, {"172.16.0.1", false},
		{"192.168.1.1", false}, {"169.254.169.254", false}, {"fe80::1", false}, {"fd00::1", false},
		{"100.64.0.1", false}, {"0.0.0.0", false}, {"0.1.2.3", false}, {"224.0.0.1", false},
		{"255.255.255.255", false}, {"::ffff:127.0.0.1", false}, {"::", false},
		{"64:ff9b::7f00:1", false}, {"64:ff9b::a9fe:a9fe", false}, {"64:ff9b::101:101", true},
		{"2002:c0a8:0101::1", false}, {"2002:0101:0101::1", true},
		{"192.0.2.10", false}, {"198.18.0.1", false}, {"198.51.100.1", false}, {"203.0.113.9", false},
		{"240.0.0.1", false}, {"192.0.0.8", false}, {"2001:db8::1", false}, {"2001:0:4136:e378::1", false},
		{"2a00:1450:4001::200e", true},
	} {
		if got := IsPublicIP(net.ParseIP(c.ip)); got != c.want {
			t.Errorf("IsPublicIP(%s) = %v, want %v", c.ip, got, c.want)
		}
	}
}

// The check runs on the dialed IP, so a URL whose host is loopback (or a
// name resolving to it) never gets a connection.
func TestPublicTransportRefusesLoopback(t *testing.T) {
	hit := false
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hit = true }))
	defer srv.Close()
	fetch := NewFetcher("", PublicTransport(), 5*time.Second)
	for _, u := range []string{srv.URL, strings.Replace(srv.URL, "127.0.0.1", "localhost", 1)} {
		if _, err := fetch(u + "/x"); err == nil || !strings.Contains(err.Error(), "non-public") {
			t.Errorf("fetch %s: err = %v, want non-public refusal", u, err)
		}
	}
	if hit {
		t.Fatal("loopback server received a request")
	}
}
