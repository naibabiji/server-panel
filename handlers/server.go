package handlers

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/naibabiji/server-panel/models"
)

type ServerHandler struct {
	DB *sql.DB
}

func (h *ServerHandler) List(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	search := c.Query("search")
	status := c.Query("status")
	userID := c.Query("user_id")
	providerID := c.Query("provider_id")
	serverType := c.Query("server_type")

	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}

	where := "WHERE 1=1"
	args := []interface{}{}
	if search != "" {
		where += " AND (s.name LIKE ? OR s.ip_address LIKE ? OR s.location LIKE ?)"
		s := "%" + search + "%"
		args = append(args, s, s, s)
	}
	if status != "" {
		where += " AND s.status = ?"
		args = append(args, status)
	}
	if userID != "" {
		where += " AND s.user_id = ?"
		args = append(args, userID)
	}
	if providerID != "" {
		where += " AND s.provider_id = ?"
		args = append(args, providerID)
	}
	if serverType != "" {
		where += " AND s.server_type = ?"
		args = append(args, serverType)
	}

	var total int
	h.DB.QueryRow("SELECT COUNT(*) FROM servers s "+where, args...).Scan(&total)

	offset := (page - 1) * pageSize
	query := `SELECT s.id, s.name, s.ip_address, s.server_type, s.os, s.user_id, COALESCE(u.name,''),
		s.cpu, s.ram, s.disk, s.bandwidth, s.provider_id, COALESCE(p.name,''),
		s.location, s.ssh_port, s.ssh_username, s.panel_type, s.panel_url, s.panel_username,
		s.purchase_date, s.expiry_date, s.renewal_cycle, s.auto_renewal, s.purchase_price, s.currency,
		s.status, s.agent_version, s.last_seen_at, s.is_online,
		s.http_probe_enabled, s.http_probe_healthy, s.http_probe_last_at, s.http_probe_last_error,
		s.status_page_enabled, s.notes, s.created_at, s.updated_at
		FROM servers s
		LEFT JOIN users u ON s.user_id = u.id
		LEFT JOIN providers p ON s.provider_id = p.id ` +
		where + " ORDER BY s.created_at DESC LIMIT ? OFFSET ?"
	args = append(args, pageSize, offset)

	rows, err := h.DB.Query(query, args...)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse("查询失败"))
		return
	}
	defer rows.Close()

	servers := []models.Server{}
	for rows.Next() {
		var s models.Server
		var probeHealthy sql.NullInt64
		var lastSeen, probeLast sql.NullString
		rows.Scan(&s.ID, &s.Name, &s.IPAddress, &s.ServerType, &s.OS, &s.UserID, &s.UserName,
			&s.CPU, &s.RAM, &s.Disk, &s.Bandwidth, &s.ProviderID, &s.ProviderName,
			&s.Location, &s.SSHPort, &s.SSHUsername, &s.PanelType, &s.PanelURL, &s.PanelUsername,
			&s.PurchaseDate, &s.ExpiryDate, &s.RenewalCycle, &s.AutoRenewal, &s.PurchasePrice, &s.Currency,
			&s.Status, &s.AgentVersion, &lastSeen, &s.IsOnline,
			&s.HTTPProbeEnabled, &probeHealthy, &probeLast, &s.HTTPProbeLastError,
			&s.StatusPageEnabled, &s.Notes, &s.CreatedAt, &s.UpdatedAt)
		if probeHealthy.Valid {
			v := int(probeHealthy.Int64)
			s.HTTPProbeHealthy = &v
		}
		if lastSeen.Valid {
			s.LastSeenAt = lastSeen.String
		}
		if probeLast.Valid {
			s.HTTPProbeLastAt = probeLast.String
		}
		servers = append(servers, s)
	}

	c.JSON(http.StatusOK, models.SuccessResponse(models.PaginatedResult{
		Items: servers, Total: total, Page: page, PageSize: pageSize,
	}))
}

func (h *ServerHandler) Get(c *gin.Context) {
	id := c.Param("id")
	var s models.Server
	var probeHealthy sql.NullInt64
	var lastSeen, probeLast sql.NullString

	err := h.DB.QueryRow(
		`SELECT s.id, s.name, s.ip_address, s.server_type, s.os, s.user_id, COALESCE(u.name,''),
		s.cpu, s.ram, s.disk, s.bandwidth, s.provider_id, COALESCE(p.name,''),
		s.location, s.ssh_port, s.ssh_username, s.ssh_password_enc, s.panel_type, s.panel_url, s.panel_username,
		s.panel_password_enc, s.purchase_date, s.expiry_date, s.renewal_cycle, s.auto_renewal, s.purchase_price, s.currency,
		s.status, s.agent_version, s.last_seen_at, s.is_online,
		s.http_probe_enabled, s.http_probe_healthy, s.http_probe_last_at, s.http_probe_last_error,
		s.status_page_enabled, s.status_page_token, s.notes, s.created_at, s.updated_at
		FROM servers s
		LEFT JOIN users u ON s.user_id = u.id
		LEFT JOIN providers p ON s.provider_id = p.id
		WHERE s.id = ?`, id,
	).Scan(&s.ID, &s.Name, &s.IPAddress, &s.ServerType, &s.OS, &s.UserID, &s.UserName,
		&s.CPU, &s.RAM, &s.Disk, &s.Bandwidth, &s.ProviderID, &s.ProviderName,
		&s.Location, &s.SSHPort, &s.SSHUsername, &s.SSHPasswordEnc, &s.PanelType, &s.PanelURL, &s.PanelUsername,
		&s.PanelPasswordEnc, &s.PurchaseDate, &s.ExpiryDate, &s.RenewalCycle, &s.AutoRenewal, &s.PurchasePrice, &s.Currency,
		&s.Status, &s.AgentVersion, &lastSeen, &s.IsOnline,
		&s.HTTPProbeEnabled, &probeHealthy, &probeLast, &s.HTTPProbeLastError,
		&s.StatusPageEnabled, &s.StatusPageToken, &s.Notes, &s.CreatedAt, &s.UpdatedAt)
	if err != nil {
		c.JSON(http.StatusNotFound, models.ErrorResponse("服务器不存在"))
		return
	}
	if probeHealthy.Valid {
		v := int(probeHealthy.Int64)
		s.HTTPProbeHealthy = &v
	}
	if lastSeen.Valid {
		s.LastSeenAt = lastSeen.String
	}
	if probeLast.Valid {
		s.HTTPProbeLastAt = probeLast.String
	}

	c.JSON(http.StatusOK, models.SuccessResponse(s))
}

func (h *ServerHandler) Create(c *gin.Context) {
	var s models.Server
	if err := c.ShouldBindJSON(&s); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse("无效的请求数据"))
		return
	}
	if s.Name == "" {
		c.JSON(http.StatusBadRequest, models.ErrorResponse("服务器名称不能为空"))
		return
	}

	// 生成 Agent API Key
	agentKey := make([]byte, 32)
	rand.Read(agentKey)
	agentKeyStr := hex.EncodeToString(agentKey)
	agentKeyHash := sha256.Sum256([]byte(agentKeyStr))
	agentKeyHashStr := hex.EncodeToString(agentKeyHash[:])

	// 加密 SSH 密码
	var sshPasswordEnc string
	if s.SSHPasswordEnc != "" {
		token, _ := c.Get("session_token")
		key := GetDerivedKey(token.(string))
		if key != nil {
			enc, err := EncryptPassword(s.SSHPasswordEnc, key)
			if err == nil {
				sshPasswordEnc = enc
			}
		}
	}

	// 加密面板密码
	var panelPasswordEnc string
	if s.PanelPasswordEnc != "" {
		token, _ := c.Get("session_token")
		key := GetDerivedKey(token.(string))
		if key != nil {
			enc, err := EncryptPassword(s.PanelPasswordEnc, key)
			if err == nil {
				panelPasswordEnc = enc
			}
		}
	}

	result, err := h.DB.Exec(
		`INSERT INTO servers (name, ip_address, server_type, os, user_id, cpu, ram, disk, bandwidth,
		 provider_id, location, ssh_port, ssh_username, ssh_password_enc, panel_type, panel_url,
		 panel_username, panel_password_enc, purchase_date, expiry_date, renewal_cycle,
		 auto_renewal, purchase_price, currency, status, agent_api_key_hash, http_probe_enabled, notes)
		 VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		s.Name, s.IPAddress, s.ServerType, s.OS, s.UserID, s.CPU, s.RAM, s.Disk, s.Bandwidth,
		s.ProviderID, s.Location, s.SSHPort, s.SSHUsername, sshPasswordEnc, s.PanelType, s.PanelURL,
		s.PanelUsername, panelPasswordEnc, s.PurchaseDate, s.ExpiryDate, s.RenewalCycle,
		s.AutoRenewal, s.PurchasePrice, s.Currency, s.Status, agentKeyHashStr, s.HTTPProbeEnabled, s.Notes,
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse("创建失败: "+err.Error()))
		return
	}

	id, _ := result.LastInsertId()
	c.JSON(http.StatusOK, models.SuccessResponse(map[string]interface{}{
		"id":         id,
		"agent_key":  agentKeyStr,
		"agent_key_note": "请立即保存此密钥，之后无法再次查看",
	}))
}

func (h *ServerHandler) Update(c *gin.Context) {
	id := c.Param("id")

	var s models.Server
	if err := c.ShouldBindJSON(&s); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse("无效的请求数据"))
		return
	}

	// 处理 SSH 密码加密（如果提供了新密码）
	sshPasswordEnc := ""
	if s.SSHPasswordEnc != "" {
		token, _ := c.Get("session_token")
		key := GetDerivedKey(token.(string))
		if key != nil {
			enc, err := EncryptPassword(s.SSHPasswordEnc, key)
			if err == nil {
				sshPasswordEnc = enc
			}
		}
	}

	// 处理面板密码加密
	panelPasswordEnc := ""
	if s.PanelPasswordEnc != "" {
		token, _ := c.Get("session_token")
		key := GetDerivedKey(token.(string))
		if key != nil {
			enc, err := EncryptPassword(s.PanelPasswordEnc, key)
			if err == nil {
				panelPasswordEnc = enc
			}
		}
	}

	query := `UPDATE servers SET name=?, ip_address=?, server_type=?, os=?, user_id=?,
		cpu=?, ram=?, disk=?, bandwidth=?, provider_id=?, location=?,
		ssh_port=?, ssh_username=?, panel_type=?, panel_url=?, panel_username=?,
		purchase_date=?, expiry_date=?, renewal_cycle=?, auto_renewal=?,
		purchase_price=?, currency=?, status=?, http_probe_enabled=?, notes=?, updated_at=CURRENT_TIMESTAMP`
	args := []interface{}{s.Name, s.IPAddress, s.ServerType, s.OS, s.UserID,
		s.CPU, s.RAM, s.Disk, s.Bandwidth, s.ProviderID, s.Location,
		s.SSHPort, s.SSHUsername, s.PanelType, s.PanelURL, s.PanelUsername,
		s.PurchaseDate, s.ExpiryDate, s.RenewalCycle, s.AutoRenewal,
		s.PurchasePrice, s.Currency, s.Status, s.HTTPProbeEnabled, s.Notes}

	if sshPasswordEnc != "" {
		query += ", ssh_password_enc=?"
		args = append(args, sshPasswordEnc)
	}
	if panelPasswordEnc != "" {
		query += ", panel_password_enc=?"
		args = append(args, panelPasswordEnc)
	}

	query += " WHERE id=?"
	args = append(args, id)

	_, err := h.DB.Exec(query, args...)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse("更新失败"))
		return
	}

	c.JSON(http.StatusOK, models.SuccessResponse(nil))
}

func (h *ServerHandler) Delete(c *gin.Context) {
	id := c.Param("id")

	// 检查是否有关联网站
	var count int
	h.DB.QueryRow("SELECT COUNT(*) FROM websites WHERE server_id = ?", id).Scan(&count)
	if count > 0 {
		c.JSON(http.StatusBadRequest, models.ErrorResponse("请先删除该服务器下的所有网站"))
		return
	}

	_, err := h.DB.Exec("DELETE FROM servers WHERE id = ?", id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse("删除失败"))
		return
	}

	c.JSON(http.StatusOK, models.SuccessResponse(nil))
}

func (h *ServerHandler) GetStats(c *gin.Context) {
	var total, online, offline, expiring int
	h.DB.QueryRow("SELECT COUNT(*) FROM servers").Scan(&total)
	h.DB.QueryRow("SELECT COUNT(*) FROM servers WHERE is_online = 1").Scan(&online)
	h.DB.QueryRow("SELECT COUNT(*) FROM servers WHERE is_online = 0 AND status = 'active'").Scan(&offline)
	h.DB.QueryRow("SELECT COUNT(*) FROM servers WHERE expiry_date != '' AND expiry_date <= date('now','+30 days') AND expiry_date >= date('now') AND status = 'active'").Scan(&expiring)

	c.JSON(http.StatusOK, models.SuccessResponse(map[string]int{
		"total":    total,
		"online":   online,
		"offline":  offline,
		"expiring": expiring,
	}))
}
