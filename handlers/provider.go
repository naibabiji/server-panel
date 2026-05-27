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
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "50"))
	search := c.Query("search")

	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 50
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
		"SELECT id, name, website, contact, notes, created_at, updated_at FROM providers "+where+" ORDER BY name LIMIT ? OFFSET ?",
		append(args, pageSize, offset)...)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse("查询失败"))
		return
	}
	defer rows.Close()

	providers := []models.Provider{}
	for rows.Next() {
		var p models.Provider
		if err := rows.Scan(&p.ID, &p.Name, &p.Website, &p.Contact, &p.Notes, &p.CreatedAt, &p.UpdatedAt); err != nil {
			c.JSON(http.StatusInternalServerError, models.ErrorResponse("读取服务商列表失败"))
			return
		}
		providers = append(providers, p)
	}

	c.JSON(http.StatusOK, models.SuccessResponse(models.PaginatedResult{
		Items: providers, Total: total, Page: page, PageSize: pageSize,
	}))
}

func (h *ProviderHandler) Get(c *gin.Context) {
	id := c.Param("id")
	var p models.Provider
	err := h.db().QueryRow(
		"SELECT id, name, website, contact, notes, created_at, updated_at FROM providers WHERE id = ?", id,
	).Scan(&p.ID, &p.Name, &p.Website, &p.Contact, &p.Notes, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		c.JSON(http.StatusNotFound, models.ErrorResponse("服务商不存在"))
		return
	}
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

	result, err := h.db().Exec(
		"INSERT INTO providers (name, website, contact, notes) VALUES (?,?,?,?)",
		p.Name, p.Website, p.Contact, p.Notes,
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

	result, err := h.db().Exec(
		"UPDATE providers SET name=?, website=?, contact=?, notes=?, updated_at=CURRENT_TIMESTAMP WHERE id=?",
		p.Name, p.Website, p.Contact, p.Notes, id,
	)
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

func validateProviderInput(c *gin.Context, p *models.Provider) bool {
	p.Name = strings.TrimSpace(p.Name)
	if p.Name == "" {
		c.JSON(http.StatusBadRequest, models.ErrorResponse("服务商名称不能为空"))
		return false
	}
	return true
}
