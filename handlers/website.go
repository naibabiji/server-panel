package handlers

import (
	"database/sql"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/naibabiji/server-panel/database"
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
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	search := c.Query("search")
	serverID := c.Query("server_id")
	userID := c.Query("user_id")
	status := c.Query("status")

	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
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
	if userID != "" {
		where += " AND w.user_id = ?"
		args = append(args, userID)
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
		 w.user_id, COALESCE(u.name,''), w.expiry_date, w.status, w.notes, w.created_at, w.updated_at
		 FROM websites w
		 LEFT JOIN servers s ON w.server_id = s.id
		 LEFT JOIN users u ON w.user_id = u.id `+
			where+" ORDER BY w.created_at DESC LIMIT ? OFFSET ?",
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
			&w.UserID, &w.UserName, &w.ExpiryDate, &w.Status, &w.Notes, &w.CreatedAt, &w.UpdatedAt)
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

func (h *WebsiteHandler) Get(c *gin.Context) {
	id := c.Param("id")
	var w models.Website
	err := h.db().QueryRow(
		`SELECT w.id, w.name, w.domain, w.site_type, w.server_id, COALESCE(s.name,''),
		 w.user_id, COALESCE(u.name,''), w.expiry_date, w.status, w.notes, w.created_at, w.updated_at
		 FROM websites w
		 LEFT JOIN servers s ON w.server_id = s.id
		 LEFT JOIN users u ON w.user_id = u.id
		 WHERE w.id = ?`, id,
	).Scan(&w.ID, &w.Name, &w.Domain, &w.SiteType, &w.ServerID, &w.ServerName,
		&w.UserID, &w.UserName, &w.ExpiryDate, &w.Status, &w.Notes, &w.CreatedAt, &w.UpdatedAt)
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

	result, err := h.db().Exec(
		`INSERT INTO websites (name, domain, site_type, server_id, user_id, expiry_date, status, notes)
		 VALUES (?,?,?,?,?,?,?,?)`,
		w.Name, w.Domain, w.SiteType, w.ServerID, w.UserID, w.ExpiryDate, w.Status, w.Notes,
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

	result, err := h.db().Exec(
		`UPDATE websites SET name=?, domain=?, site_type=?, server_id=?, user_id=?, expiry_date=?, status=?, notes=?, updated_at=CURRENT_TIMESTAMP WHERE id=?`,
		w.Name, w.Domain, w.SiteType, w.ServerID, w.UserID, w.ExpiryDate, w.Status, w.Notes, id,
	)
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
	return true
}
