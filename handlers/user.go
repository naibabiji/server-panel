package handlers

import (
	"database/sql"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/naibabiji/server-panel/database"
	"github.com/naibabiji/server-panel/models"
)

type UserHandler struct {
	DB *sql.DB
}

func (h *UserHandler) List(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	search := c.Query("search")

	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}

	where := "WHERE 1=1"
	args := []interface{}{}
	if search != "" {
		where += " AND (name LIKE ? OR contact_person LIKE ? OR company LIKE ?)"
		s := "%" + search + "%"
		args = append(args, s, s, s)
	}

	var total int
	database.GetDB().QueryRow("SELECT COUNT(*) FROM users "+where, args...).Scan(&total)

	offset := (page - 1) * pageSize
	rows, err := database.GetDB().Query(
		"SELECT id, name, contact_person, phone, email, company, start_date, address, notes, created_at, updated_at FROM users "+where+" ORDER BY created_at DESC LIMIT ? OFFSET ?",
		append(args, pageSize, offset)...)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse("查询失败"))
		return
	}
	defer rows.Close()

	users := []models.User{}
	for rows.Next() {
		var u models.User
		rows.Scan(&u.ID, &u.Name, &u.ContactPerson, &u.Phone, &u.Email, &u.Company,
			&u.StartDate, &u.Address, &u.Notes, &u.CreatedAt, &u.UpdatedAt)
		users = append(users, u)
	}

	c.JSON(http.StatusOK, models.SuccessResponse(models.PaginatedResult{
		Items: users, Total: total, Page: page, PageSize: pageSize,
	}))
}

func (h *UserHandler) Get(c *gin.Context) {
	id := c.Param("id")
	var u models.User
	err := database.GetDB().QueryRow(
		"SELECT id, name, contact_person, phone, email, company, start_date, address, notes, created_at, updated_at FROM users WHERE id = ?", id,
	).Scan(&u.ID, &u.Name, &u.ContactPerson, &u.Phone, &u.Email, &u.Company,
		&u.StartDate, &u.Address, &u.Notes, &u.CreatedAt, &u.UpdatedAt)
	if err != nil {
		c.JSON(http.StatusNotFound, models.ErrorResponse("用户不存在"))
		return
	}
	c.JSON(http.StatusOK, models.SuccessResponse(u))
}

func (h *UserHandler) Create(c *gin.Context) {
	var u models.User
	if err := c.ShouldBindJSON(&u); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse("无效的请求数据"))
		return
	}
	if u.Name == "" {
		c.JSON(http.StatusBadRequest, models.ErrorResponse("用户名称不能为空"))
		return
	}

	result, err := database.GetDB().Exec(
		`INSERT INTO users (name, contact_person, phone, email, company, start_date, address, notes)
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

func (h *UserHandler) Update(c *gin.Context) {
	id := c.Param("id")
	var u models.User
	if err := c.ShouldBindJSON(&u); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse("无效的请求数据"))
		return
	}

	_, err := database.GetDB().Exec(
		`UPDATE users SET name=?, contact_person=?, phone=?, email=?, company=?, start_date=?, address=?, notes=?, updated_at=CURRENT_TIMESTAMP WHERE id=?`,
		u.Name, u.ContactPerson, u.Phone, u.Email, u.Company, u.StartDate, u.Address, u.Notes, id,
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse("更新失败"))
		return
	}
	c.JSON(http.StatusOK, models.SuccessResponse(nil))
}

func (h *UserHandler) Delete(c *gin.Context) {
	id := c.Param("id")
	_, err := database.GetDB().Exec("DELETE FROM users WHERE id = ?", id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse("删除失败"))
		return
	}
	c.JSON(http.StatusOK, models.SuccessResponse(nil))
}
