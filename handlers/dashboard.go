package handlers

import (
	"database/sql"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/naibabiji/server-panel/database"
	"github.com/naibabiji/server-panel/models"
)

type DashboardHandler struct{}

func (h *DashboardHandler) GetStats(c *gin.Context) {
	db := database.GetDB()

	var total, online, offline, expiringServers, expiringWebsites, totalWebsites, recentAlerts int
	db.QueryRow("SELECT COUNT(*) FROM servers").Scan(&total)
	db.QueryRow("SELECT COUNT(*) FROM servers WHERE is_online = 1").Scan(&online)
	db.QueryRow(`SELECT COUNT(*) FROM servers
		WHERE is_online = 0 AND status = 'active'
		AND (agent_version != '' OR last_seen_at IS NOT NULL)`).Scan(&offline)
	db.QueryRow(`SELECT COUNT(*) FROM servers WHERE expiry_date != '' AND expiry_date <= date('now','+30 days') AND expiry_date >= date('now') AND status = 'active'`).Scan(&expiringServers)
	db.QueryRow(`SELECT COUNT(*) FROM websites WHERE expiry_date != '' AND expiry_date <= date('now','+30 days') AND expiry_date >= date('now') AND status = 'active'`).Scan(&expiringWebsites)
	db.QueryRow("SELECT COUNT(*) FROM websites").Scan(&totalWebsites)
	db.QueryRow("SELECT COUNT(*) FROM alert_log WHERE resolved = 0 AND created_at > datetime('now','-7 days')").Scan(&recentAlerts)

	c.JSON(http.StatusOK, models.SuccessResponse(map[string]int{
		"total_servers":  total,
		"online_count":   online,
		"offline_count":  offline,
		"expiring_count": expiringServers + expiringWebsites,
		"total_websites": totalWebsites,
		"recent_alerts":  recentAlerts,
	}))
}

func (h *DashboardHandler) GetExpiring(c *gin.Context) {
	db := database.GetDB()

	// 即将到期的服务器
	serverRows, err := db.Query(
		`SELECT id, name, expiry_date,
		 CAST(julianday(expiry_date) - julianday(date('now')) AS INTEGER) AS days_left
		 FROM servers
		 WHERE expiry_date != '' AND expiry_date <= date('now','+30 days') AND expiry_date >= date('now') AND status = 'active'
		 ORDER BY expiry_date ASC LIMIT 10`)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse("获取即将到期服务器失败"))
		return
	}
	type ExpiringItem struct {
		ID         int64  `json:"id"`
		Name       string `json:"name"`
		ExpiryDate string `json:"expiry_date"`
		DaysLeft   int    `json:"days_left"`
		DetailPath string `json:"detail_path"`
		ServerName string `json:"server_name,omitempty"`
	}
	servers := []ExpiringItem{}
	defer serverRows.Close()
	for serverRows.Next() {
		var item ExpiringItem
		if err := serverRows.Scan(&item.ID, &item.Name, &item.ExpiryDate, &item.DaysLeft); err != nil {
			c.JSON(http.StatusInternalServerError, models.ErrorResponse("解析即将到期服务器失败"))
			return
		}
		item.DetailPath = "/servers/" + strconv.FormatInt(item.ID, 10)
		servers = append(servers, item)
	}
	if err := serverRows.Err(); err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse("读取即将到期服务器失败"))
		return
	}

	// 即将到期的网站
	webRows, err := db.Query(
		`SELECT w.id, w.name, w.domain, w.expiry_date, COALESCE(s.name, ''),
		 CAST(julianday(w.expiry_date) - julianday(date('now')) AS INTEGER) AS days_left
		 FROM websites w
		 LEFT JOIN servers s ON w.server_id = s.id
		 WHERE w.expiry_date != '' AND w.expiry_date <= date('now','+30 days') AND w.expiry_date >= date('now') AND w.status = 'active'
		 ORDER BY w.expiry_date ASC LIMIT 10`)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse("获取即将到期网站失败"))
		return
	}
	websites := []ExpiringItem{}
	defer webRows.Close()
	for webRows.Next() {
		var item ExpiringItem
		var domain string
		if err := webRows.Scan(&item.ID, &item.Name, &domain, &item.ExpiryDate, &item.ServerName, &item.DaysLeft); err != nil {
			c.JSON(http.StatusInternalServerError, models.ErrorResponse("解析即将到期网站失败"))
			return
		}
		if item.Name == "" {
			item.Name = domain
		}
		item.DetailPath = "/websites/" + strconv.FormatInt(item.ID, 10)
		websites = append(websites, item)
	}
	if err := webRows.Err(); err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse("读取即将到期网站失败"))
		return
	}

	c.JSON(http.StatusOK, models.SuccessResponse(map[string]interface{}{
		"servers":  servers,
		"websites": websites,
	}))
}

func (h *DashboardHandler) GetHTTPProbeIssues(c *gin.Context) {
	db := database.GetDB()

	rows, err := db.Query(
		`SELECT id, name, http_probe_last_at, http_probe_last_error
		 FROM servers
		 WHERE http_probe_enabled = 1 AND http_probe_healthy = 0
		 ORDER BY http_probe_last_at DESC LIMIT 20`)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse("获取 HTTP 探测异常失败"))
		return
	}
	defer rows.Close()

	type probeIssue struct {
		ID         int64  `json:"id"`
		Name       string `json:"name"`
		LastAt     string `json:"last_at"`
		LastError  string `json:"last_error"`
		DetailPath string `json:"detail_path"`
	}
	issues := []probeIssue{}
	for rows.Next() {
		var item probeIssue
		var lastAt sql.NullString
		if err := rows.Scan(&item.ID, &item.Name, &lastAt, &item.LastError); err != nil {
			c.JSON(http.StatusInternalServerError, models.ErrorResponse("解析 HTTP 探测异常失败"))
			return
		}
		if lastAt.Valid {
			item.LastAt = lastAt.String
		}
		item.DetailPath = "/servers/" + strconv.FormatInt(item.ID, 10)
		issues = append(issues, item)
	}
	if err := rows.Err(); err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse("读取 HTTP 探测异常失败"))
		return
	}

	c.JSON(http.StatusOK, models.SuccessResponse(issues))
}

func (h *DashboardHandler) GetRecentAlerts(c *gin.Context) {
	db := database.GetDB()
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "10"))

	rows, err := db.Query(
		`SELECT a.id, a.alert_type, a.server_id, a.website_id, a.level, a.message, a.created_at,
		 COALESCE(s.name, ''), COALESCE(w.name, '')
		 FROM alert_log a
		 LEFT JOIN servers s ON a.server_id = s.id
		 LEFT JOIN websites w ON a.website_id = w.id
		 WHERE a.resolved = 0 ORDER BY a.created_at DESC LIMIT ?`, limit)
	if err != nil {
		c.JSON(http.StatusOK, models.SuccessResponse([]interface{}{}))
		return
	}
	defer rows.Close()

	type alertItem struct {
		models.AlertLog
		ServerName  string `json:"server_name"`
		WebsiteName string `json:"website_name"`
	}
	alerts := []alertItem{}
	for rows.Next() {
		var a alertItem
		rows.Scan(&a.ID, &a.AlertType, &a.ServerID, &a.WebsiteID, &a.Level, &a.Message, &a.CreatedAt,
			&a.ServerName, &a.WebsiteName)
		alerts = append(alerts, a)
	}
	if alerts == nil {
		alerts = []alertItem{}
	}

	c.JSON(http.StatusOK, models.SuccessResponse(alerts))
}
