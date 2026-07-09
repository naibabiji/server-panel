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

type ProviderHandler struct {
	DB *sql.DB
}

func (h *ProviderHandler) db() *sql.DB {
	if h.DB != nil {
		return h.DB
	}
	return database.GetDB()
}

func (h *ProviderHandler) List(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "30"))
	search := c.Query("search")

	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 30
	}

	where := "WHERE 1=1"
	args := []interface{}{}
	if search != "" {
		where += " AND name LIKE ?"
		args = append(args, "%"+search+"%")
	}

	var total int
	h.db().QueryRow("SELECT COUNT(*) FROM providers "+where, args...).Scan(&total)

	offset := (page - 1) * pageSize
	rows, err := h.db().Query(
		"SELECT id, name, website, contact, private_notes_enc <> '', notes, created_at, updated_at FROM providers "+where+" ORDER BY name LIMIT ? OFFSET ?",
		append(args, pageSize, offset)...)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse("查询失败"))
		return
	}
	defer rows.Close()

	providers := []models.Provider{}
	for rows.Next() {
		var p models.Provider
		var hasPrivateNotes int
		if err := rows.Scan(&p.ID, &p.Name, &p.Website, &p.Contact, &hasPrivateNotes, &p.Notes, &p.CreatedAt, &p.UpdatedAt); err != nil {
			c.JSON(http.StatusInternalServerError, models.ErrorResponse("读取服务商列表失败"))
			return
		}
		p.HasPrivateNotes = hasPrivateNotes == 1
		providers = append(providers, p)
	}

	c.JSON(http.StatusOK, models.SuccessResponse(models.PaginatedResult{
		Items: providers, Total: total, Page: page, PageSize: pageSize,
	}))
}

func (h *ProviderHandler) Get(c *gin.Context) {
	id := c.Param("id")
	var p models.Provider
	var hasPrivateNotes int
	err := h.db().QueryRow(
		"SELECT id, name, website, contact, private_notes_enc <> '', notes, created_at, updated_at FROM providers WHERE id = ?", id,
	).Scan(&p.ID, &p.Name, &p.Website, &p.Contact, &hasPrivateNotes, &p.Notes, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		c.JSON(http.StatusNotFound, models.ErrorResponse("服务商不存在"))
		return
	}
	p.HasPrivateNotes = hasPrivateNotes == 1
	c.JSON(http.StatusOK, models.SuccessResponse(p))
}

func (h *ProviderHandler) Create(c *gin.Context) {
	var p models.Provider
	if err := c.ShouldBindJSON(&p); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse("无效的请求数据"))
		return
	}
	if !validateProviderInput(c, &p) {
		return
	}
	if h.providerNameTaken(p.Name, 0) {
		c.JSON(http.StatusConflict, models.ErrorResponse("服务商已存在（名称不区分大小写）"))
		return
	}
	privateNotesEnc, ok := encryptOptionalSecret(c, h.db(), p.PrivateNotes, "请先设置查看密码，再保存加密备注")
	if !ok {
		return
	}

	result, err := h.db().Exec(
		"INSERT INTO providers (name, website, contact, private_notes_enc, notes) VALUES (?,?,?,?,?)",
		p.Name, p.Website, p.Contact, privateNotesEnc, p.Notes,
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse("创建失败，名称可能已存在"))
		return
	}
	id, _ := result.LastInsertId()
	c.JSON(http.StatusOK, models.SuccessResponse(map[string]int64{"id": id}))
}

func (h *ProviderHandler) Update(c *gin.Context) {
	id := c.Param("id")
	var p models.Provider
	if err := c.ShouldBindJSON(&p); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse("无效的请求数据"))
		return
	}
	if !validateProviderInput(c, &p) {
		return
	}
	if idNum, err := strconv.ParseInt(id, 10, 64); err == nil && h.providerNameTaken(p.Name, idNum) {
		c.JSON(http.StatusConflict, models.ErrorResponse("服务商已存在（名称不区分大小写）"))
		return
	}

	query := "UPDATE providers SET name=?, website=?, contact=?, notes=?, updated_at=CURRENT_TIMESTAMP"
	args := []interface{}{p.Name, p.Website, p.Contact, p.Notes}
	if p.PrivateNotes != "" {
		privateNotesEnc, ok := encryptOptionalSecret(c, h.db(), p.PrivateNotes, "请先设置查看密码，再保存加密备注")
		if !ok {
			return
		}
		query += ", private_notes_enc=?"
		args = append(args, privateNotesEnc)
	}
	query += " WHERE id=?"
	args = append(args, id)

	result, err := h.db().Exec(query, args...)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse("更新失败，名称可能已存在"))
		return
	}
	if rows, _ := result.RowsAffected(); rows == 0 {
		c.JSON(http.StatusNotFound, models.ErrorResponse("服务商不存在"))
		return
	}
	c.JSON(http.StatusOK, models.SuccessResponse(nil))
}

func (h *ProviderHandler) GetPrivateNotes(c *gin.Context) {
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

	var encrypted string
	if err := h.db().QueryRow("SELECT private_notes_enc FROM providers WHERE id = ?", id).Scan(&encrypted); err != nil {
		c.JSON(http.StatusNotFound, models.ErrorResponse("服务商不存在"))
		return
	}
	if encrypted == "" {
		c.JSON(http.StatusOK, models.SuccessResponse(map[string]string{
			"field": "private-notes",
			"label": "加密备注",
			"value": "",
		}))
		return
	}

	key, err := GetSecretEncryptionKey(h.db())
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse("读取加密密钥失败"))
		return
	}
	value, err := DecryptPassword(encrypted, key)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse("加密备注解密失败，请确认查看密码是否正确"))
		return
	}
	c.JSON(http.StatusOK, models.SuccessResponse(map[string]string{
		"field": "private-notes",
		"label": "加密备注",
		"value": value,
	}))
}

func (h *ProviderHandler) ClearPrivateNotes(c *gin.Context) {
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

	result, err := h.db().Exec("UPDATE providers SET private_notes_enc='', updated_at=CURRENT_TIMESTAMP WHERE id = ?", id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse("清空加密备注失败"))
		return
	}
	if rows, _ := result.RowsAffected(); rows == 0 {
		c.JSON(http.StatusNotFound, models.ErrorResponse("服务商不存在"))
		return
	}
	c.JSON(http.StatusOK, models.SuccessResponse(nil))
}

func (h *ProviderHandler) Delete(c *gin.Context) {
	id := c.Param("id")
	result, err := h.db().Exec("DELETE FROM providers WHERE id = ?", id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse("删除失败"))
		return
	}
	if rows, _ := result.RowsAffected(); rows == 0 {
		c.JSON(http.StatusNotFound, models.ErrorResponse("服务商不存在"))
		return
	}
	c.JSON(http.StatusOK, models.SuccessResponse(nil))
}

// providerNameTaken reports whether another provider already has this name,
// compared case-insensitively so "DigitalOcean" and "digitalocean" are
// treated as the same provider. excludeID is the provider being updated (0
// when creating), so renaming a provider back to its own name isn't flagged
// as a conflict with itself.
func (h *ProviderHandler) providerNameTaken(name string, excludeID int64) bool {
	var existingID int64
	err := h.db().QueryRow(
		"SELECT id FROM providers WHERE LOWER(name) = LOWER(?) AND id != ?",
		name, excludeID,
	).Scan(&existingID)
	return err == nil
}

func validateProviderInput(c *gin.Context, p *models.Provider) bool {
	p.Name = strings.TrimSpace(p.Name)
	if p.Name == "" {
		c.JSON(http.StatusBadRequest, models.ErrorResponse("服务商名称不能为空"))
		return false
	}
	return true
}
