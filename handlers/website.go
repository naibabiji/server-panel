package handlers

import (
	"database/sql"
	"log"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/naibabiji/server-panel/database"
	"github.com/naibabiji/server-panel/middleware"
	"github.com/naibabiji/server-panel/models"
)

type WebsiteHandler struct {
	DB *sql.DB
}

func (h *WebsiteHandler) db() *sql.DB {
	if h.DB != nil {
		return h.DB
	}
	return database.GetDB()
}

func (h *WebsiteHandler) List(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "30"))
	search := c.Query("search")
	serverID := c.Query("server_id")
	customerID := c.Query("customer_id")
	status := c.Query("status")
	orderBy := buildWebsiteOrderBy(c.Query("sort"), c.Query("order"))

	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 30
	}

	where := "WHERE 1=1"
	args := []interface{}{}
	if search != "" {
		where += " AND (w.name LIKE ? OR w.domain LIKE ?)"
		s := "%" + search + "%"
		args = append(args, s, s)
	}
	if serverID != "" {
		where += " AND w.server_id = ?"
		args = append(args, serverID)
	}
	if customerID != "" {
		where += " AND w.customer_id = ?"
		args = append(args, customerID)
	}
	if status != "" {
		where += " AND w.status = ?"
		args = append(args, status)
	}

	var total int
	h.db().QueryRow("SELECT COUNT(*) FROM websites w "+where, args...).Scan(&total)

	offset := (page - 1) * pageSize
	rows, err := h.db().Query(
		`SELECT w.id, w.name, w.domain, w.site_type, w.server_id, COALESCE(s.name,''),
		 w.customer_id, COALESCE(u.name,''), w.panel_type, w.panel_url, w.panel_username,
		 w.expiry_date, w.status, w.notes, w.created_at, w.updated_at
		 FROM websites w
		 LEFT JOIN servers s ON w.server_id = s.id
		 LEFT JOIN customers u ON w.customer_id = u.id `+
			where+" ORDER BY "+orderBy+" LIMIT ? OFFSET ?",
		append(args, pageSize, offset)...)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse("查询失败"))
		return
	}
	defer rows.Close()

	websites := []models.Website{}
	for rows.Next() {
		var w models.Website
		err := rows.Scan(&w.ID, &w.Name, &w.Domain, &w.SiteType, &w.ServerID, &w.ServerName,
			&w.CustomerID, &w.CustomerName, &w.PanelType, &w.PanelURL, &w.PanelUsername,
			&w.ExpiryDate, &w.Status, &w.Notes, &w.CreatedAt, &w.UpdatedAt)
		if err != nil {
			c.JSON(http.StatusInternalServerError, models.ErrorResponse("读取网站列表失败"))
			return
		}
		websites = append(websites, w)
	}

	c.JSON(http.StatusOK, models.SuccessResponse(models.PaginatedResult{
		Items: websites, Total: total, Page: page, PageSize: pageSize,
	}))
}

// buildWebsiteOrderBy whitelists the sortable columns for the website list so the
// sort/order query params can't be used to inject arbitrary SQL. Rows with no
// expiry_date always sort last, regardless of direction.
func buildWebsiteOrderBy(sort, order string) string {
	dir := "DESC"
	if order == "asc" {
		dir = "ASC"
	}
	switch sort {
	case "expiry_date":
		return "CASE WHEN w.expiry_date = '' THEN 1 ELSE 0 END ASC, w.expiry_date " + dir
	case "name":
		return "w.name " + dir
	default:
		return "w.created_at " + dir
	}
}

func (h *WebsiteHandler) Get(c *gin.Context) {
	id := c.Param("id")
	var w models.Website
	err := h.db().QueryRow(
		`SELECT w.id, w.name, w.domain, w.site_type, w.server_id, COALESCE(s.name,''),
		 w.customer_id, COALESCE(u.name,''), w.panel_type, w.panel_url, w.panel_username, w.panel_password_enc,
		 w.expiry_date, w.status, w.notes, w.created_at, w.updated_at
		 FROM websites w
		 LEFT JOIN servers s ON w.server_id = s.id
		 LEFT JOIN customers u ON w.customer_id = u.id
		 WHERE w.id = ?`, id,
	).Scan(&w.ID, &w.Name, &w.Domain, &w.SiteType, &w.ServerID, &w.ServerName,
		&w.CustomerID, &w.CustomerName, &w.PanelType, &w.PanelURL, &w.PanelUsername, &w.PanelPasswordEnc,
		&w.ExpiryDate, &w.Status, &w.Notes, &w.CreatedAt, &w.UpdatedAt)
	if err != nil {
		c.JSON(http.StatusNotFound, models.ErrorResponse("网站不存在"))
		return
	}
	c.JSON(http.StatusOK, models.SuccessResponse(w))
}

func (h *WebsiteHandler) Create(c *gin.Context) {
	var w models.Website
	if err := c.ShouldBindJSON(&w); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse("无效的请求数据"))
		return
	}
	if !validateWebsiteInput(c, &w) {
		return
	}
	if h.websiteDomainTaken(w.Domain, 0) {
		c.JSON(http.StatusConflict, models.ErrorResponse("该域名已添加过网站"))
		return
	}
	panelPasswordEnc, ok := encryptOptionalPassword(c, h.db(), w.PanelPassword)
	if !ok {
		return
	}

	result, err := h.db().Exec(
		`INSERT INTO websites (name, domain, site_type, server_id, customer_id, panel_type, panel_url, panel_username, panel_password_enc, expiry_date, status, notes)
		 VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
		w.Name, w.Domain, w.SiteType, w.ServerID, w.CustomerID, w.PanelType, w.PanelURL, w.PanelUsername, panelPasswordEnc, w.ExpiryDate, w.Status, w.Notes,
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse("创建失败"))
		return
	}
	id, _ := result.LastInsertId()
	c.JSON(http.StatusOK, models.SuccessResponse(map[string]int64{"id": id}))
}

func (h *WebsiteHandler) Update(c *gin.Context) {
	id := c.Param("id")
	var w models.Website
	if err := c.ShouldBindJSON(&w); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse("无效的请求数据"))
		return
	}
	if !validateWebsiteInput(c, &w) {
		return
	}
	if idNum, err := strconv.ParseInt(id, 10, 64); err == nil && h.websiteDomainTaken(w.Domain, idNum) {
		c.JSON(http.StatusConflict, models.ErrorResponse("该域名已添加过网站"))
		return
	}
	panelPasswordEnc, ok := encryptOptionalPassword(c, h.db(), w.PanelPassword)
	if !ok {
		return
	}

	query := `UPDATE websites SET name=?, domain=?, site_type=?, server_id=?, customer_id=?,
		panel_type=?, panel_url=?, panel_username=?, expiry_date=?, status=?, notes=?, updated_at=CURRENT_TIMESTAMP`
	args := []interface{}{w.Name, w.Domain, w.SiteType, w.ServerID, w.CustomerID,
		w.PanelType, w.PanelURL, w.PanelUsername, w.ExpiryDate, w.Status, w.Notes}
	if panelPasswordEnc != "" {
		query += ", panel_password_enc=?"
		args = append(args, panelPasswordEnc)
	}
	query += " WHERE id=?"
	args = append(args, id)

	result, err := h.db().Exec(query, args...)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse("更新失败"))
		return
	}
	if rows, _ := result.RowsAffected(); rows == 0 {
		c.JSON(http.StatusNotFound, models.ErrorResponse("网站不存在"))
		return
	}
	c.JSON(http.StatusOK, models.SuccessResponse(nil))
}

func (h *WebsiteHandler) GetPanelPassword(c *gin.Context) {
	id := c.Param("id")
	setup, err := isViewPasswordSetup(h.db())
	if err != nil {
		log.Printf("read view password setup status failed: %v", err)
		c.JSON(http.StatusInternalServerError, models.ErrorResponse("读取查看密码状态失败"))
		return
	}
	if !setup {
		c.JSON(http.StatusPreconditionRequired, models.ErrorResponse("请先设置查看密码"))
		return
	}
	sessionToken, ok := getSessionToken(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, models.ErrorResponse("会话已过期，请重新登录"))
		return
	}
	if !ConsumeViewToken(c.GetHeader("X-View-Token"), sessionToken, middleware.ClientIP(c)) {
		c.JSON(http.StatusForbidden, models.ErrorResponse("请重新输入查看密码"))
		return
	}
	key, err := GetSecretEncryptionKey(h.db())
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse("读取加密密钥失败"))
		return
	}

	var encrypted string
	err = h.db().QueryRow("SELECT panel_password_enc FROM websites WHERE id = ?", id).Scan(&encrypted)
	if err != nil {
		c.JSON(http.StatusNotFound, models.ErrorResponse("网站不存在"))
		return
	}
	if encrypted == "" {
		c.JSON(http.StatusOK, models.SuccessResponse(map[string]string{
			"field": "panel-password",
			"label": "网站面板密码",
			"value": "",
		}))
		return
	}
	value, err := DecryptPassword(encrypted, key)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse("网站面板密码解密失败，请确认查看密码是否正确"))
		return
	}
	c.JSON(http.StatusOK, models.SuccessResponse(map[string]string{
		"field": "panel-password",
		"label": "网站面板密码",
		"value": value,
	}))
}

func (h *WebsiteHandler) Delete(c *gin.Context) {
	id := c.Param("id")
	result, err := h.db().Exec("DELETE FROM websites WHERE id = ?", id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse("删除失败"))
		return
	}
	if rows, _ := result.RowsAffected(); rows == 0 {
		c.JSON(http.StatusNotFound, models.ErrorResponse("网站不存在"))
		return
	}
	c.JSON(http.StatusOK, models.SuccessResponse(nil))
}

// websiteDomainTaken reports whether another website already uses this
// domain, compared case-insensitively (domains aren't case-sensitive).
// excludeID is the website being updated (0 when creating), so saving a
// website without changing its own domain isn't flagged as a conflict with
// itself.
func (h *WebsiteHandler) websiteDomainTaken(domain string, excludeID int64) bool {
	var existingID int64
	err := h.db().QueryRow(
		"SELECT id FROM websites WHERE LOWER(domain) = LOWER(?) AND id != ?",
		domain, excludeID,
	).Scan(&existingID)
	return err == nil
}

func validateWebsiteInput(c *gin.Context, w *models.Website) bool {
	w.Domain = strings.TrimSpace(w.Domain)
	if w.Domain == "" {
		c.JSON(http.StatusBadRequest, models.ErrorResponse("域名不能为空"))
		return false
	}
	if w.ServerID == 0 {
		c.JSON(http.StatusBadRequest, models.ErrorResponse("请选择所属服务器"))
		return false
	}
	if w.Status == "" {
		w.Status = models.WebsiteStatusActive
	}
	if w.PanelType == "" {
		w.PanelType = models.PanelTypeNone
	}
	return true
}
