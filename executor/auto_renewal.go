package executor

import (
	"database/sql"
	"errors"
	"log"
	"strings"
	"time"

	"github.com/naibabiji/server-panel/database"
	"github.com/naibabiji/server-panel/models"
	sqlitedriver "modernc.org/sqlite"
)

// sqliteBusy is SQLITE_BUSY. SQLite's primary result codes are part of its
// stable public C API and are guaranteed by the SQLite project to never
// change, so hardcoding it here (rather than importing the generated
// modernc.org/sqlite/lib bindings just for one constant) is safe.
const sqliteBusy = 5

func StartAutoRenewalChecker(interval time.Duration) {
	go func() {
		// Stagger the first run so it doesn't land in the burst of writes
		// every other Start* background job fires at process startup.
		time.Sleep(30 * time.Second)
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

	type candidate struct {
		id                       int64
		expiryDate, renewalCycle string
		autoRenewal              int
	}
	var candidates []candidate
	for rows.Next() {
		var c candidate
		if err := rows.Scan(&c.id, &c.expiryDate, &c.renewalCycle, &c.autoRenewal); err != nil {
			log.Printf("auto renewal row read failed: %v", err)
			continue
		}
		candidates = append(candidates, c)
	}
	rowsErr := rows.Err()
	rows.Close()
	if rowsErr != nil {
		// Iteration broke partway through: candidates only holds a partial
		// result set. Renewing just those would silently skip the rest
		// until the next scheduled run picks them up 24h later - abort
		// instead and let the next run retry the query from scratch.
		log.Printf("auto renewal row iteration failed: %v", rowsErr)
		return
	}

	// The SELECT cursor above is fully drained and closed before any UPDATE
	// runs, so a write never has to wait on our own still-open read.
	for _, c := range candidates {
		renewed := models.RenewedExpiryDate(c.expiryDate, c.renewalCycle, c.autoRenewal, now)
		if renewed == c.expiryDate {
			continue
		}

		// Optimistic concurrency: only apply the renewal if expiry_date is
		// still what we read it as. If an admin edited the record (or
		// disabled auto_renewal) in between, RowsAffected is 0 and we skip
		// it rather than clobbering their change with a stale computation.
		result, err := execWithBusyRetry(db,
			`UPDATE servers SET expiry_date = ?, status = 'active', updated_at = CURRENT_TIMESTAMP
			 WHERE id = ? AND expiry_date = ? AND auto_renewal = 1`,
			renewed, c.id, c.expiryDate,
		)
		if err != nil {
			log.Printf("auto renewal update failed: server_id=%d: %v", c.id, err)
			continue
		}
		affected, err := result.RowsAffected()
		if err != nil {
			log.Printf("auto renewal update: could not read rows affected for server_id=%d: %v", c.id, err)
			continue
		}
		if affected == 0 {
			log.Printf("auto renewal skipped server_id=%d: expiry_date/auto_renewal changed concurrently", c.id)
		}
	}
}

// execWithBusyRetry retries an Exec a few times when SQLite reports the
// database as locked/busy. Each attempt already blocks internally for up to
// busy_timeout (5s, see database.Open) before returning SQLITE_BUSY, so this
// is a small amount of extra defense-in-depth against contention that
// outlasts that internal wait - not the primary mechanism (that's WAL mode
// plus a real busy_timeout, both enabled in database.Open).
func execWithBusyRetry(db *sql.DB, query string, args ...interface{}) (sql.Result, error) {
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			time.Sleep(time.Duration(attempt) * 300 * time.Millisecond)
		}
		result, err := db.Exec(query, args...)
		if err == nil {
			return result, nil
		}
		lastErr = err
		if !isBusyErr(err) {
			return nil, err
		}
	}
	return nil, lastErr
}

func isBusyErr(err error) bool {
	var sqliteErr *sqlitedriver.Error
	if errors.As(err, &sqliteErr) {
		return sqliteErr.Code()&0xff == sqliteBusy
	}
	// Fallback in case the error ever arrives unwrapped/stringified.
	msg := err.Error()
	return strings.Contains(msg, "database is locked") || strings.Contains(msg, "SQLITE_BUSY")
}
