package executor

import (
	"log"
	"strconv"
	"time"

	"github.com/naibabiji/server-panel/database"
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

			result, err := db.Exec("DELETE FROM metrics WHERE recorded_at < datetime('now', ? || ' days')",
				strconv.Itoa(-retentionDays))
			if err != nil {
				log.Printf("Metric cleanup failed: %v", err)
			} else if rows, _ := result.RowsAffected(); rows > 0 {
				log.Printf("Metric cleanup: deleted %d old records (retention: %d days)", rows, retentionDays)
			}
		}
	}()
}
