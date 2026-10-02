package handlers

import (
	"database/sql"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/naibabiji/server-panel/i18n"
	"github.com/naibabiji/server-panel/models"
	"github.com/naibabiji/server-panel/releasecheck"
)

type AgentDataHandler struct {
	DB *sql.DB
	// PanelVersion is the version every update-capable Agent is told to
	// follow (see agentUpdateTarget).
	PanelVersion string
}

// maxAgentUpdateErrorLen bounds the Agent-reported update error stored per
// server; it is shown in the panel, not parsed.
const maxAgentUpdateErrorLen = 500

// agentUpdateTarget returns the version an Agent should self-update to, or
// "" for none. Agents always follow the panel's own release version, never
// downgrade, and only receive a target when they report a working updater.
// Dev/prerelease panel builds never issue targets.
func agentUpdateTarget(panelVersion, agentVersion string, autoUpdate bool) string {
	if !autoUpdate || !releasecheck.IsStableVersion(panelVersion) {
		return ""
	}
	if releasecheck.CompareVersions(panelVersion, agentVersion) <= 0 {
		return ""
	}
	return panelVersion
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
		     agent_version = '', agent_auto_update = 0, agent_update_error = '',
		     last_seen_at = NULL, is_online = 0,
		     tcp_reachable = NULL, tcp_reachable_checked_at = NULL,
		     updated_at = CURRENT_TIMESTAMP
		 WHERE id = ?`,
		serverID,
	)
	c.JSON(http.StatusOK, models.SuccessResponse(map[string]string{
		"message": i18n.TE(c.Request, "errors.agent.marked_uninstalled"),
	}))
}

func (h *AgentDataHandler) ReceiveMetrics(c *gin.Context) {
	serverID, _ := c.Get("agent_server_id")
	reqTime, _ := c.Get("agent_request_time")

	var payload models.AgentMetricPayload
	if err := c.ShouldBindJSON(&payload); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse(i18n.TE(c.Request, "errors.agent.invalid_metrics")))
		return
	}

	// 更新 agent 版本与自更新状态
	if payload.AgentVersion != "" {
		updateError := payload.UpdateError
		if len(updateError) > maxAgentUpdateErrorLen {
			updateError = strings.ToValidUTF8(updateError[:maxAgentUpdateErrorLen], "")
		}
		h.DB.Exec("UPDATE servers SET agent_version = ?, agent_auto_update = ?, agent_update_error = ? WHERE id = ?",
			payload.AgentVersion, payload.AutoUpdate, updateError, serverID)
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
		c.JSON(http.StatusInternalServerError, models.ErrorResponse(i18n.TE(c.Request, "errors.agent.write_metrics_failed")))
		return
	}

	resp := map[string]interface{}{
		"server_time": time.Now().UTC().Format(time.RFC3339),
	}
	if target := agentUpdateTarget(h.PanelVersion, payload.AgentVersion, payload.AutoUpdate); target != "" {
		resp["agent_update"] = map[string]string{"version": target}
	}
	c.JSON(http.StatusOK, models.SuccessResponse(resp))
}
