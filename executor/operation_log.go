package executor

import (
	"database/sql"
	"log"

	"github.com/naibabiji/server-panel/database"
)

const operationLogKeepRows = 300

// RecordOperationLog writes a row to the generic operation_logs audit table
// and prunes old rows so the table doesn't grow unbounded. Failures are
// logged rather than returned, since audit logging should never block the
// operation it's recording.
func RecordOperationLog(operation, target, status, message string) {
	db := database.GetDB()
	if db == nil {
		return
	}
	if _, err := db.Exec(
		`INSERT INTO operation_logs (operation, target, status, message) VALUES (?, ?, ?, ?)`,
		operation, target, status, message,
	); err != nil {
		log.Printf("记录操作日志失败: %v", err)
		return
	}
	pruneOperationLogs(db)
}

func pruneOperationLogs(db *sql.DB) {
	_, _ = db.Exec(
		`DELETE FROM operation_logs WHERE id NOT IN (SELECT id FROM operation_logs ORDER BY id DESC LIMIT ?)`,
		operationLogKeepRows,
	)
}
