package executor

import (
	"database/sql"
	"fmt"
	"strconv"
	"time"

	"github.com/naibabiji/server-panel/database"
)

func StartAlertChecker(interval time.Duration) {
	go func() {
		time.Sleep(15 * time.Second)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for range ticker.C {
			runAlertCheck()
		}
	}()
}

func runAlertCheck() {
	db := database.GetDB()
	if db == nil {
		return
	}

	checkExpiryAlerts(db, "server_expiry", "servers", "expiry_date")
	checkExpiryAlerts(db, "website_expiry", "websites", "expiry_date")
	checkHTTPProbeAlerts(db)
	checkResourceAlerts(db, "cpu_high", "cpu_percent")
	checkResourceAlerts(db, "memory_high", "memory_percent")
	checkDiskAlerts(db)
	checkOfflineAlerts(db)
}

var allowedTables = map[string]bool{"servers": true, "websites": true}
var allowedColumns = map[string]bool{"expiry_date": true, "cpu_percent": true, "memory_percent": true, "disk_percent": true}

func checkExpiryAlerts(db *sql.DB, alertType, table, dateCol string) {
	if !allowedTables[table] || !allowedColumns[dateCol] {
		return
	}

	rows, err := db.Query(
		"SELECT id, threshold_value, notify_user, notify_email, server_id FROM alert_rules WHERE alert_type = ? AND enabled = 1",
		alertType)
	if err != nil {
		return
	}
	defer rows.Close()

	for rows.Next() {
		var daysBefore float64
		var notifyUser int
		var notifyEmail string
		var serverID *int64
		rows.Scan(new(int64), &daysBefore, &notifyUser, &notifyEmail, &serverID)
		if daysBefore <= 0 {
			daysBefore = 7
		}

		query := fmt.Sprintf(
			`SELECT s.id, s.name FROM %s s WHERE s.%s != '' AND s.%s <= date('now','+%d days') AND s.%s >= date('now')`,
			table, dateCol, dateCol, int(daysBefore), dateCol)
		if table == "servers" {
			query += " AND s.status = 'active'"
		} else {
			query += " AND s.status = 'active'"
		}

		itemRows, err := db.Query(query)
		if err != nil {
			continue
		}
		for itemRows.Next() {
			var itemID int64
			var itemName string
			itemRows.Scan(&itemID, &itemName)
			label := "服务器"
			if table == "websites" {
				label = "网站"
			}
			createAlert(db, alertType, &itemID, nil, "warning",
				fmt.Sprintf("%s %s 将在 %d 天后到期", label, itemName, int(daysBefore)))
		}
		itemRows.Close()
	}
}

func checkHTTPProbeAlerts(db *sql.DB) {
	rows, err := db.Query("SELECT id, name FROM alert_rules WHERE alert_type = 'http_probe_down' AND enabled = 1")
	if err != nil {
		return
	}
	defer rows.Close()

	hasEnabled := rows.Next()
	if !hasEnabled {
		return
	}

	sRows, err := db.Query(
		"SELECT id, name, http_probe_last_error FROM servers WHERE http_probe_enabled = 1 AND http_probe_healthy = 0")
	if err != nil {
		return
	}
	defer sRows.Close()
	for sRows.Next() {
		var id int64
		var name, lastErr string
		sRows.Scan(&id, &name, &lastErr)
		createAlert(db, "http_probe_down", &id, nil, "warning",
			fmt.Sprintf("服务器 %s HTTP 探测异常: %s", name, lastErr))
	}
}

func checkResourceAlerts(db *sql.DB, alertType, metricCol string) {
	if !allowedColumns[metricCol] {
		return
	}

	rows, err := db.Query(
		"SELECT threshold_value, threshold_count FROM alert_rules WHERE alert_type = ? AND enabled = 1",
		alertType)
	if err != nil {
		return
	}
	defer rows.Close()

	for rows.Next() {
		var threshold, count float64
		rows.Scan(&threshold, &count)
		if threshold <= 0 { threshold = 90 }
		if count <= 0 { count = 3 }

		checkCount := int(count)
		sRows, err := db.Query(
			fmt.Sprintf(`SELECT s.id, s.name FROM servers s WHERE (
			     SELECT COUNT(*) FROM (
			         SELECT %s FROM metrics WHERE server_id = s.id ORDER BY recorded_at DESC LIMIT %d
			     ) WHERE %s > ?
			 ) = %d AND s.is_online = 1`, metricCol, checkCount, metricCol, checkCount),
			threshold)
		if err != nil {
			continue
		}
		for sRows.Next() {
			var id int64
			var name string
			sRows.Scan(&id, &name)
			createAlert(db, alertType, &id, nil, "warning",
				fmt.Sprintf("服务器 %s %s 连续 %d 次超标 (阈值: %.0f%%)", name, alertTypeToLabel(alertType), checkCount, threshold))
		}
		sRows.Close()
	}
}

func checkDiskAlerts(db *sql.DB) {
	rows, err := db.Query("SELECT threshold_value FROM alert_rules WHERE alert_type = 'disk_high' AND enabled = 1")
	if err != nil {
		return
	}
	defer rows.Close()

	for rows.Next() {
		var threshold float64
		rows.Scan(&threshold)
		if threshold <= 0 { continue }

		sRows, err := db.Query(
			`SELECT s.id, s.name, m.disk_percent FROM servers s
			 JOIN (SELECT server_id, disk_percent FROM metrics WHERE (server_id, recorded_at) IN (SELECT server_id, MAX(recorded_at) FROM metrics GROUP BY server_id)) m ON s.id = m.server_id
			 WHERE m.disk_percent > ? AND s.is_online = 1`, threshold)
		if err != nil {
			continue
		}
		for sRows.Next() {
			var id int64
			var name string
			var disk float64
			sRows.Scan(&id, &name, &disk)
			createAlert(db, "disk_high", &id, nil, "warning",
				fmt.Sprintf("服务器 %s 磁盘使用率 %.1f%% 超过阈值 %.0f%%", name, disk, threshold))
		}
		sRows.Close()
	}
}

func checkOfflineAlerts(db *sql.DB) {
	rows, err := db.Query("SELECT threshold_value FROM alert_rules WHERE alert_type = 'server_offline' AND enabled = 1")
	if err != nil {
		return
	}
	defer rows.Close()

	for rows.Next() {
		var threshold float64
		rows.Scan(&threshold)
		if threshold <= 0 { threshold = 5 }

		minutes := int(threshold)
		sRows, err := db.Query(
			`SELECT id, name FROM servers WHERE is_online = 0 AND status = 'active'
			 AND last_seen_at IS NOT NULL AND last_seen_at < datetime('now', ? || ' minutes')`,
			strconv.Itoa(-minutes))
		if err != nil {
			continue
		}
		for sRows.Next() {
			var id int64
			var name string
			sRows.Scan(&id, &name)
			createAlert(db, "server_offline", &id, nil, "critical",
				fmt.Sprintf("服务器 %s 已离线超过 %d 分钟", name, minutes))
		}
		sRows.Close()
	}
}

func alertTypeToLabel(t string) string {
	switch t {
	case "cpu_high": return "CPU"
	case "memory_high": return "内存"
	case "disk_high": return "磁盘"
	default: return t
	}
}

func createAlert(db *sql.DB, alertType string, serverID *int64, websiteID *int64, level, message string) {
	var count int
	if serverID != nil {
		db.QueryRow(
			`SELECT COUNT(*) FROM alert_log WHERE alert_type = ? AND server_id = ? AND resolved = 0 AND created_at > datetime('now','-4 hours')`,
			alertType, *serverID).Scan(&count)
	} else {
		db.QueryRow(
			`SELECT COUNT(*) FROM alert_log WHERE alert_type = ? AND resolved = 0 AND created_at > datetime('now','-4 hours')`,
			alertType).Scan(&count)
	}
	if count > 0 { return }

	_, _ = db.Exec(
		`INSERT INTO alert_log (alert_type, server_id, website_id, level, message) VALUES (?,?,?,?,?)`,
		alertType, serverID, websiteID, level, message)

	go func() {
		_ = SendMail("", "Server Panel 告警", message)
	}()
}
