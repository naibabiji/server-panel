package releasecheck

import (
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"syscall"
	"time"
)

const downloadBase = "https://github.com/naibabiji/server-panel/releases/download/"

// MaxDownloadBytes caps any single release file. Binaries are a few MB;
// anything larger is an error rather than a silently truncated file.
const MaxDownloadBytes = 64 << 20

// FileURL is the download URL of a release asset, optionally prefixed by a
// GitHub proxy (the "<proxy>/<full github url>" form the installer uses).
func FileURL(proxy, tag, name string) string {
	u := downloadBase + tag + "/" + name
	if proxy == "" {
		return u
	}
	return strings.TrimRight(proxy, "/") + "/" + u
}

// NewFetcher returns an HTTP GET for release files. Redirects are only
// followed to GitHub's release hosts or the proxy's own host, so a hostile
// proxy cannot point the caller (the panel, or the root Agent updater) at
// localhost, the LAN or cloud metadata endpoints. Integrity never depends on
// this: callers verify everything against the release signature.
func NewFetcher(proxy string, transport http.RoundTripper, timeout time.Duration) func(string) ([]byte, error) {
	proxyHost := ""
	if pu, err := url.Parse(proxy); err == nil && proxy != "" {
		proxyHost = strings.ToLower(pu.Hostname())
	}
	client := &http.Client{
		Timeout:   timeout,
		Transport: transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return errors.New("too many redirects")
			}
			if !redirectAllowed(req.URL, proxyHost) {
				return fmt.Errorf("redirect to %s not allowed", req.URL.Host)
			}
			return nil
		},
	}
	return func(rawURL string) ([]byte, error) {
		resp, err := client.Get(rawURL)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("GET %s: status %d", rawURL, resp.StatusCode)
		}
		data, err := io.ReadAll(io.LimitReader(resp.Body, MaxDownloadBytes+1))
		if err != nil {
			return nil, err
		}
		if len(data) > MaxDownloadBytes {
			return nil, fmt.Errorf("GET %s: response larger than %d bytes", rawURL, MaxDownloadBytes)
		}
		return data, nil
	}
}

func redirectAllowed(u *url.URL, proxyHost string) bool {
	host := strings.ToLower(u.Hostname())
	if proxyHost != "" && host == proxyHost {
		return u.Scheme == "https" || u.Scheme == "http"
	}
	return u.Scheme == "https" && (host == "github.com" || strings.HasSuffix(host, ".githubusercontent.com"))
}

// AgentBinaryName is the release asset name of the Agent for arch.
func AgentBinaryName(arch string) string { return "server-panel-agent-linux-" + arch }

// VerifiedAgentChecksum downloads checksums-<arch>.txt and its .sig for tag,
// verifies the signature with verify (normally VerifySignatureFile) and
// returns the signed SHA-256 of the Agent binary for arch. This is the one
// place a trusted Agent hash comes from - for the root self-updater and for
// the panel-generated install/upgrade commands alike.
func VerifiedAgentChecksum(fetch func(string) ([]byte, error), verify func(message, sigFile []byte) error, proxy, tag, arch string) (string, error) {
	if !IsStableVersion(tag) {
		return "", fmt.Errorf("invalid release tag %q", tag)
	}
	if arch != "amd64" && arch != "arm64" {
		return "", fmt.Errorf("unsupported architecture %s", arch)
	}
	checksumsName := "checksums-" + arch + ".txt"
	checksums, err := fetch(FileURL(proxy, tag, checksumsName))
	if err != nil {
		return "", fmt.Errorf("download checksums: %w", err)
	}
	sig, err := fetch(FileURL(proxy, tag, checksumsName+".sig"))
	if err != nil {
		return "", fmt.Errorf("download signature: %w", err)
	}
	if err := verify(checksums, sig); err != nil {
		return "", err
	}
	hash, err := ChecksumFor(string(checksums), AgentBinaryName(arch))
	if err != nil {
		return "", err
	}
	hash = strings.ToLower(hash)
	if len(hash) != 64 || strings.Trim(hash, "0123456789abcdef") != "" {
		return "", fmt.Errorf("signed checksum for %s is not a SHA-256 hex digest", AgentBinaryName(arch))
	}
	return hash, nil
}

// PublicTransport is an HTTP transport whose connections may only reach
// public unicast addresses. The check runs in the dialer's Control hook,
// i.e. on the IP actually being connected to after DNS resolution, so a
// hostname that resolves (or later rebinds) to loopback, a private/LAN
// range, link-local (cloud metadata) or multicast is refused. The panel uses
// it for fetches whose host an admin request supplies (a GitHub proxy URL).
// Environment HTTP proxies are deliberately not honoured.
func PublicTransport() *http.Transport {
	dialer := &net.Dialer{
		Timeout: 15 * time.Second,
		Control: func(_, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return err
			}
			if ip := net.ParseIP(host); ip == nil || !IsPublicIP(ip) {
				return fmt.Errorf("connection to non-public address %s refused", host)
			}
			return nil
		},
	}
	return &http.Transport{
		DialContext:           dialer.DialContext,
		TLSHandshakeTimeout:   15 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
		MaxIdleConns:          4,
		IdleConnTimeout:       30 * time.Second,
	}
}

// cgnat is the RFC 6598 shared address space, not covered by net.IP.IsPrivate.
var cgnat = &net.IPNet{IP: net.IPv4(100, 64, 0, 0), Mask: net.CIDRMask(10, 32)}

// IPv6 prefixes that embed an IPv4 address the packet may end up at:
// NAT64 well-known prefix (RFC 6052, v4 in the last 4 bytes) and 6to4
// (RFC 3056, v4 in bytes 2-5).
var (
	nat64  = &net.IPNet{IP: net.ParseIP("64:ff9b::"), Mask: net.CIDRMask(96, 128)}
	sixTo4 = &net.IPNet{IP: net.ParseIP("2002::"), Mask: net.CIDRMask(16, 128)}
)

// specialPurpose lists IANA special-purpose ranges that net.IP still calls
// global unicast: IETF protocol assignments, documentation, benchmarking,
// reserved (240/4), discard-only and IETF IPv6 assignments (incl. Teredo).
var specialPurpose = func() []*net.IPNet {
	var nets []*net.IPNet
	for _, cidr := range []string{
		"192.0.0.0/24", "192.0.2.0/24", "198.18.0.0/15", "198.51.100.0/24",
		"203.0.113.0/24", "240.0.0.0/4",
		"100::/64", "2001::/23", "2001:db8::/32", "3fff::/20",
	} {
		_, n, err := net.ParseCIDR(cidr)
		if err != nil {
			panic(err)
		}
		nets = append(nets, n)
	}
	return nets
}()

// IsPublicIP reports whether ip is a globally routable unicast address.
func IsPublicIP(ip net.IP) bool {
	for _, n := range specialPurpose {
		if n.Contains(ip) {
			return false
		}
	}
	if ip16 := ip.To16(); ip16 != nil && ip.To4() == nil {
		if nat64.Contains(ip16) {
			return IsPublicIP(net.IP(ip16[12:16]))
		}
		if sixTo4.Contains(ip16) {
			return IsPublicIP(net.IP(ip16[2:6]))
		}
	}
	if ip4 := ip.To4(); ip4 != nil {
		ip = ip4
		if ip4[0] == 0 || cgnat.Contains(ip4) || ip4.Equal(net.IPv4bcast) {
			return false
		}
	}
	return ip.IsGlobalUnicast() && !ip.IsPrivate() && !ip.IsLoopback() &&
		!ip.IsLinkLocalUnicast() && !ip.IsMulticast() && !ip.IsUnspecified()
}
