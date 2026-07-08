package executor

import (
	"crypto/tls"
	"database/sql"
	"errors"
	"log"
	"math/rand"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/naibabiji/server-panel/database"
	"github.com/naibabiji/server-panel/timeutil"
)

func StartHTTPProber(fallbackInterval time.Duration) {
	go func() {
		time.Sleep(10 * time.Second)

		for {
			runProbe()
			time.Sleep(currentProbeInterval(fallbackInterval))
		}
	}()
}

func currentProbeInterval(fallback time.Duration) time.Duration {
	db := database.GetDB()
	if db == nil {
		return fallback
	}

	var minutesStr string
	_ = db.QueryRow("SELECT svalue FROM settings WHERE skey = 'http_probe_interval_minutes'").Scan(&minutesStr)
	minutes, err := strconv.Atoi(minutesStr)
	if err != nil || minutes <= 0 {
		return fallback
	}
	return time.Duration(minutes) * time.Minute
}

func runProbe() {
	db := database.GetDB()
	if db == nil {
		return
	}

	var timeoutStr string
	_ = db.QueryRow("SELECT svalue FROM settings WHERE skey = 'http_probe_timeout_seconds'").Scan(&timeoutStr)
	timeoutSec, err := strconv.Atoi(timeoutStr)
	if err != nil || timeoutSec <= 0 {
		timeoutSec = 10
	}

	rows, err := db.Query("SELECT id FROM servers WHERE http_probe_enabled = 1")
	if err != nil {
		log.Printf("HTTP prober query failed: %v", err)
		return
	}
	defer rows.Close()

	var serverIDs []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err == nil {
			serverIDs = append(serverIDs, id)
		}
	}

	now := timeutil.NowDisplay()
	for _, serverID := range serverIDs {
		domain := pickRandomDomain(db, serverID)
		if domain == "" {
			continue
		}

		healthy, errMsg := checkDomain(domain, timeoutSec)
		if healthy {
			_, _ = db.Exec(`UPDATE servers SET http_probe_healthy = 1, http_probe_last_at = ?, http_probe_last_error = '' WHERE id = ?`,
				now, serverID)
		} else {
			_, _ = db.Exec(`UPDATE servers SET http_probe_healthy = 0, http_probe_last_at = ?, http_probe_last_error = ? WHERE id = ?`,
				now, errMsg, serverID)
		}
	}
}

func pickRandomDomain(db *sql.DB, serverID int64) string {
	rows, err := db.Query("SELECT domain FROM websites WHERE server_id = ? AND status = 'active'", serverID)
	if err != nil {
		return ""
	}
	defer rows.Close()

	var domains []string
	for rows.Next() {
		var d string
		if err := rows.Scan(&d); err == nil && d != "" {
			domains = append(domains, d)
		}
	}
	if len(domains) == 0 {
		return ""
	}
	return domains[rand.Intn(len(domains))]
}

func checkDomain(domain string, timeoutSec int) (bool, string) {
	target, err := normalizeProbeURL(domain)
	if err != nil {
		return false, err.Error()
	}

	client := &http.Client{
		Timeout: time.Duration(timeoutSec) * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 3 {
				return http.ErrUseLastResponse
			}
			return nil
		},
	}

	req, err := http.NewRequest(http.MethodGet, target, nil)
	if err != nil {
		return false, err.Error()
	}
	// Go's default "Go-http-client/1.1" User-Agent (and the absence of any
	// other browser-like headers) is a well-known bot signature that many
	// WAFs/CDNs (Cloudflare, Sucuri, security plugins, etc.) challenge or
	// deliberately slow-walk, even though a real browser sails through
	// instantly - that mismatch is what surfaces here as a probe timeout
	// ("context deadline exceeded ... awaiting headers") on a site that's
	// actually reachable. Looking like an ordinary browser avoids that.
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")

	resp, err := client.Do(req)
	if err != nil {
		return false, err.Error()
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return true, ""
	}
	return false, "HTTP " + strconv.Itoa(resp.StatusCode)
}

func normalizeProbeURL(domain string) (string, error) {
	target := strings.TrimSpace(domain)
	if target == "" {
		return "", errors.New("empty domain")
	}

	if !strings.HasPrefix(target, "http://") && !strings.HasPrefix(target, "https://") {
		target = "https://" + target
	}

	u, err := url.Parse(target)
	if err != nil {
		return "", err
	}
	if u.Host == "" {
		return "", errors.New("invalid domain")
	}
	u.Scheme = "https"
	u.Path = "/"
	u.RawQuery = ""
	u.Fragment = ""
	return u.String(), nil
}
