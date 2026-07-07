package executor

import (
	"database/sql"
	"fmt"
	"log"
	"strconv"
	"strings"
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
	activeTargets := make(map[alertTarget]bool)

	rows, err := db.Query(
		"SELECT threshold_value FROM alert_rules WHERE alert_type = ? AND enabled = 1",
		alertType)
	if err != nil {
		return
	}

	var thresholds []float64
	for rows.Next() {
		var daysBefore float64
		if err := rows.Scan(&daysBefore); err == nil {
			thresholds = append(thresholds, daysBefore)
		}
	}
	rows.Close()

	for _, daysBefore := range thresholds {
		if daysBefore <= 0 {
			daysBefore = 7
		}
		dateModifier := fmt.Sprintf("+%d days", int(daysBefore))

		if table == "servers" {
			itemRows, err := db.Query(
				`SELECT id, name FROM servers
				 WHERE expiry_date != '' AND expiry_date <= date('now', ?) AND expiry_date >= date('now')
				 AND status = 'active'`,
				dateModifier)
			if err != nil {
				continue
			}
			for itemRows.Next() {
				var serverID int64
				var itemName string
				itemRows.Scan(&serverID, &itemName)
				activeTargets[alertTarget{serverID: serverID}] = true
				createAlert(db, alertType, &serverID, nil, "warning",
					fmt.Sprintf("服务器 %s 将在 %d 天后到期", itemName, int(daysBefore)))
			}
			itemRows.Close()
		} else {
			itemRows, err := db.Query(
				`SELECT id, name, server_id FROM websites
				 WHERE expiry_date != '' AND expiry_date <= date('now', ?) AND expiry_date >= date('now')
				 AND status = 'active'`,
				dateModifier)
			if err != nil {
				continue
			}
			for itemRows.Next() {
				var websiteID, serverID int64
				var itemName string
				itemRows.Scan(&websiteID, &itemName, &serverID)
				activeTargets[alertTarget{serverID: serverID, websiteID: websiteID}] = true
				createAlert(db, alertType, &serverID, &websiteID, "warning",
					fmt.Sprintf("网站 %s 将在 %d 天后到期", itemName, int(daysBefore)))
			}
			itemRows.Close()
		}
	}
	resolveInactiveAlerts(db, alertType, activeTargets)
}

func checkHTTPProbeAlerts(db *sql.DB) {
	rows, err := db.Query("SELECT id, name FROM alert_rules WHERE alert_type = 'http_probe_down' AND enabled = 1")
	if err != nil {
		return
	}

	hasEnabled := rows.Next()
	rows.Close()
	if !hasEnabled {
		resolveInactiveAlerts(db, "http_probe_down", nil)
		return
	}

	activeTargets := make(map[alertTarget]bool)
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
		activeTargets[alertTarget{serverID: id}] = true
		createAlert(db, "http_probe_down", &id, nil, "warning",
			fmt.Sprintf("服务器 %s HTTP 探测异常: %s", name, lastErr))
	}
	resolveInactiveAlerts(db, "http_probe_down", activeTargets)
}

func checkResourceAlerts(db *sql.DB, alertType, metricCol string) {
	if !allowedColumns[metricCol] {
		return
	}
	activeTargets := make(map[alertTarget]bool)

	rows, err := db.Query(
		"SELECT threshold_value, threshold_count FROM alert_rules WHERE alert_type = ? AND enabled = 1",
		alertType)
	if err != nil {
		return
	}

	type resourceRule struct {
		threshold float64
		count     float64
	}
	var rules []resourceRule
	for rows.Next() {
		var r resourceRule
		if err := rows.Scan(&r.threshold, &r.count); err == nil {
			rules = append(rules, r)
		}
	}
	rows.Close()

	for _, r := range rules {
		if r.threshold <= 0 {
			r.threshold = 90
		}
		if r.count <= 0 {
			r.count = 3
		}

		checkCount := int(r.count)
		sRows, err := db.Query(
			fmt.Sprintf(`SELECT s.id, s.name FROM servers s WHERE (
			     SELECT COUNT(*) FROM (
			         SELECT %s FROM metrics WHERE server_id = s.id ORDER BY recorded_at DESC LIMIT %d
			     ) WHERE %s > ?
			 ) = %d AND s.is_online = 1`, metricCol, checkCount, metricCol, checkCount),
			r.threshold)
		if err != nil {
			continue
		}
		for sRows.Next() {
			var id int64
			var name string
			sRows.Scan(&id, &name)
			activeTargets[alertTarget{serverID: id}] = true
			createAlert(db, alertType, &id, nil, "warning",
				fmt.Sprintf("服务器 %s %s 连续 %d 次超标 (阈值: %.0f%%)", name, alertTypeToLabel(alertType), checkCount, r.threshold))
		}
		sRows.Close()
	}
	resolveInactiveAlerts(db, alertType, activeTargets)
}

func checkDiskAlerts(db *sql.DB) {
	rows, err := db.Query("SELECT threshold_value FROM alert_rules WHERE alert_type = 'disk_high' AND enabled = 1")
	if err != nil {
		return
	}
	activeTargets := make(map[alertTarget]bool)

	var thresholds []float64
	for rows.Next() {
		var threshold float64
		if err := rows.Scan(&threshold); err == nil {
			thresholds = append(thresholds, threshold)
		}
	}
	rows.Close()

	for _, threshold := range thresholds {
		if threshold <= 0 {
			continue
		}

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
			activeTargets[alertTarget{serverID: id}] = true
			createAlert(db, "disk_high", &id, nil, "warning",
				fmt.Sprintf("服务器 %s 磁盘使用率 %.1f%% 超过阈值 %.0f%%", name, disk, threshold))
		}
		sRows.Close()
	}
	resolveInactiveAlerts(db, "disk_high", activeTargets)
}

func checkOfflineAlerts(db *sql.DB) {
	rows, err := db.Query("SELECT threshold_value FROM alert_rules WHERE alert_type = 'server_offline' AND enabled = 1")
	if err != nil {
		return
	}
	activeTargets := make(map[alertTarget]bool)

	var thresholds []float64
	for rows.Next() {
		var threshold float64
		if err := rows.Scan(&threshold); err == nil {
			thresholds = append(thresholds, threshold)
		}
	}
	rows.Close()

	for _, threshold := range thresholds {
		if threshold <= 0 {
			threshold = 5
		}

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
			activeTargets[alertTarget{serverID: id}] = true
			createAlert(db, "server_offline", &id, nil, "critical",
				fmt.Sprintf("服务器 %s 已离线超过 %d 分钟", name, minutes))
		}
		sRows.Close()
	}
	resolveInactiveAlerts(db, "server_offline", activeTargets)
}

func alertTypeToLabel(t string) string {
	switch t {
	case "cpu_high":
		return "CPU"
	case "memory_high":
		return "内存"
	case "disk_high":
		return "磁盘"
	default:
		return t
	}
}

func createAlert(db *sql.DB, alertType string, serverID *int64, websiteID *int64, level, message string) {
	var count int
	where, args := alertTargetWhere(alertType, serverID, websiteID)
	_ = db.QueryRow(`SELECT COUNT(*) FROM alert_log WHERE `+where+` AND resolved = 0`, args...).Scan(&count)
	if count > 0 {
		return
	}

	if _, err := db.Exec(
		`INSERT INTO alert_log (alert_type, server_id, website_id, level, message) VALUES (?,?,?,?,?)`,
		alertType, nullableID(serverID), nullableID(websiteID), level, message); err != nil {
		log.Printf("create alert failed: type=%s server_id=%v website_id=%v: %v", alertType, nullableID(serverID), nullableID(websiteID), err)
		return
	}

	go func() {
		for _, recipient := range alertRecipients(db, alertType, serverID, websiteID) {
			if err := SendMail(recipient, "Server Panel 告警", message); err != nil {
				log.Printf("send alert email failed: recipient=%q type=%s server_id=%v website_id=%v: %v",
					recipient, alertType, nullableID(serverID), nullableID(websiteID), err)
			}
		}
	}()
}

func nullableID(id *int64) interface{} {
	if id == nil {
		return nil
	}
	return *id
}

type alertTarget struct {
	serverID  int64
	websiteID int64
}

func alertTargetWhere(alertType string, serverID *int64, websiteID *int64) (string, []interface{}) {
	where := "alert_type = ?"
	args := []interface{}{alertType}
	if serverID != nil {
		where += " AND server_id = ?"
		args = append(args, *serverID)
	} else {
		where += " AND server_id IS NULL"
	}
	if websiteID != nil {
		where += " AND website_id = ?"
		args = append(args, *websiteID)
	} else {
		where += " AND website_id IS NULL"
	}
	return where, args
}

func resolveInactiveAlerts(db *sql.DB, alertType string, active map[alertTarget]bool) {
	rows, err := db.Query(`SELECT id, COALESCE(server_id, 0), COALESCE(website_id, 0)
		FROM alert_log WHERE alert_type = ? AND resolved = 0`, alertType)
	if err != nil {
		return
	}
	defer rows.Close()

	var resolveIDs []int64
	for rows.Next() {
		var id int64
		var serverID int64
		var websiteID int64
		if err := rows.Scan(&id, &serverID, &websiteID); err != nil {
			continue
		}
		if active == nil || !active[alertTarget{serverID: serverID, websiteID: websiteID}] {
			resolveIDs = append(resolveIDs, id)
		}
	}
	rows.Close()

	for _, id := range resolveIDs {
		_, _ = db.Exec("UPDATE alert_log SET resolved = 1 WHERE id = ?", id)
	}
}

func alertRecipients(db *sql.DB, alertType string, serverID *int64, websiteID *int64) []string {
	notifyUser, notifyEmail := alertRuleNotification(db, alertType, serverID)
	recipients := make([]string, 0, 2)
	seen := map[string]bool{}
	add := func(email string) {
		email = strings.TrimSpace(email)
		if seen[email] {
			return
		}
		seen[email] = true
		recipients = append(recipients, email)
	}

	if notifyEmail != "" {
		add(notifyEmail)
	} else {
		add("")
	}
	if notifyUser == 1 {
		if userEmail := alertUserEmail(db, serverID, websiteID); userEmail != "" {
			add(userEmail)
		}
	}
	return recipients
}

func alertRuleNotification(db *sql.DB, alertType string, serverID *int64) (int, string) {
	var notifyUser int
	var notifyEmail string
	if serverID != nil {
		err := db.QueryRow(`SELECT notify_user, notify_email FROM alert_rules
			WHERE alert_type = ? AND enabled = 1 AND server_id = ?
			LIMIT 1`, alertType, *serverID).Scan(&notifyUser, &notifyEmail)
		if err == nil {
			return notifyUser, notifyEmail
		}
	}
	_ = db.QueryRow(`SELECT notify_user, notify_email FROM alert_rules
		WHERE alert_type = ? AND enabled = 1 AND server_id IS NULL
		LIMIT 1`, alertType).Scan(&notifyUser, &notifyEmail)
	return notifyUser, notifyEmail
}

func alertUserEmail(db *sql.DB, serverID *int64, websiteID *int64) string {
	var email string
	if websiteID != nil {
		_ = db.QueryRow(`SELECT COALESCE(u.email, '') FROM websites w
			LEFT JOIN customers u ON w.customer_id = u.id WHERE w.id = ?`, *websiteID).Scan(&email)
		return email
	}
	if serverID != nil {
		_ = db.QueryRow(`SELECT COALESCE(u.email, '') FROM servers s
			LEFT JOIN customers u ON s.customer_id = u.id WHERE s.id = ?`, *serverID).Scan(&email)
	}
	return email
}
