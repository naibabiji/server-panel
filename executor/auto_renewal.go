package executor

import (
	"database/sql"
	"log"
	"time"

	"github.com/naibabiji/server-panel/database"
	"github.com/naibabiji/server-panel/models"
)

func StartAutoRenewalChecker(interval time.Duration) {
	go func() {
		runAutoRenewals(database.GetDB(), time.Now())

		for {
			time.Sleep(interval)
			runAutoRenewals(database.GetDB(), time.Now())
		}
	}()
}

func runAutoRenewals(db *sql.DB, now time.Time) {
	if db == nil {
		return
	}

	rows, err := db.Query(
		`SELECT id, expiry_date, renewal_cycle, auto_renewal
		 FROM servers
		 WHERE auto_renewal = 1 AND expiry_date != '' AND renewal_cycle != ''`)
	if err != nil {
		log.Printf("auto renewal query failed: %v", err)
		return
	}
	defer rows.Close()

	for rows.Next() {
		var id int64
		var expiryDate, renewalCycle string
		var autoRenewal int
		if err := rows.Scan(&id, &expiryDate, &renewalCycle, &autoRenewal); err != nil {
			log.Printf("auto renewal row read failed: %v", err)
			continue
		}

		renewed := models.RenewedExpiryDate(expiryDate, renewalCycle, autoRenewal, now)
		if renewed == expiryDate {
			continue
		}

		if _, err := db.Exec(
			`UPDATE servers SET expiry_date = ?, status = 'active', updated_at = CURRENT_TIMESTAMP WHERE id = ?`,
			renewed, id,
		); err != nil {
			log.Printf("auto renewal update failed: server_id=%d: %v", id, err)
		}
	}
}
