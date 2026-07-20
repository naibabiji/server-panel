package handlers

import (
	"database/sql"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/naibabiji/server-panel/database"
	"github.com/naibabiji/server-panel/models"
	"github.com/naibabiji/server-panel/timeutil"
)

type MetricsHandler struct {
	DB *sql.DB
}

func (h *MetricsHandler) GetOverview(c *gin.Context) {
	db := h.db()

	rows, err := db.Query(
		`SELECT s.id, s.name, s.ip_address, s.is_online, s.last_seen_at,
		 COALESCE(s.provider_id, 0), COALESCE(p.name, ''),
		 COALESCE(s.agent_version,''), s.http_probe_enabled, s.http_probe_healthy, s.http_probe_last_at,
		 s.tcp_reachable,
		 m.cpu_percent, m.memory_percent, m.disk_percent, m.load_avg_1, m.uptime_seconds, m.recorded_at,
		 m.net_rx_bytes, m.net_tx_bytes
		 FROM servers s
		 LEFT JOIN providers p ON s.provider_id = p.id
		 LEFT JOIN (
		     SELECT server_id, cpu_percent, memory_percent, disk_percent, load_avg_1, uptime_seconds, recorded_at,
		            net_rx_bytes, net_tx_bytes,
		            ROW_NUMBER() OVER (PARTITION BY server_id ORDER BY recorded_at DESC) rn
		     FROM metrics
		 ) m ON s.id = m.server_id AND m.rn = 1
		 WHERE s.agent_version != '' OR s.last_seen_at IS NOT NULL OR m.recorded_at IS NOT NULL
		 ORDER BY COALESCE(p.name, '未设置'), s.name`)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse("查询失败"))
		return
	}
	defer rows.Close()

	type OverviewItem struct {
		ID               int64   `json:"id"`
		Name             string  `json:"name"`
		IPAddress        string  `json:"ip_address"`
		IsOnline         bool    `json:"is_online"`
		LastSeenAt       string  `json:"last_seen_at"`
		AgentVersion     string  `json:"agent_version"`
		ProviderID       int64   `json:"provider_id"`
		ProviderName     string  `json:"provider_name"`
		HasAgent         bool    `json:"has_agent"`
		HTTPProbeEnabled int     `json:"http_probe_enabled"`
		HTTPProbeHealthy *int    `json:"http_probe_healthy"`
		HTTPProbeLastAt  string  `json:"http_probe_last_at"`
		TCPReachable     *int    `json:"tcp_reachable"`
		CPUPercent       float64 `json:"cpu_percent"`
		MemoryPercent    float64 `json:"memory_percent"`
		DiskPercent      float64 `json:"disk_percent"`
		NetRXBytes       int64   `json:"net_rx_bytes"`
		NetTXBytes       int64   `json:"net_tx_bytes"`
		LoadAvg1         float64 `json:"load_avg_1"`
		UptimeSeconds    int64   `json:"uptime_seconds"`
		RecordedAt       string  `json:"recorded_at"`
	}

	items := []OverviewItem{}
	for rows.Next() {
		var item OverviewItem
		var probeHealthy, tcpReachable sql.NullInt64
		var lastSeen, probeLast, agentVer, recorded sql.NullString
		var cpu, mem, disk, load sql.NullFloat64
		var uptime sql.NullInt64
		var netRX, netTX sql.NullInt64
		err := rows.Scan(&item.ID, &item.Name, &item.IPAddress, &item.IsOnline, &lastSeen,
			&item.ProviderID, &item.ProviderName, &agentVer, &item.HTTPProbeEnabled, &probeHealthy, &probeLast,
			&tcpReachable,
			&cpu, &mem, &disk, &load, &uptime, &recorded,
			&netRX, &netTX)
		if err != nil {
			c.JSON(http.StatusInternalServerError, models.ErrorResponse("读取监控概览失败"))
			return
		}
		if probeHealthy.Valid {
			v := int(probeHealthy.Int64)
			item.HTTPProbeHealthy = &v
		}
		if tcpReachable.Valid {
			v := int(tcpReachable.Int64)
			item.TCPReachable = &v
		}
		if lastSeen.Valid {
			item.LastSeenAt = lastSeen.String
		}
		if probeLast.Valid {
			item.HTTPProbeLastAt = probeLast.String
		}
		if agentVer.Valid {
			item.AgentVersion = agentVer.String
		}
		item.HasAgent = item.AgentVersion != ""
		if cpu.Valid {
			item.CPUPercent = cpu.Float64
		}
		if mem.Valid {
			item.MemoryPercent = mem.Float64
		}
		if disk.Valid {
			item.DiskPercent = disk.Float64
		}
		if load.Valid {
			item.LoadAvg1 = load.Float64
		}
		if uptime.Valid {
			item.UptimeSeconds = uptime.Int64
		}
		if recorded.Valid {
			item.RecordedAt = recorded.String
		}
		if netRX.Valid {
			item.NetRXBytes = netRX.Int64
		}
		if netTX.Valid {
			item.NetTXBytes = netTX.Int64
		}
		items = append(items, item)
	}

	c.JSON(http.StatusOK, models.SuccessResponse(items))
}

func (h *MetricsHandler) GetServerMetrics(c *gin.Context) {
	serverID := c.Param("id")
	if _, err := strconv.ParseInt(serverID, 10, 64); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse("无效的服务器 ID"))
		return
	}

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

	rows, err := h.db().Query(
		`SELECT recorded_at, cpu_percent, memory_percent, disk_percent, load_avg_1, load_avg_5, load_avg_15,
		 net_rx_bytes, net_tx_bytes
		 FROM metrics WHERE server_id = ? AND recorded_at >= ? ORDER BY recorded_at`,
		serverID, timeutil.Display(since))
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

func (h *MetricsHandler) GetLatest(c *gin.Context) {
	serverID := c.Param("id")
	parsedServerID, err := strconv.ParseInt(serverID, 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse("无效的服务器 ID"))
		return
	}

	var m models.MetricSample
	err = h.db().QueryRow(
		`SELECT cpu_percent, memory_percent, memory_used, memory_total,
		 disk_percent, disk_used, disk_total, net_rx_bytes, net_tx_bytes,
		 load_avg_1, load_avg_5, load_avg_15, uptime_seconds, recorded_at
		 FROM metrics WHERE server_id = ? ORDER BY recorded_at DESC LIMIT 1`, parsedServerID,
	).Scan(&m.CPUPercent, &m.MemoryPercent, &m.MemoryUsed, &m.MemoryTotal,
		&m.DiskPercent, &m.DiskUsed, &m.DiskTotal, &m.NetRXBytes, &m.NetTXBytes,
		&m.LoadAvg1, &m.LoadAvg5, &m.LoadAvg15, &m.UptimeSeconds, &m.RecordedAt)
	if err != nil {
		c.JSON(http.StatusNotFound, models.ErrorResponse("暂无性能数据"))
		return
	}
	m.ServerID = parsedServerID

	c.JSON(http.StatusOK, models.SuccessResponse(m))
}

func (h *MetricsHandler) db() *sql.DB {
	if h.DB != nil {
		return h.DB
	}
	return database.GetDB()
}

type MetricPoint struct {
	Time   string  `json:"t"`
	CPU    float64 `json:"cpu"`
	Memory float64 `json:"mem"`
	Disk   float64 `json:"disk"`
	Load1  float64 `json:"load1"`
	Load5  float64 `json:"load5"`
	Load15 float64 `json:"load15"`
	NetRX  int64   `json:"rx"`
	NetTX  int64   `json:"tx"`
}

type metricBucket struct {
	point MetricPoint
	count int
}

func bucketMetricPoints(points []MetricPoint, bucketMinutes int) []MetricPoint {
	bucketSeconds := int64(bucketMinutes * 60)
	buckets := make(map[int64]*metricBucket)
	order := []int64{}

	for _, p := range points {
		t, err := time.ParseInLocation("2006-01-02 15:04:05", p.Time, time.UTC)
		if err != nil {
			continue
		}
		key := (t.Unix() / bucketSeconds) * bucketSeconds
		b, ok := buckets[key]
		if !ok {
			b = &metricBucket{point: MetricPoint{Time: timeutil.Display(time.Unix(key, 0))}}
			buckets[key] = b
			order = append(order, key)
		}
		b.point.CPU += p.CPU
		b.point.Memory += p.Memory
		b.point.Disk += p.Disk
		b.point.Load1 += p.Load1
		b.point.Load5 += p.Load5
		b.point.Load15 += p.Load15
		b.point.NetRX += p.NetRX
		b.point.NetTX += p.NetTX
		b.count++
	}

	result := make([]MetricPoint, 0, len(order))
	for _, key := range order {
		b := buckets[key]
		if b.count == 0 {
			continue
		}
		b.point.CPU /= float64(b.count)
		b.point.Memory /= float64(b.count)
		b.point.Disk /= float64(b.count)
		b.point.Load1 /= float64(b.count)
		b.point.Load5 /= float64(b.count)
		b.point.Load15 /= float64(b.count)
		result = append(result, b.point)
	}
	return result
}
