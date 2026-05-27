package handlers

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/naibabiji/server-panel/database"
	"github.com/naibabiji/server-panel/models"
)

type DashboardHandler struct{}

func (h *DashboardHandler) GetStats(c *gin.Context) {
	db := database.GetDB()

	var total, online, offline, expiring, totalWebsites, recentAlerts int
	db.QueryRow("SELECT COUNT(*) FROM servers").Scan(&total)
	db.QueryRow("SELECT COUNT(*) FROM servers WHERE is_online = 1").Scan(&online)
	db.QueryRow("SELECT COUNT(*) FROM servers WHERE is_online = 0 AND status = 'active'").Scan(&offline)
	db.QueryRow(`SELECT COUNT(*) FROM servers WHERE expiry_date != '' AND expiry_date <= date('now','+30 days') AND expiry_date >= date('now') AND status = 'active'`).Scan(&expiring)
	db.QueryRow("SELECT COUNT(*) FROM websites").Scan(&totalWebsites)
	db.QueryRow("SELECT COUNT(*) FROM alert_log WHERE resolved = 0 AND created_at > datetime('now','-7 days')").Scan(&recentAlerts)

	c.JSON(http.StatusOK, models.SuccessResponse(map[string]int{
		"total_servers":   total,
		"online_count":    online,
		"offline_count":   offline,
		"expiring_count":  expiring,
		"total_websites":  totalWebsites,
		"recent_alerts":   recentAlerts,
	}))
}

func (h *DashboardHandler) GetExpiring(c *gin.Context) {
	db := database.GetDB()

	// 即将到期的服务器
	serverRows, _ := db.Query(
		`SELECT name, expiry_date FROM servers
		 WHERE expiry_date != '' AND expiry_date <= date('now','+30 days') AND expiry_date >= date('now') AND status = 'active'
		 ORDER BY expiry_date ASC LIMIT 5`)
	type ExpiringItem struct {
		Name       string `json:"name"`
		ExpiryDate string `json:"expiry_date"`
		DaysLeft   int    `json:"days_left"`
	}
	servers := []ExpiringItem{}
	if serverRows != nil {
		defer serverRows.Close()
		for serverRows.Next() {
			var item ExpiringItem
			serverRows.Scan(&item.Name, &item.ExpiryDate)
			// 简单估算剩余天数
			if len(item.ExpiryDate) >= 10 {
				db.QueryRow("SELECT CAST(julianday(?)-julianday('now') AS INTEGER)", item.ExpiryDate[:10]).Scan(&item.DaysLeft)
			}
			servers = append(servers, item)
		}
	}

	// 即将到期的网站
	webRows, _ := db.Query(
		`SELECT w.name, w.expiry_date, s.name FROM websites w
		 LEFT JOIN servers s ON w.server_id = s.id
		 WHERE w.expiry_date != '' AND w.expiry_date <= date('now','+30 days') AND w.expiry_date >= date('now') AND w.status = 'active'
		 ORDER BY w.expiry_date ASC LIMIT 5`)
	websites := []ExpiringItem{}
	if webRows != nil {
		defer webRows.Close()
		for webRows.Next() {
			var item ExpiringItem
			var serverName string
			webRows.Scan(&item.Name, &item.ExpiryDate, &serverName)
			if serverName != "" {
				item.Name = serverName + " — " + item.Name
			}
			if len(item.ExpiryDate) >= 10 {
				db.QueryRow("SELECT CAST(julianday(?)-julianday('now') AS INTEGER)", item.ExpiryDate[:10]).Scan(&item.DaysLeft)
			}
			websites = append(websites, item)
		}
	}

	c.JSON(http.StatusOK, models.SuccessResponse(map[string]interface{}{
		"servers":  servers,
		"websites": websites,
	}))
}

func (h *DashboardHandler) GetRecentAlerts(c *gin.Context) {
	db := database.GetDB()
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "10"))

	rows, err := db.Query(
		`SELECT id, alert_type, server_id, level, message, created_at FROM alert_log
		 WHERE resolved = 0 ORDER BY created_at DESC LIMIT ?`, limit)
	if err != nil {
		c.JSON(http.StatusOK, models.SuccessResponse([]interface{}{}))
		return
	}
	defer rows.Close()

	alerts := []models.AlertLog{}
	for rows.Next() {
		var a models.AlertLog
		rows.Scan(&a.ID, &a.AlertType, &a.ServerID, &a.Level, &a.Message, &a.CreatedAt)
		alerts = append(alerts, a)
	}
	if alerts == nil {
		alerts = []models.AlertLog{}
	}

	c.JSON(http.StatusOK, models.SuccessResponse(alerts))
}
