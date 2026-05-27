package executor

import (
	"crypto/tls"
	"database/sql"
	"log"
	"math/rand"
	"net/http"
	"strconv"
	"time"

	"github.com/naibabiji/server-panel/database"
)

func StartHTTPProber(interval time.Duration) {
	go func() {
		// 启动后等 10 秒再开始探测
		time.Sleep(10 * time.Second)

		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for range ticker.C {
			runProbe()
		}
	}()
}

func runProbe() {
	db := database.GetDB()
	if db == nil {
		return
	}

	// 获取探测参数
	var timeoutSec int
	db.QueryRow("SELECT svalue FROM settings WHERE skey = 'http_probe_timeout_seconds'").Scan(&timeoutSec)
	if timeoutSec <= 0 {
		timeoutSec = 10
	}

	// 查询开启探测的服务器
	rows, err := db.Query("SELECT id FROM servers WHERE http_probe_enabled = 1")
	if err != nil {
		log.Printf("HTTP prober query failed: %v", err)
		return
	}
	defer rows.Close()

	var serverIDs []int64
	for rows.Next() {
		var id int64
		rows.Scan(&id)
		serverIDs = append(serverIDs, id)
	}

	now := time.Now().UTC().Format("2006-01-02 15:04:05")

	for _, serverID := range serverIDs {
		domain := pickRandomDomain(db, serverID)
		if domain == "" {
			continue
		}

		healthy, errMsg := checkDomain(domain, timeoutSec)

		if healthy {
			db.Exec(`UPDATE servers SET http_probe_healthy = 1, http_probe_last_at = ?, http_probe_last_error = '' WHERE id = ?`,
				now, serverID)
		} else {
			db.Exec(`UPDATE servers SET http_probe_healthy = 0, http_probe_last_at = ?, http_probe_last_error = ? WHERE id = ?`,
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
		rows.Scan(&d)
		if d != "" {
			domains = append(domains, d)
		}
	}
	if len(domains) == 0 {
		return ""
	}
	return domains[rand.Intn(len(domains))]
}

func checkDomain(domain string, timeoutSec int) (bool, string) {
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

	url := "https://" + domain
	resp, err := client.Get(url)
	if err != nil {
		return false, err.Error()
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 200 && resp.StatusCode < 400 {
		return true, ""
	}
	return false, "HTTP " + strconv.Itoa(resp.StatusCode)
}
