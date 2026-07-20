package executor

import (
	"database/sql"
	"errors"
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

// checkExpiryAlerts triggers/updates/resolves the "about to expire" and
// "already expired, unrenewed" alert for every active server or website.
// It reads the widest configured threshold (the largest "notify N days
// before" rule) as the query window, then classifies each matched row by
// its actual remaining days rather than by which threshold matched -
// that keeps the alert message accurate ("已过期 N 天" vs "将在 N 天后到期")
// even as time passes and a row moves from upcoming to overdue, instead of
// freezing whatever wording was used at first-alert time.
//
// Any failure while reading rules/items aborts the whole check without
// calling resolveInactiveAlerts, so a transient DB read error can never be
// mistaken for "nothing is expiring anymore" and mass-resolve live alerts.
func checkExpiryAlerts(db *sql.DB, alertType, table, dateCol string) {
	if !allowedTables[table] || !allowedColumns[dateCol] {
		return
	}

	rows, err := db.Query(
		"SELECT threshold_value FROM alert_rules WHERE alert_type = ? AND enabled = 1",
		alertType)
	if err != nil {
		return
	}
	var maxDays float64
	hasEnabledRule := false
	for rows.Next() {
		hasEnabledRule = true
		var daysBefore float64
		if err := rows.Scan(&daysBefore); err != nil {
			rows.Close()
			return
		}
		if daysBefore > maxDays {
			maxDays = daysBefore
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return
	}
	rows.Close()
	// No enabled rule for this alert type: nothing should be alerting, and
	// any previously-open alert (from a rule that has since been disabled
	// or deleted) must be resolved rather than left hanging open forever.
	if !hasEnabledRule {
		resolveInactiveAlerts(db, alertType, nil)
		return
	}
	if maxDays <= 0 {
		maxDays = 7
	}
	dateModifier := fmt.Sprintf("+%d days", int(maxDays))

	type expiryTarget struct {
		id, serverID, websiteID int64
		name                    string
		daysLeft                int
	}
	var targets []expiryTarget
	label := "服务器"

	if table == "servers" {
		itemRows, err := db.Query(
			`SELECT id, name, CAST(julianday(expiry_date) - julianday(date('now')) AS INTEGER)
			 FROM servers
			 WHERE expiry_date != '' AND expiry_date <= date('now', ?) AND status = 'active'`,
			dateModifier)
		if err != nil {
			return
		}
		for itemRows.Next() {
			var t expiryTarget
			if err := itemRows.Scan(&t.id, &t.name, &t.daysLeft); err != nil {
				itemRows.Close()
				return
			}
			t.serverID = t.id
			targets = append(targets, t)
		}
		if err := itemRows.Err(); err != nil {
			itemRows.Close()
			return
		}
		itemRows.Close()
	} else {
		label = "网站"
		itemRows, err := db.Query(
			`SELECT id, name, server_id, CAST(julianday(expiry_date) - julianday(date('now')) AS INTEGER)
			 FROM websites
			 WHERE expiry_date != '' AND expiry_date <= date('now', ?) AND status = 'active'`,
			dateModifier)
		if err != nil {
			return
		}
		for itemRows.Next() {
			var t expiryTarget
			if err := itemRows.Scan(&t.id, &t.name, &t.serverID, &t.daysLeft); err != nil {
				itemRows.Close()
				return
			}
			t.websiteID = t.id
			targets = append(targets, t)
		}
		if err := itemRows.Err(); err != nil {
			itemRows.Close()
			return
		}
		itemRows.Close()
	}

	// Item rows are fully drained and closed above before any alert write,
	// same reasoning as executor/auto_renewal.go.
	activeTargets := make(map[alertTarget]bool)
	for _, t := range targets {
		var serverID *int64 = &t.serverID
		var websiteID *int64
		if table == "servers" {
			activeTargets[alertTarget{serverID: t.serverID}] = true
		} else {
			websiteID = &t.websiteID
			activeTargets[alertTarget{serverID: t.serverID, websiteID: t.websiteID}] = true
		}

		var level, message string
		switch {
		case t.daysLeft < 0:
			level = "critical"
			message = fmt.Sprintf("%s %s 已过期 %d 天，尚未续费", label, t.name, -t.daysLeft)
		case t.daysLeft == 0:
			level = "warning"
			message = fmt.Sprintf("%s %s 今天到期", label, t.name)
		default:
			level = "warning"
			message = fmt.Sprintf("%s %s 将在 %d 天后到期", label, t.name, t.daysLeft)
		}
		upsertAlert(db, alertType, serverID, websiteID, level, message)
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

// reachabilityFreshnessWindow bounds how old a TCP reachability check
// (executor/reachability.go, tcp_reachable_checked_at) may be before
// checkOfflineAlerts trusts it. Without this, an ancient check result -
// left over from a previous, unrelated outage, or from before the reachability
// checker ever ran for this server - could otherwise get treated as
// evidence about right now.
const reachabilityFreshnessWindow = "-10 minutes"

// checkOfflineAlerts fires on a lost Agent heartbeat (is_online = 0 for
// longer than the configured threshold), but a lost heartbeat alone doesn't
// mean the server is actually down - the Agent process, its DNS resolution,
// or its own outbound network can break while the server keeps serving
// traffic fine. It cross-checks the independent TCP reachability signal
// (executor/reachability.go, tcp_reachable) to tell the two apart: only a
// server with a *fresh, post-heartbeat-loss* confirmation that it's also
// TCP-unreachable gets the critical "server_offline" alert. Everything else -
// confirmed still-reachable, not yet checked, or a check too stale/old to
// trust - gets the lower severity "agent_offline" alert instead, so an
// unconfirmed guess is never the thing that pages someone about a server
// outage that might not be happening. "agent_offline" shares server_offline's
// threshold/enabled flag (there's no separate rule to configure) and, via
// createAlertUsingRule, its notification routing too.
func checkOfflineAlerts(db *sql.DB) {
	rows, err := db.Query("SELECT threshold_value FROM alert_rules WHERE alert_type = 'server_offline' AND enabled = 1")
	if err != nil {
		return
	}
	offlineTargets := make(map[alertTarget]bool)
	agentOfflineTargets := make(map[alertTarget]bool)

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
		// tcp_reachable is only trusted (non-NULL in the result) when its
		// check ran after this outage started (checked_at > last_seen_at)
		// and recently enough (checked_at within reachabilityFreshnessWindow)
		// - otherwise the CASE forces NULL, which the loop below treats the
		// same as "not yet confirmed".
		sRows, err := db.Query(
			`SELECT id, name,
			   CASE WHEN tcp_reachable_checked_at IS NOT NULL
			             AND tcp_reachable_checked_at > last_seen_at
			             AND tcp_reachable_checked_at >= datetime('now', ?)
			        THEN tcp_reachable
			        ELSE NULL
			   END
			 FROM servers WHERE is_online = 0 AND status = 'active'
			 AND last_seen_at IS NOT NULL AND last_seen_at < datetime('now', ? || ' minutes')`,
			reachabilityFreshnessWindow, strconv.Itoa(-minutes))
		if err != nil {
			continue
		}
		for sRows.Next() {
			var id int64
			var name string
			var confirmedReachable sql.NullInt64
			sRows.Scan(&id, &name, &confirmedReachable)

			if confirmedReachable.Valid && confirmedReachable.Int64 == 0 {
				offlineTargets[alertTarget{serverID: id}] = true
				createAlert(db, "server_offline", &id, nil, "critical",
					fmt.Sprintf("服务器 %s 已离线超过 %d 分钟", name, minutes))
				continue
			}

			agentOfflineTargets[alertTarget{serverID: id}] = true
			message := fmt.Sprintf("服务器 %s 的 Agent 探针已失联超过 %d 分钟，服务器网络可达性尚未确认（请检查探针进程/DNS/出站网络，或等待面板下一轮网络探测）", name, minutes)
			if confirmedReachable.Valid {
				message = fmt.Sprintf("服务器 %s 的 Agent 探针已失联超过 %d 分钟，但服务器网络仍可达（请检查探针进程/DNS/出站网络）", name, minutes)
			}
			createAlertUsingRule(db, "agent_offline", "server_offline", &id, nil, "warning", message)
		}
		sRows.Close()
	}
	resolveInactiveAlerts(db, "server_offline", offlineTargets)
	resolveInactiveAlerts(db, "agent_offline", agentOfflineTargets)
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
	createAlertUsingRule(db, alertType, alertType, serverID, websiteID, level, message)
}

// createAlertUsingRule is like createAlert but looks up notification routing
// (notify_user/notify_email) under ruleType instead of alertType. This is
// for alert types that are a derived/secondary flavor of another type that's
// the one actually configurable in Settings - "agent_offline" (alert_checker.go's
// checkOfflineAlerts) doesn't get its own alert_rules row, and is meant to
// share the routing configured on its source "server_offline" rule rather
// than silently falling back to the admin default because a lookup under
// "agent_offline" never matches anything.
func createAlertUsingRule(db *sql.DB, alertType, ruleType string, serverID *int64, websiteID *int64, level, message string) {
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

	notifyAlertUsingRule(db, alertType, ruleType, serverID, websiteID, message)
}

// upsertAlert is like createAlert but, when an unresolved alert for the same
// target already exists, refreshes its message/level instead of leaving it
// as a no-op. Expiry alerts need this: the same unresolved row lives from
// "will expire in N days" through "already overdue by N days", and the
// wording/severity must track that instead of freezing at first trigger.
// A notification is only (re-)sent when the alert is first created or when
// it escalates to critical, so re-running this every tick doesn't spam.
func upsertAlert(db *sql.DB, alertType string, serverID *int64, websiteID *int64, level, message string) {
	where, args := alertTargetWhere(alertType, serverID, websiteID)
	var id int64
	var existingMessage, existingLevel string
	err := db.QueryRow(`SELECT id, message, level FROM alert_log WHERE `+where+` AND resolved = 0`, args...).
		Scan(&id, &existingMessage, &existingLevel)
	switch {
	case err == nil:
		if existingMessage == message && existingLevel == level {
			return
		}
		if _, err := db.Exec(`UPDATE alert_log SET message = ?, level = ? WHERE id = ?`, message, level, id); err != nil {
			log.Printf("update alert failed: type=%s id=%d: %v", alertType, id, err)
			return
		}
		if level == "critical" && existingLevel != "critical" {
			notifyAlert(db, alertType, serverID, websiteID, message)
		}
		return
	case !errors.Is(err, sql.ErrNoRows):
		log.Printf("check existing alert failed: type=%s: %v", alertType, err)
		return
	}

	if _, err := db.Exec(
		`INSERT INTO alert_log (alert_type, server_id, website_id, level, message) VALUES (?,?,?,?,?)`,
		alertType, nullableID(serverID), nullableID(websiteID), level, message); err != nil {
		log.Printf("create alert failed: type=%s server_id=%v website_id=%v: %v", alertType, nullableID(serverID), nullableID(websiteID), err)
		return
	}
	notifyAlert(db, alertType, serverID, websiteID, message)
}

func notifyAlert(db *sql.DB, alertType string, serverID *int64, websiteID *int64, message string) {
	notifyAlertUsingRule(db, alertType, alertType, serverID, websiteID, message)
}

// notifyAlertUsingRule sends the alert email(s), looking up notify_user/
// notify_email routing under ruleType rather than alertType - see
// createAlertUsingRule. alertType is kept for logging only, so a failed
// send is still attributed to the alert that was actually created.
func notifyAlertUsingRule(db *sql.DB, alertType, ruleType string, serverID *int64, websiteID *int64, message string) {
	go func() {
		for _, recipient := range alertRecipients(db, ruleType, serverID, websiteID) {
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
