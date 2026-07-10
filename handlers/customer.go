package handlers

import (
	"database/sql"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/naibabiji/server-panel/database"
	"github.com/naibabiji/server-panel/models"
)

type CustomerHandler struct {
	DB *sql.DB
}

func (h *CustomerHandler) db() *sql.DB {
	if h.DB != nil {
		return h.DB
	}
	return database.GetDB()
}

func (h *CustomerHandler) List(c *gin.Context) {
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
		where += " AND (name LIKE ? OR contact_person LIKE ? OR company LIKE ?)"
		s := "%" + search + "%"
		args = append(args, s, s, s)
	}

	var total int
	h.db().QueryRow("SELECT COUNT(*) FROM customers "+where, args...).Scan(&total)

	offset := (page - 1) * pageSize
	rows, err := h.db().Query(
		`SELECT c.id, c.name, c.contact_person, c.phone, c.email, c.company, c.start_date, c.address, c.notes, c.created_at, c.updated_at,
			(SELECT COUNT(*) FROM servers s WHERE s.customer_id = c.id) AS server_count,
			(SELECT COUNT(*) FROM websites w WHERE w.customer_id = c.id) AS website_count
		FROM customers c `+where+" ORDER BY c.created_at DESC LIMIT ? OFFSET ?",
		append(args, pageSize, offset)...)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse("查询失败"))
		return
	}
	defer rows.Close()

	customers := []models.Customer{}
	for rows.Next() {
		var u models.Customer
		rows.Scan(&u.ID, &u.Name, &u.ContactPerson, &u.Phone, &u.Email, &u.Company,
			&u.StartDate, &u.Address, &u.Notes, &u.CreatedAt, &u.UpdatedAt, &u.ServerCount, &u.WebsiteCount)
		customers = append(customers, u)
	}

	c.JSON(http.StatusOK, models.SuccessResponse(models.PaginatedResult{
		Items: customers, Total: total, Page: page, PageSize: pageSize,
	}))
}

func (h *CustomerHandler) Get(c *gin.Context) {
	id := c.Param("id")
	var u models.Customer
	err := h.db().QueryRow(
		"SELECT id, name, contact_person, phone, email, company, start_date, address, notes, created_at, updated_at FROM customers WHERE id = ?", id,
	).Scan(&u.ID, &u.Name, &u.ContactPerson, &u.Phone, &u.Email, &u.Company,
		&u.StartDate, &u.Address, &u.Notes, &u.CreatedAt, &u.UpdatedAt)
	if err != nil {
		c.JSON(http.StatusNotFound, models.ErrorResponse("客户不存在"))
		return
	}
	c.JSON(http.StatusOK, models.SuccessResponse(u))
}

func (h *CustomerHandler) Create(c *gin.Context) {
	var u models.Customer
	if err := c.ShouldBindJSON(&u); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse("无效的请求数据"))
		return
	}
	if u.Name == "" {
		c.JSON(http.StatusBadRequest, models.ErrorResponse("客户名称不能为空"))
		return
	}

	result, err := h.db().Exec(
		`INSERT INTO customers (name, contact_person, phone, email, company, start_date, address, notes)
		 VALUES (?,?,?,?,?,?,?,?)`,
		u.Name, u.ContactPerson, u.Phone, u.Email, u.Company, u.StartDate, u.Address, u.Notes,
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse("创建失败"))
		return
	}
	id, _ := result.LastInsertId()
	c.JSON(http.StatusOK, models.SuccessResponse(map[string]int64{"id": id}))
}

func (h *CustomerHandler) Update(c *gin.Context) {
	id := c.Param("id")
	var u models.Customer
	if err := c.ShouldBindJSON(&u); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse("无效的请求数据"))
		return
	}
	if u.Name == "" {
		c.JSON(http.StatusBadRequest, models.ErrorResponse("客户名称不能为空"))
		return
	}

	result, err := h.db().Exec(
		`UPDATE customers SET name=?, contact_person=?, phone=?, email=?, company=?, start_date=?, address=?, notes=?, updated_at=CURRENT_TIMESTAMP WHERE id=?`,
		u.Name, u.ContactPerson, u.Phone, u.Email, u.Company, u.StartDate, u.Address, u.Notes, id,
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse("更新失败"))
		return
	}
	if rows, _ := result.RowsAffected(); rows == 0 {
		c.JSON(http.StatusNotFound, models.ErrorResponse("客户不存在"))
		return
	}
	c.JSON(http.StatusOK, models.SuccessResponse(nil))
}

func (h *CustomerHandler) Delete(c *gin.Context) {
	id := c.Param("id")
	result, err := h.db().Exec("DELETE FROM customers WHERE id = ?", id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse("删除失败"))
		return
	}
	if rows, _ := result.RowsAffected(); rows == 0 {
		c.JSON(http.StatusNotFound, models.ErrorResponse("客户不存在"))
		return
	}
	c.JSON(http.StatusOK, models.SuccessResponse(nil))
}
