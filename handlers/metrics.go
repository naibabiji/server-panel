package handlers

import (
	"database/sql"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/naibabiji/server-panel/database"
	"github.com/naibabiji/server-panel/models"
)

type MetricsHandler struct {
	DB *sql.DB
}

func (h *MetricsHandler) GetOverview(c *gin.Context) {
	db := h.DB
	if db == nil {
		db = database.GetDB()
	}

	rows, err := db.Query(
		`SELECT s.id, s.name, s.ip_address, s.is_online, s.last_seen_at,
		 COALESCE(s.agent_version,''), s.http_probe_enabled, s.http_probe_healthy, s.http_probe_last_at,
		 m.cpu_percent, m.memory_percent, m.disk_percent, m.load_avg_1, m.uptime_seconds, m.recorded_at
		 FROM servers s
		 LEFT JOIN (
		     SELECT server_id, cpu_percent, memory_percent, disk_percent, load_avg_1, uptime_seconds, recorded_at,
		            ROW_NUMBER() OVER (PARTITION BY server_id ORDER BY recorded_at DESC) rn
		     FROM metrics
		 ) m ON s.id = m.server_id AND m.rn = 1
		 ORDER BY s.name`)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse("查询失败"))
		return
	}
	defer rows.Close()

	type OverviewItem struct {
		ID                int64   `json:"id"`
		Name              string  `json:"name"`
		IPAddress         string  `json:"ip_address"`
		IsOnline          bool    `json:"is_online"`
		LastSeenAt        string  `json:"last_seen_at"`
		AgentVersion      string  `json:"agent_version"`
		HTTPProbeEnabled  int     `json:"http_probe_enabled"`
		HTTPProbeHealthy  *int    `json:"http_probe_healthy"`
		HTTPProbeLastAt   string  `json:"http_probe_last_at"`
		CPUPercent        float64 `json:"cpu_percent"`
		MemoryPercent     float64 `json:"memory_percent"`
		DiskPercent       float64 `json:"disk_percent"`
		LoadAvg1          float64 `json:"load_avg_1"`
		UptimeSeconds     int64   `json:"uptime_seconds"`
		RecordedAt        string  `json:"recorded_at"`
	}

	items := []OverviewItem{}
	for rows.Next() {
		var item OverviewItem
		var probeHealthy sql.NullInt64
		var lastSeen, probeLast, agentVer, recorded sql.NullString
		var cpu, mem, disk, load sql.NullFloat64
		var uptime sql.NullInt64
		rows.Scan(&item.ID, &item.Name, &item.IPAddress, &item.IsOnline, &lastSeen,
			&agentVer, &item.HTTPProbeEnabled, &probeHealthy, &probeLast,
			&cpu, &mem, &disk, &load, &uptime, &recorded)
		if probeHealthy.Valid {
			v := int(probeHealthy.Int64)
			item.HTTPProbeHealthy = &v
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
		items = append(items, item)
	}

	if items == nil {
		items = []OverviewItem{}
	}

	c.JSON(http.StatusOK, models.SuccessResponse(items))
}

func (h *MetricsHandler) GetServerMetrics(c *gin.Context) {
	serverID := c.Param("id")
	rangeParam := c.DefaultQuery("range", "24h")

	var since time.Time
	switch rangeParam {
	case "7d":
		since = time.Now().Add(-7 * 24 * time.Hour)
	case "15d":
		since = time.Now().Add(-15 * 24 * time.Hour)
	case "30d":
		since = time.Now().Add(-30 * 24 * time.Hour)
	default:
		since = time.Now().Add(-24 * time.Hour)
	}

	// 下采样：超过 24h 范围聚合到 5 分钟粒度
	bucketMinutes := 1
	if rangeParam != "24h" {
		bucketMinutes = 5
	}

	db := h.DB
	if db == nil {
		db = database.GetDB()
	}

	rows, err := db.Query(
		`SELECT recorded_at, cpu_percent, memory_percent, disk_percent, load_avg_1, load_avg_5, load_avg_15,
		 net_rx_bytes, net_tx_bytes
		 FROM metrics WHERE server_id = ? AND recorded_at >= ? ORDER BY recorded_at`,
		serverID, since.Format("2006-01-02 15:04:05"))
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse("查询失败"))
		return
	}
	defer rows.Close()

	type MetricPoint struct {
		Time         string  `json:"t"`
		CPU          float64 `json:"cpu"`
		Memory       float64 `json:"mem"`
		Disk         float64 `json:"disk"`
		Load1        float64 `json:"load1"`
		Load5        float64 `json:"load5"`
		Load15       float64 `json:"load15"`
		NetRX        int64   `json:"rx"`
		NetTX        int64   `json:"tx"`
	}

	rawPoints := []MetricPoint{}
	for rows.Next() {
		var p MetricPoint
		var netRX, netTX sql.NullInt64
		var timeStr string
		rows.Scan(&timeStr, &p.CPU, &p.Memory, &p.Disk, &p.Load1, &p.Load5, &p.Load15, &netRX, &netTX)
		p.Time = timeStr
		if netRX.Valid {
			p.NetRX = netRX.Int64
		}
		if netTX.Valid {
			p.NetTX = netTX.Int64
		}
		rawPoints = append(rawPoints, p)
	}

	// 下采样聚合
	if bucketMinutes > 1 && len(rawPoints) > 0 {
		result := []MetricPoint{}
		bucket := MetricPoint{}
		count := 0
		lastBucket := ""

		for _, p := range rawPoints {
			bucketKey := p.Time[:16] // YYYY-MM-DD HH:MM
			if lastBucket != "" && bucketKey[:len(bucketKey)-1] != lastBucket[:len(lastBucket)-1] {
				bucket.CPU /= float64(count)
				bucket.Memory /= float64(count)
				bucket.Disk /= float64(count)
				bucket.Load1 /= float64(count)
				bucket.Load5 /= float64(count)
				bucket.Load15 /= float64(count)
				bucket.Time = lastBucket
				result = append(result, bucket)
				bucket = MetricPoint{}
				count = 0
			}
			bucket.CPU += p.CPU
			bucket.Memory += p.Memory
			bucket.Disk += p.Disk
			bucket.Load1 += p.Load1
			bucket.Load5 += p.Load5
			bucket.Load15 += p.Load15
			bucket.NetRX += p.NetRX
			bucket.NetTX += p.NetTX
			count++
			lastBucket = bucketKey
		}
		if count > 0 {
			bucket.CPU /= float64(count)
			bucket.Memory /= float64(count)
			bucket.Disk /= float64(count)
			bucket.Load1 /= float64(count)
			bucket.Load5 /= float64(count)
			bucket.Load15 /= float64(count)
			bucket.Time = lastBucket
			result = append(result, bucket)
		}
		rawPoints = result
	}

	if rawPoints == nil {
		rawPoints = []MetricPoint{}
	}

	c.JSON(http.StatusOK, models.SuccessResponse(rawPoints))
}

func (h *MetricsHandler) GetLatest(c *gin.Context) {
	serverID := c.Param("id")
	db := h.DB
	if db == nil {
		db = database.GetDB()
	}

	var m models.MetricSample
	err := db.QueryRow(
		`SELECT cpu_percent, memory_percent, memory_used, memory_total,
		 disk_percent, disk_used, disk_total, net_rx_bytes, net_tx_bytes,
		 load_avg_1, load_avg_5, load_avg_15, uptime_seconds, recorded_at
		 FROM metrics WHERE server_id = ? ORDER BY recorded_at DESC LIMIT 1`, serverID,
	).Scan(&m.CPUPercent, &m.MemoryPercent, &m.MemoryUsed, &m.MemoryTotal,
		&m.DiskPercent, &m.DiskUsed, &m.DiskTotal, &m.NetRXBytes, &m.NetTXBytes,
		&m.LoadAvg1, &m.LoadAvg5, &m.LoadAvg15, &m.UptimeSeconds, &m.RecordedAt)
	if err != nil {
		c.JSON(http.StatusNotFound, models.ErrorResponse("暂无性能数据"))
		return
	}
	m.ServerID, _ = parseInt64(serverID)

	c.JSON(http.StatusOK, models.SuccessResponse(m))
}

func parseInt64(s string) (int64, error) {
	var i int64
	for _, c := range s {
		if c >= '0' && c <= '9' {
			i = i*10 + int64(c-'0')
		}
	}
	return i, nil
}
