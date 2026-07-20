package handlers

import (
	"database/sql"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/naibabiji/server-panel/models"
)

type AgentDataHandler struct {
	DB *sql.DB
}

func (h *AgentDataHandler) Ping(c *gin.Context) {
	serverID, _ := c.Get("agent_server_id")
	c.JSON(http.StatusOK, models.SuccessResponse(map[string]interface{}{
		"server_id":   serverID,
		"server_time": time.Now().UTC().Format(time.RFC3339),
	}))
}

func (h *AgentDataHandler) Uninstall(c *gin.Context) {
	serverID, _ := c.Get("agent_server_id")
	_, _ = h.DB.Exec(
		`UPDATE servers
		 SET agent_api_key_hash = '', agent_api_key_enc = '',
		     agent_version = '', last_seen_at = NULL, is_online = 0,
		     tcp_reachable = NULL, tcp_reachable_checked_at = NULL,
		     updated_at = CURRENT_TIMESTAMP
		 WHERE id = ?`,
		serverID,
	)
	c.JSON(http.StatusOK, models.SuccessResponse(map[string]string{
		"message": "Agent 已标记为卸载",
	}))
}

func (h *AgentDataHandler) ReceiveMetrics(c *gin.Context) {
	serverID, _ := c.Get("agent_server_id")
	reqTime, _ := c.Get("agent_request_time")

	var payload models.AgentMetricPayload
	if err := c.ShouldBindJSON(&payload); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse("无效的指标数据"))
		return
	}

	// 更新 agent 版本
	if payload.AgentVersion != "" {
		h.DB.Exec("UPDATE servers SET agent_version = ? WHERE id = ?", payload.AgentVersion, serverID)
	}

	// 计算接收延迟
	var ingestLatency int64
	if t, ok := reqTime.(time.Time); ok {
		ingestLatency = time.Since(t).Microseconds()
	}

	// 写入指标
	_, err := h.DB.Exec(
		`INSERT INTO metrics (server_id, cpu_percent, memory_percent, memory_used, memory_total,
		 disk_percent, disk_used, disk_total, net_rx_bytes, net_tx_bytes,
		 load_avg_1, load_avg_5, load_avg_15, uptime_seconds, ingest_latency_us)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		serverID,
		payload.CPUPercent, payload.MemoryPercent, payload.MemoryUsed, payload.MemoryTotal,
		payload.DiskPercent, payload.DiskUsed, payload.DiskTotal, payload.NetRXBytes, payload.NetTXBytes,
		payload.LoadAvg1, payload.LoadAvg5, payload.LoadAvg15, payload.UptimeSeconds,
		ingestLatency,
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse("写入指标失败"))
		return
	}

	c.JSON(http.StatusOK, models.SuccessResponse(map[string]interface{}{
		"server_time": time.Now().UTC().Format(time.RFC3339),
	}))
}
