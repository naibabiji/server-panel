package executor

import (
	"log"
	"time"

	"github.com/naibabiji/server-panel/database"
)

// hostMetricsStartupOffset staggers this ticker's phase away from the other
// 60s-interval background jobs started around the same instant at boot
// (StartAgentOfflineChecker, StartAlertChecker) - otherwise all of them tick
// in lockstep every minute and contend for SQLite's single-writer lock at
// the exact same moment, which _busy_timeout doesn't always absorb.
const hostMetricsStartupOffset = 15 * time.Second

// StartHostMetricsCollector periodically samples the panel's own host and
// records it into host_metrics, for the Dashboard's host-performance
// section. Runs on the same interval as the Agent's own default report
// interval (agent/config.go).
func StartHostMetricsCollector(interval time.Duration) {
	if interval <= 0 {
		interval = 60 * time.Second
	}
	go func() {
		time.Sleep(hostMetricsStartupOffset)

		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		recordHostMetrics()
		for range ticker.C {
			recordHostMetrics()
		}
	}()
}

func recordHostMetrics() {
	db := database.GetDB()
	if db == nil {
		return
	}
	s := CollectHostMetrics()

	var err error
	for attempt := 0; attempt < 2; attempt++ {
		if attempt > 0 {
			time.Sleep(250 * time.Millisecond)
		}
		_, err = db.Exec(
			`INSERT INTO host_metrics (
				cpu_percent, memory_percent, memory_used, memory_total,
				disk_percent, disk_used, disk_total, net_rx_bytes, net_tx_bytes,
				load_avg_1, load_avg_5, load_avg_15, uptime_seconds
			) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			s.CPUPercent, s.MemoryPercent, s.MemoryUsed, s.MemoryTotal,
			s.DiskPercent, s.DiskUsed, s.DiskTotal, s.NetRXBytes, s.NetTXBytes,
			s.LoadAvg1, s.LoadAvg5, s.LoadAvg15, s.UptimeSeconds,
		)
		if err == nil {
			return
		}
	}
	log.Printf("Host metric record failed: %v", err)
}
