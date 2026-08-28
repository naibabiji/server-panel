package executor

import (
	"database/sql"
	"log"
	"strconv"
	"sync"
	"time"

	"github.com/naibabiji/server-panel/database"
)

const (
	metricCleanupVacuumThreshold  int64 = 1000
	metricCleanupVacuumInterval         = 24 * time.Hour
	metricCleanupVacuumSettingKey       = "metric_cleanup_last_vacuum_at"
)

var (
	metricCleanupVacuumMu   sync.Mutex
	metricCleanupLastVacuum time.Time
)

func StartMetricCleanup(interval time.Duration) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for range ticker.C {
			db := database.GetDB()
			if db == nil {
				continue
			}

			var retentionDaysStr string
			db.QueryRow("SELECT svalue FROM settings WHERE skey = 'metric_retention_days'").Scan(&retentionDaysStr)
			retentionDays, err := strconv.Atoi(retentionDaysStr)
			if err != nil || retentionDays <= 0 {
				retentionDays = 30
			}

			cleanupMetricHistory(db, retentionDays)
		}
	}()
}

func cleanupMetricHistory(db *sql.DB, retentionDays int) {
	var deleted int64
	result, metricErr := db.Exec("DELETE FROM metrics WHERE recorded_at < datetime('now', ? || ' days')",
		strconv.Itoa(-retentionDays))
	if metricErr != nil {
		log.Printf("Metric cleanup failed: %v", metricErr)
	} else if rows, err := result.RowsAffected(); err == nil {
		deleted += rows
		if rows > 0 {
			log.Printf("Metric cleanup: deleted %d old records (retention: %d days)", rows, retentionDays)
		}
	}

	hostResult, hostErr := db.Exec("DELETE FROM host_metrics WHERE recorded_at < datetime('now', ? || ' days')",
		strconv.Itoa(-retentionDays))
	if hostErr != nil {
		log.Printf("Host metric cleanup failed: %v", hostErr)
	} else if rows, err := hostResult.RowsAffected(); err == nil {
		deleted += rows
		if rows > 0 {
			log.Printf("Host metric cleanup: deleted %d old records (retention: %d days)", rows, retentionDays)
		}
	}

	if metricErr != nil || hostErr != nil || deleted < metricCleanupVacuumThreshold {
		return
	}
	metricCleanupVacuumMu.Lock()
	defer metricCleanupVacuumMu.Unlock()
	now := time.Now()
	var persistedLastVacuum string
	if err := db.QueryRow("SELECT svalue FROM settings WHERE skey = ?", metricCleanupVacuumSettingKey).Scan(&persistedLastVacuum); err == nil {
		if parsed, err := time.Parse(time.RFC3339Nano, persistedLastVacuum); err == nil && now.Sub(parsed) < metricCleanupVacuumInterval {
			metricCleanupLastVacuum = parsed
			return
		}
	}
	if !metricCleanupLastVacuum.IsZero() && now.Sub(metricCleanupLastVacuum) < metricCleanupVacuumInterval {
		return
	}
	started := now
	if _, err := db.Exec("VACUUM"); err != nil {
		log.Printf("Metric cleanup VACUUM failed after deleting %d records (elapsed: %s): %v", deleted, time.Since(started), err)
		return
	}
	metricCleanupLastVacuum = time.Now().UTC()
	if _, err := db.Exec(`INSERT INTO settings (skey, svalue) VALUES (?, ?)
		ON CONFLICT(skey) DO UPDATE SET svalue = excluded.svalue`,
		metricCleanupVacuumSettingKey, metricCleanupLastVacuum.Format(time.RFC3339Nano)); err != nil {
		log.Printf("Metric cleanup failed to persist VACUUM timestamp: %v", err)
	}
	log.Printf("Metric cleanup VACUUM completed after deleting %d records (elapsed: %s)", deleted, time.Since(started))
}
