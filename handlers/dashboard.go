package handlers

import (
	"database/sql"
	"net/http"
	"os"
	"runtime"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/naibabiji/server-panel/database"
	"github.com/naibabiji/server-panel/models"
	"github.com/naibabiji/server-panel/timeutil"
)

type DashboardHandler struct{}

func (h *DashboardHandler) GetStats(c *gin.Context) {
	db := database.GetDB()

	var total, online, offline, expiringServers, expiringWebsites, overdueServers, overdueWebsites, totalWebsites, recentAlerts int
	db.QueryRow("SELECT COUNT(*) FROM servers").Scan(&total)
	db.QueryRow("SELECT COUNT(*) FROM servers WHERE is_online = 1").Scan(&online)
	db.QueryRow(`SELECT COUNT(*) FROM servers
		WHERE is_online = 0 AND status = 'active'
		AND (agent_version != '' OR last_seen_at IS NOT NULL)`).Scan(&offline)
	db.QueryRow(`SELECT COUNT(*) FROM servers WHERE expiry_date != '' AND expiry_date <= date('now','+30 days') AND expiry_date >= date('now') AND status = 'active'`).Scan(&expiringServers)
	db.QueryRow(`SELECT COUNT(*) FROM websites WHERE expiry_date != '' AND expiry_date <= date('now','+30 days') AND expiry_date >= date('now') AND status = 'active'`).Scan(&expiringWebsites)
	db.QueryRow(`SELECT COUNT(*) FROM servers WHERE expiry_date != '' AND expiry_date < date('now') AND status = 'active'`).Scan(&overdueServers)
	db.QueryRow(`SELECT COUNT(*) FROM websites WHERE expiry_date != '' AND expiry_date < date('now') AND status = 'active'`).Scan(&overdueWebsites)
	db.QueryRow("SELECT COUNT(*) FROM websites").Scan(&totalWebsites)
	db.QueryRow("SELECT COUNT(*) FROM alert_log WHERE resolved = 0 AND created_at > datetime('now','-7 days')").Scan(&recentAlerts)

	c.JSON(http.StatusOK, models.SuccessResponse(map[string]int{
		"total_servers":  total,
		"online_count":   online,
		"offline_count":  offline,
		"expiring_count": expiringServers + expiringWebsites,
		"overdue_count":  overdueServers + overdueWebsites,
		"total_websites": totalWebsites,
		"recent_alerts":  recentAlerts,
	}))
}

type ExpiringItem struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	ExpiryDate string `json:"expiry_date"`
	DaysLeft   int    `json:"days_left"`
	DetailPath string `json:"detail_path"`
	ServerName string `json:"server_name,omitempty"`
}

func queryExpiringServers(db *sql.DB, extraWhere string) ([]ExpiringItem, error) {
	rows, err := db.Query(
		`SELECT id, name, expiry_date,
		 CAST(julianday(expiry_date) - julianday(date('now')) AS INTEGER) AS days_left
		 FROM servers
		 WHERE expiry_date != '' AND status = 'active' AND ` + extraWhere + `
		 ORDER BY expiry_date ASC LIMIT 10`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := []ExpiringItem{}
	for rows.Next() {
		var item ExpiringItem
		if err := rows.Scan(&item.ID, &item.Name, &item.ExpiryDate, &item.DaysLeft); err != nil {
			return nil, err
		}
		item.DetailPath = "/servers/" + strconv.FormatInt(item.ID, 10)
		items = append(items, item)
	}
	return items, rows.Err()
}

func queryExpiringWebsites(db *sql.DB, extraWhere string) ([]ExpiringItem, error) {
	rows, err := db.Query(
		`SELECT w.id, w.name, w.domain, w.expiry_date, COALESCE(s.name, ''),
		 CAST(julianday(w.expiry_date) - julianday(date('now')) AS INTEGER) AS days_left
		 FROM websites w
		 LEFT JOIN servers s ON w.server_id = s.id
		 WHERE w.expiry_date != '' AND w.status = 'active' AND ` + extraWhere + `
		 ORDER BY w.expiry_date ASC LIMIT 10`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := []ExpiringItem{}
	for rows.Next() {
		var item ExpiringItem
		var domain string
		if err := rows.Scan(&item.ID, &item.Name, &domain, &item.ExpiryDate, &item.ServerName, &item.DaysLeft); err != nil {
			return nil, err
		}
		if item.Name == "" {
			item.Name = domain
		}
		item.DetailPath = "/websites/" + strconv.FormatInt(item.ID, 10)
		items = append(items, item)
	}
	return items, rows.Err()
}

// GetExpiring returns two independent, independently-limited buckets per
// entity type: items expiring in the next 30 days, and items already past
// their expiry date and still active. Merging both into one ORDER BY +
// LIMIT list would let a pile-up of long-overdue records crowd out ones
// that are genuinely about to expire soon.
func (h *DashboardHandler) GetExpiring(c *gin.Context) {
	db := database.GetDB()

	expiringServers, err := queryExpiringServers(db, "expiry_date >= date('now') AND expiry_date <= date('now','+30 days')")
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse("获取即将到期服务器失败"))
		return
	}
	overdueServers, err := queryExpiringServers(db, "expiry_date < date('now')")
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse("获取已到期服务器失败"))
		return
	}
	expiringWebsites, err := queryExpiringWebsites(db, "w.expiry_date >= date('now') AND w.expiry_date <= date('now','+30 days')")
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse("获取即将到期网站失败"))
		return
	}
	overdueWebsites, err := queryExpiringWebsites(db, "w.expiry_date < date('now')")
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse("获取已到期网站失败"))
		return
	}

	c.JSON(http.StatusOK, models.SuccessResponse(map[string]interface{}{
		"servers":          expiringServers,
		"websites":         expiringWebsites,
		"overdue_servers":  overdueServers,
		"overdue_websites": overdueWebsites,
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

type hostMetricsLatestResponse struct {
	models.MetricSample
	Hostname string `json:"hostname"`
	CPUCores int    `json:"cpu_cores"`
}

// GetHostMetricsLatest returns the most recent sample of the machine the
// panel process itself runs on (see executor/host_metrics.go), plus static
// host info for the Dashboard's performance-section header.
func (h *DashboardHandler) GetHostMetricsLatest(c *gin.Context) {
	db := database.GetDB()

	var resp hostMetricsLatestResponse
	err := db.QueryRow(
		`SELECT cpu_percent, memory_percent, memory_used, memory_total,
		 disk_percent, disk_used, disk_total, net_rx_bytes, net_tx_bytes,
		 load_avg_1, load_avg_5, load_avg_15, uptime_seconds, recorded_at
		 FROM host_metrics ORDER BY recorded_at DESC LIMIT 1`,
	).Scan(&resp.CPUPercent, &resp.MemoryPercent, &resp.MemoryUsed, &resp.MemoryTotal,
		&resp.DiskPercent, &resp.DiskUsed, &resp.DiskTotal, &resp.NetRXBytes, &resp.NetTXBytes,
		&resp.LoadAvg1, &resp.LoadAvg5, &resp.LoadAvg15, &resp.UptimeSeconds, &resp.RecordedAt)
	if err != nil {
		c.JSON(http.StatusNotFound, models.ErrorResponse("暂无性能数据"))
		return
	}

	resp.Hostname, _ = os.Hostname()
	resp.CPUCores = runtime.NumCPU()

	c.JSON(http.StatusOK, models.SuccessResponse(resp))
}

// GetHostMetrics returns bucketed history for the panel's own host, same
// range/bucketing scheme as MetricsHandler.GetServerMetrics (handlers/metrics.go)
// and the same MetricPoint JSON shape, so the Dashboard reuses the exact
// same chart-rendering JS as templates/monitor_detail.html.
func (h *DashboardHandler) GetHostMetrics(c *gin.Context) {
	db := database.GetDB()

	rangeParam := c.DefaultQuery("range", "24h")
	since := time.Now().Add(-24 * time.Hour)
	bucketMinutes := 1
	switch rangeParam {
	case "7d":
		since = time.Now().Add(-7 * 24 * time.Hour)
		bucketMinutes = 5
	case "15d":
		since = time.Now().Add(-15 * 24 * time.Hour)
		bucketMinutes = 5
	case "30d":
		since = time.Now().Add(-30 * 24 * time.Hour)
		bucketMinutes = 5
	}

	rows, err := db.Query(
		`SELECT recorded_at, cpu_percent, memory_percent, disk_percent, load_avg_1, load_avg_5, load_avg_15,
		 net_rx_bytes, net_tx_bytes
		 FROM host_metrics WHERE recorded_at >= ? ORDER BY recorded_at`,
		timeutil.Display(since))
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse("查询失败"))
		return
	}
	defer rows.Close()

	rawPoints := []MetricPoint{}
	for rows.Next() {
		var p MetricPoint
		var netRX, netTX sql.NullInt64
		var timeStr string
		if err := rows.Scan(&timeStr, &p.CPU, &p.Memory, &p.Disk, &p.Load1, &p.Load5, &p.Load15, &netRX, &netTX); err != nil {
			c.JSON(http.StatusInternalServerError, models.ErrorResponse("读取性能数据失败"))
			return
		}
		p.Time = timeStr
		if netRX.Valid {
			p.NetRX = netRX.Int64
		}
		if netTX.Valid {
			p.NetTX = netTX.Int64
		}
		rawPoints = append(rawPoints, p)
	}

	if bucketMinutes > 1 {
		rawPoints = bucketMetricPoints(rawPoints, bucketMinutes)
	}

	c.JSON(http.StatusOK, models.SuccessResponse(rawPoints))
}
