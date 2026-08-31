package handlers

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/naibabiji/server-panel/database"
	"github.com/naibabiji/server-panel/i18n"
	"github.com/naibabiji/server-panel/middleware"
	"github.com/naibabiji/server-panel/models"
)

type ServerHandler struct {
	DB *sql.DB
}

const effectiveServerStatusSQL = "CASE WHEN s.expiry_date != '' AND s.expiry_date < date('now') THEN 'expired' ELSE 'active' END"

func (h *ServerHandler) List(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "30"))
	search := c.Query("search")
	status := c.Query("status")
	customerID := c.Query("customer_id")
	providerID := c.Query("provider_id")
	serverType := c.Query("server_type")
	orderBy := buildServerOrderBy(c.Query("sort"), c.Query("order"))

	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 30
	}

	where := "WHERE 1=1"
	args := []interface{}{}
	if search != "" {
		where += " AND (s.name LIKE ? OR s.ip_address LIKE ? OR s.location LIKE ?)"
		s := "%" + search + "%"
		args = append(args, s, s, s)
	}
	if status != "" {
		where += " AND " + effectiveServerStatusSQL + " = ?"
		args = append(args, status)
	}
	if customerID != "" {
		where += " AND s.customer_id = ?"
		args = append(args, customerID)
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
	query := `SELECT s.id, s.name, s.ip_address, s.server_type, s.os, s.customer_id, COALESCE(u.name,''),
		s.cpu_cores, s.ram_gb, s.disk_gb, s.bandwidth, s.provider_id, COALESCE(p.name,''),
		s.location, s.ssh_port, s.ssh_username, s.panel_type,
		(SELECT COUNT(*) FROM websites w WHERE w.server_id = s.id), s.panel_url, s.panel_username,
		s.purchase_date, s.expiry_date, s.renewal_cycle, s.auto_renewal, s.purchase_price, s.currency,
		` + effectiveServerStatusSQL + `, s.agent_version, s.last_seen_at, s.is_online,
		s.http_probe_enabled, s.http_probe_healthy, s.http_probe_last_at, s.http_probe_last_error,
		s.tcp_reachable, s.tcp_reachable_checked_at,
		s.status_page_enabled, s.notes, s.created_at, s.updated_at
		FROM servers s
		LEFT JOIN customers u ON s.customer_id = u.id
		LEFT JOIN providers p ON s.provider_id = p.id ` +
		where + " ORDER BY " + orderBy + " LIMIT ? OFFSET ?"
	args = append(args, pageSize, offset)

	rows, err := h.DB.Query(query, args...)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse(i18n.TE(c.Request, "errors.query_failed")))
		return
	}
	defer rows.Close()

	servers := []models.Server{}
	for rows.Next() {
		var s models.Server
		var probeHealthy, tcpReachable sql.NullInt64
		var lastSeen, probeLast, tcpReachableAt sql.NullString
		err := rows.Scan(&s.ID, &s.Name, &s.IPAddress, &s.ServerType, &s.OS, &s.CustomerID, &s.CustomerName,
			&s.CPUCores, &s.RAMGB, &s.DiskGB, &s.Bandwidth, &s.ProviderID, &s.ProviderName,
			&s.Location, &s.SSHPort, &s.SSHUsername, &s.PanelType, &s.WebsiteCount, &s.PanelURL, &s.PanelUsername,
			&s.PurchaseDate, &s.ExpiryDate, &s.RenewalCycle, &s.AutoRenewal, &s.PurchasePrice, &s.Currency,
			&s.Status, &s.AgentVersion, &lastSeen, &s.IsOnline,
			&s.HTTPProbeEnabled, &probeHealthy, &probeLast, &s.HTTPProbeLastError,
			&tcpReachable, &tcpReachableAt,
			&s.StatusPageEnabled, &s.Notes, &s.CreatedAt, &s.UpdatedAt)
		if err != nil {
			log.Printf("read server list row failed: %v", err)
			c.JSON(http.StatusInternalServerError, models.ErrorResponse(i18n.TE(c.Request, "errors.server.list_failed")))
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
		if tcpReachable.Valid {
			v := int(tcpReachable.Int64)
			s.TCPReachable = &v
		}
		if tcpReachableAt.Valid {
			s.TCPReachableAt = tcpReachableAt.String
		}
		servers = append(servers, s)
	}

	c.JSON(http.StatusOK, models.SuccessResponse(models.PaginatedResult{
		Items: servers, Total: total, Page: page, PageSize: pageSize,
	}))
}

// buildServerOrderBy whitelists the sortable columns for the server list so the
// sort/order query params can't be used to inject arbitrary SQL. Rows with no
// expiry_date always sort last, regardless of direction, so an empty date never
// masquerades as "soonest to expire".
func buildServerOrderBy(sort, order string) string {
	dir := "DESC"
	if order == "asc" {
		dir = "ASC"
	}
	switch sort {
	case "expiry_date":
		return "CASE WHEN s.expiry_date = '' THEN 1 ELSE 0 END ASC, s.expiry_date " + dir
	case "name":
		return "s.name " + dir
	default:
		return "s.created_at " + dir
	}
}

func (h *ServerHandler) Get(c *gin.Context) {
	id := c.Param("id")
	var s models.Server
	var probeHealthy, tcpReachable sql.NullInt64
	var lastSeen, probeLast, tcpReachableAt sql.NullString

	err := h.DB.QueryRow(
		`SELECT s.id, s.name, s.ip_address, s.server_type, s.os, s.customer_id, COALESCE(u.name,''),
		s.cpu_cores, s.ram_gb, s.disk_gb, s.bandwidth, s.provider_id, COALESCE(p.name,''),
		s.location, s.ssh_port, s.ssh_username, s.ssh_password_enc, s.panel_type, s.panel_url, s.panel_username,
		s.panel_password_enc, s.purchase_date, s.expiry_date, s.renewal_cycle, s.auto_renewal, s.purchase_price, s.currency,
		`+effectiveServerStatusSQL+`, s.agent_api_key_enc, s.agent_version, s.last_seen_at, s.is_online,
		s.http_probe_enabled, s.http_probe_healthy, s.http_probe_last_at, s.http_probe_last_error,
		s.tcp_reachable, s.tcp_reachable_checked_at,
		s.status_page_enabled, s.status_page_token, s.notes, s.created_at, s.updated_at
		FROM servers s
		LEFT JOIN customers u ON s.customer_id = u.id
		LEFT JOIN providers p ON s.provider_id = p.id
		WHERE s.id = ?`, id,
	).Scan(&s.ID, &s.Name, &s.IPAddress, &s.ServerType, &s.OS, &s.CustomerID, &s.CustomerName,
		&s.CPUCores, &s.RAMGB, &s.DiskGB, &s.Bandwidth, &s.ProviderID, &s.ProviderName,
		&s.Location, &s.SSHPort, &s.SSHUsername, &s.SSHPasswordEnc, &s.PanelType, &s.PanelURL, &s.PanelUsername,
		&s.PanelPasswordEnc, &s.PurchaseDate, &s.ExpiryDate, &s.RenewalCycle, &s.AutoRenewal, &s.PurchasePrice, &s.Currency,
		&s.Status, &s.AgentAPIKeyEnc, &s.AgentVersion, &lastSeen, &s.IsOnline,
		&s.HTTPProbeEnabled, &probeHealthy, &probeLast, &s.HTTPProbeLastError,
		&tcpReachable, &tcpReachableAt,
		&s.StatusPageEnabled, &s.StatusPageToken, &s.Notes, &s.CreatedAt, &s.UpdatedAt)
	if err != nil {
		log.Printf("read server detail failed: id=%s: %v", id, err)
		c.JSON(http.StatusNotFound, models.ErrorResponse(i18n.TE(c.Request, "errors.server.not_found")))
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
	if tcpReachable.Valid {
		v := int(tcpReachable.Int64)
		s.TCPReachable = &v
	}
	if tcpReachableAt.Valid {
		s.TCPReachableAt = tcpReachableAt.String
	}

	c.JSON(http.StatusOK, models.SuccessResponse(s))
}

func (h *ServerHandler) Create(c *gin.Context) {
	var s models.Server
	if err := c.ShouldBindJSON(&s); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse(i18n.TE(c.Request, "errors.invalid_request")))
		return
	}
	if s.Name == "" {
		c.JSON(http.StatusBadRequest, models.ErrorResponse(i18n.TE(c.Request, "errors.server.name_required")))
		return
	}

	sshPasswordEnc, ok := encryptOptionalPassword(c, h.DB, s.SSHPassword)
	if !ok {
		return
	}
	panelPasswordEnc, ok := encryptOptionalPassword(c, h.DB, s.PanelPassword)
	if !ok {
		return
	}
	s.ExpiryDate = models.RenewedExpiryDate(s.ExpiryDate, s.RenewalCycle, s.AutoRenewal, time.Now())
	s.Status = models.ServerStatusForExpiry(s.ExpiryDate, time.Now())

	result, err := h.DB.Exec(
		`INSERT INTO servers (name, ip_address, server_type, os, customer_id, cpu_cores, ram_gb, disk_gb, bandwidth,
		 provider_id, location, ssh_port, ssh_username, ssh_password_enc, panel_type, panel_url,
		 panel_username, panel_password_enc, purchase_date, expiry_date, renewal_cycle,
		 auto_renewal, purchase_price, currency, status, agent_api_key_hash, agent_api_key_enc, http_probe_enabled, notes)
		 VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		s.Name, s.IPAddress, s.ServerType, s.OS, s.CustomerID, s.CPUCores, s.RAMGB, s.DiskGB, s.Bandwidth,
		s.ProviderID, s.Location, s.SSHPort, s.SSHUsername, sshPasswordEnc, s.PanelType, s.PanelURL,
		s.PanelUsername, panelPasswordEnc, s.PurchaseDate, s.ExpiryDate, s.RenewalCycle,
		s.AutoRenewal, s.PurchasePrice, s.Currency, s.Status, "", "", s.HTTPProbeEnabled, s.Notes,
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse(i18n.TE(c.Request, "errors.server.create_failed", i18n.P{"error": err.Error()})))
		return
	}

	id, _ := result.LastInsertId()
	c.JSON(http.StatusOK, models.SuccessResponse(map[string]interface{}{
		"id":      id,
		"message": i18n.TE(c.Request, "errors.server.created"),
	}))
}

func (h *ServerHandler) RegenerateAgentKey(c *gin.Context) {
	id := c.Param("id")
	agentKeyStr, agentKeyHashStr, err := generateAgentKey()
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse(i18n.TE(c.Request, "errors.server.generate_agent_key_failed")))
		return
	}
	result, err := h.DB.Exec(
		`UPDATE servers
		 SET agent_api_key_hash = ?, agent_api_key_enc = '',
		     agent_version = '', last_seen_at = NULL, is_online = 0,
		     tcp_reachable = NULL, tcp_reachable_checked_at = NULL,
		     updated_at = CURRENT_TIMESTAMP
		 WHERE id = ?`,
		agentKeyHashStr, id,
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse(i18n.TE(c.Request, "errors.server.regen_agent_key_failed")))
		return
	}
	if rows, _ := result.RowsAffected(); rows == 0 {
		c.JSON(http.StatusNotFound, models.ErrorResponse(i18n.TE(c.Request, "errors.server.not_found")))
		return
	}
	c.JSON(http.StatusOK, models.SuccessResponse(map[string]interface{}{
		"agent_key": agentKeyStr,
		"message":   i18n.TE(c.Request, "errors.server.agent_key_regenerated"),
	}))
}

func (h *ServerHandler) PrepareAgentUninstall(c *gin.Context) {
	id := c.Param("id")
	result, err := h.DB.Exec(
		`UPDATE servers
		 SET agent_api_key_hash = '', agent_api_key_enc = '',
		     agent_version = '', last_seen_at = NULL, is_online = 0,
		     tcp_reachable = NULL, tcp_reachable_checked_at = NULL,
		     updated_at = CURRENT_TIMESTAMP
		 WHERE id = ?`,
		id,
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse(i18n.TE(c.Request, "errors.server.prepare_uninstall_failed")))
		return
	}
	if rows, _ := result.RowsAffected(); rows == 0 {
		c.JSON(http.StatusNotFound, models.ErrorResponse(i18n.TE(c.Request, "errors.server.not_found")))
		return
	}
	c.JSON(http.StatusOK, models.SuccessResponse(map[string]string{
		"message": i18n.TE(c.Request, "errors.server.agent_uninstall_ready"),
	}))
}

func (h *ServerHandler) GetSecret(c *gin.Context) {
	id := c.Param("id")
	field := c.Param("field")

	setup, err := isViewPasswordSetup(h.DB)
	if err != nil {
		log.Printf("read view password setup status failed: %v", err)
		c.JSON(http.StatusInternalServerError, models.ErrorResponse(i18n.TE(c.Request, "errors.view_password_status_failed")))
		return
	}
	if !setup {
		c.JSON(http.StatusPreconditionRequired, models.ErrorResponse(i18n.TE(c.Request, "common.view_password_required")))
		return
	}
	sessionToken, ok := getSessionToken(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, models.ErrorResponse(i18n.TE(c.Request, "session.session_expired")))
		return
	}
	if !ConsumeViewToken(c.GetHeader("X-View-Token"), sessionToken, middleware.ClientIP(c)) {
		c.JSON(http.StatusForbidden, models.ErrorResponse(i18n.TE(c.Request, "errors.reenter_view_password")))
		return
	}
	key, err := GetSecretEncryptionKey(h.DB)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse(i18n.TE(c.Request, "errors.read_encryption_key_failed")))
		return
	}

	var column, label string
	switch field {
	case "ssh-password":
		column = "ssh_password_enc"
		label = i18n.TE(c.Request, "errors.server.label_ssh_password")
	case "panel-password":
		column = "panel_password_enc"
		label = i18n.TE(c.Request, "errors.server.label_panel_password")
	default:
		c.JSON(http.StatusNotFound, models.ErrorResponse(i18n.TE(c.Request, "errors.server.unsupported_secret_field")))
		return
	}

	var encrypted string
	if err := h.DB.QueryRow("SELECT "+column+" FROM servers WHERE id = ?", id).Scan(&encrypted); err != nil {
		c.JSON(http.StatusNotFound, models.ErrorResponse(i18n.TE(c.Request, "errors.server.not_found")))
		return
	}
	if encrypted == "" {
		c.JSON(http.StatusOK, models.SuccessResponse(map[string]string{
			"field": field,
			"label": label,
			"value": "",
		}))
		return
	}

	value, err := DecryptPassword(encrypted, key)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse(i18n.TE(c.Request, "errors.server.decrypt_failed", i18n.P{"label": label})))
		return
	}

	c.JSON(http.StatusOK, models.SuccessResponse(map[string]string{
		"field": field,
		"label": label,
		"value": value,
	}))
}

func (h *ServerHandler) Update(c *gin.Context) {
	id := c.Param("id")

	var s models.Server
	if err := c.ShouldBindJSON(&s); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse(i18n.TE(c.Request, "errors.invalid_request")))
		return
	}
	if s.Name == "" {
		c.JSON(http.StatusBadRequest, models.ErrorResponse(i18n.TE(c.Request, "errors.server.name_required")))
		return
	}

	sshPasswordEnc, ok := encryptOptionalPassword(c, h.DB, s.SSHPassword)
	if !ok {
		return
	}
	panelPasswordEnc, ok := encryptOptionalPassword(c, h.DB, s.PanelPassword)
	if !ok {
		return
	}
	s.ExpiryDate = models.RenewedExpiryDate(s.ExpiryDate, s.RenewalCycle, s.AutoRenewal, time.Now())
	s.Status = models.ServerStatusForExpiry(s.ExpiryDate, time.Now())

	query := `UPDATE servers SET name=?, ip_address=?, server_type=?, os=?, customer_id=?,
		cpu_cores=?, ram_gb=?, disk_gb=?, bandwidth=?, provider_id=?, location=?,
		ssh_port=?, ssh_username=?, panel_type=?, panel_url=?, panel_username=?,
		purchase_date=?, expiry_date=?, renewal_cycle=?, auto_renewal=?,
		purchase_price=?, currency=?, status=?, http_probe_enabled=?, notes=?, updated_at=CURRENT_TIMESTAMP`
	args := []interface{}{s.Name, s.IPAddress, s.ServerType, s.OS, s.CustomerID,
		s.CPUCores, s.RAMGB, s.DiskGB, s.Bandwidth, s.ProviderID, s.Location,
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

	result, err := h.DB.Exec(query, args...)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse(i18n.TE(c.Request, "errors.update_failed")))
		return
	}
	if rows, _ := result.RowsAffected(); rows == 0 {
		c.JSON(http.StatusNotFound, models.ErrorResponse(i18n.TE(c.Request, "errors.server.not_found")))
		return
	}

	c.JSON(http.StatusOK, models.SuccessResponse(nil))
}

func (h *ServerHandler) Delete(c *gin.Context) {
	id := c.Param("id")

	var count int
	h.DB.QueryRow("SELECT COUNT(*) FROM websites WHERE server_id = ?", id).Scan(&count)
	if count > 0 {
		c.JSON(http.StatusBadRequest, models.ErrorResponse(i18n.TE(c.Request, "errors.server.delete_websites_first")))
		return
	}

	result, err := h.DB.Exec("DELETE FROM servers WHERE id = ?", id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse(i18n.TE(c.Request, "errors.delete_failed")))
		return
	}
	if rows, _ := result.RowsAffected(); rows == 0 {
		c.JSON(http.StatusNotFound, models.ErrorResponse(i18n.TE(c.Request, "errors.server.not_found")))
		return
	}

	c.JSON(http.StatusOK, models.SuccessResponse(nil))
}

func (h *ServerHandler) GetStats(c *gin.Context) {
	var total, online, offline, expiring, overdue int
	h.DB.QueryRow("SELECT COUNT(*) FROM servers").Scan(&total)
	h.DB.QueryRow("SELECT COUNT(*) FROM servers WHERE is_online = 1").Scan(&online)
	h.DB.QueryRow(`SELECT COUNT(*) FROM servers
		WHERE is_online = 0
		AND (agent_version != '' OR last_seen_at IS NOT NULL)`).Scan(&offline)
	// Same "expiring"/"overdue" split as /api/dashboard/stats - expiring is
	// the next-30-days window and overdue is already past expiry_date.
	h.DB.QueryRow("SELECT COUNT(*) FROM servers WHERE expiry_date != '' AND expiry_date >= date('now') AND expiry_date <= date('now','+30 days')").Scan(&expiring)
	h.DB.QueryRow("SELECT COUNT(*) FROM servers WHERE expiry_date != '' AND expiry_date < date('now')").Scan(&overdue)

	c.JSON(http.StatusOK, models.SuccessResponse(map[string]int{
		"total":    total,
		"online":   online,
		"offline":  offline,
		"expiring": expiring,
		"overdue":  overdue,
	}))
}

func generateAgentKey() (string, string, error) {
	agentKey := make([]byte, 32)
	if _, err := rand.Read(agentKey); err != nil {
		return "", "", err
	}
	agentKeyStr := base64.RawURLEncoding.EncodeToString(agentKey)
	agentKeyHash := sha256.Sum256([]byte(agentKeyStr))
	return agentKeyStr, hex.EncodeToString(agentKeyHash[:]), nil
}

func encryptOptionalPassword(c *gin.Context, db *sql.DB, plaintext string) (string, bool) {
	return encryptOptionalSecret(c, db, plaintext, i18n.TE(c.Request, "errors.server.setup_view_password_for_secret"))
}

func encryptOptionalSecret(c *gin.Context, db *sql.DB, plaintext string, setupMessage string) (string, bool) {
	if plaintext == "" {
		return "", true
	}

	setup, err := isViewPasswordSetup(db)
	if err != nil {
		log.Printf("read view password setup status failed: %v", err)
		c.JSON(http.StatusInternalServerError, models.ErrorResponse(i18n.TE(c.Request, "errors.view_password_status_failed")))
		return "", false
	}
	if !setup {
		c.JSON(http.StatusForbidden, models.ErrorResponse(setupMessage))
		return "", false
	}
	key, err := GetSecretEncryptionKey(db)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse(i18n.TE(c.Request, "errors.read_encryption_key_failed")))
		return "", false
	}

	enc, err := EncryptPassword(plaintext, key)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse(i18n.TE(c.Request, "errors.server.encrypt_password_failed")))
		return "", false
	}
	return enc, true
}

func encryptSecretIfUnlocked(c *gin.Context, plaintext string) string {
	if plaintext == "" {
		return ""
	}
	key, err := GetSecretEncryptionKey(database.GetDB())
	if err != nil {
		return ""
	}
	enc, err := EncryptPassword(plaintext, key)
	if err != nil {
		return ""
	}
	return enc
}

func isViewPasswordSetup(db *sql.DB) (bool, error) {
	if db == nil {
		return false, errors.New("database is not initialized")
	}
	var hash string
	var err error
	for i := 0; i < 3; i++ {
		err = db.QueryRow("SELECT svalue FROM settings WHERE skey = 'view_password_hash'").Scan(&hash)
		if err == nil {
			return hash != "", nil
		}
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		time.Sleep(time.Duration(100*(i+1)) * time.Millisecond)
	}
	return false, err
}
