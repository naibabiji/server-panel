package executor

import (
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	cloudflareIPv4URL = "https://www.cloudflare.com/ips-v4"
	cloudflareIPv6URL = "https://www.cloudflare.com/ips-v6"
)

var (
	cfMu     sync.RWMutex
	cfRanges []*net.IPNet
)

// IsCloudflareIP reports whether ip falls within a published Cloudflare edge range.
func IsCloudflareIP(ip net.IP) bool {
	if ip == nil {
		return false
	}
	cfMu.RLock()
	defer cfMu.RUnlock()
	for _, n := range cfRanges {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// FetchCloudflareIPs downloads Cloudflare's published IPv4/IPv6 edge ranges
// and, on success, atomically replaces the list IsCloudflareIP checks
// against. A failed fetch leaves the previous list in place so a transient
// outage doesn't drop Cloudflare-origin requests back to shared-IP handling.
func FetchCloudflareIPs() error {
	var ranges []*net.IPNet
	for _, url := range []string{cloudflareIPv4URL, cloudflareIPv6URL} {
		body, err := fetchBody(url)
		if err != nil {
			return fmt.Errorf("fetch %s: %w", url, err)
		}
		for _, line := range strings.Split(body, "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			if _, ipnet, err := net.ParseCIDR(line); err == nil {
				ranges = append(ranges, ipnet)
			}
		}
	}
	if len(ranges) == 0 {
		return fmt.Errorf("no cloudflare IP ranges parsed")
	}

	cfMu.Lock()
	cfRanges = ranges
	cfMu.Unlock()
	return nil
}

func fetchBody(url string) (string, error) {
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

// StartCloudflareIPRefresh fetches Cloudflare's edge ranges immediately, then
// keeps refreshing them every interval so a future range change (Cloudflare
// updates these only a few times a year) doesn't require a panel restart.
func StartCloudflareIPRefresh(interval time.Duration) {
	go func() {
		if err := FetchCloudflareIPs(); err != nil {
			log.Printf("cloudflare IP range fetch failed, CDN requests will use shared edge IPs until retry: %v", err)
		} else {
			log.Printf("cloudflare IP ranges loaded")
		}
		for {
			time.Sleep(interval)
			if err := FetchCloudflareIPs(); err != nil {
				log.Printf("cloudflare IP range refresh failed, keeping previous list: %v", err)
			}
		}
	}()
}
