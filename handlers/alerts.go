package handlers

import (
	"database/sql"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/naibabiji/server-panel/database"
	"github.com/naibabiji/server-panel/executor"
	"github.com/naibabiji/server-panel/models"
)

type AlertHandler struct {
	DB *sql.DB
}

func (h *AlertHandler) db() *sql.DB {
	if h.DB != nil {
		return h.DB
	}
	return database.GetDB()
}

func (h *AlertHandler) ListRules(c *gin.Context) {
	rows, err := h.db().Query(
		`SELECT r.id, r.alert_type, r.name, r.enabled, r.threshold_value, r.threshold_count,
		 r.notify_user, r.notify_email, r.server_id, COALESCE(s.name,''), r.created_at, r.updated_at
		 FROM alert_rules r LEFT JOIN servers s ON r.server_id = s.id ORDER BY r.alert_type`)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse("查询失败"))
		return
	}
	defer rows.Close()

	rules := []models.AlertRule{}
	for rows.Next() {
		var r models.AlertRule
		rows.Scan(&r.ID, &r.AlertType, &r.Name, &r.Enabled, &r.ThresholdValue, &r.ThresholdCount,
			&r.NotifyUser, &r.NotifyEmail, &r.ServerID, &r.ServerName, &r.CreatedAt, &r.UpdatedAt)
		rules = append(rules, r)
	}
	c.JSON(http.StatusOK, models.SuccessResponse(rules))
}

func (h *AlertHandler) CreateRule(c *gin.Context) {
	var r models.AlertRule
	if err := c.ShouldBindJSON(&r); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse("无效的请求数据"))
		return
	}
	if !normalizeAlertRule(&r) {
		c.JSON(http.StatusBadRequest, models.ErrorResponse("无效的告警类型"))
		return
	}
	result, err := h.db().Exec(
		`INSERT INTO alert_rules (alert_type, name, enabled, threshold_value, threshold_count, notify_user, notify_email, server_id)
		 VALUES (?,?,?,?,?,?,?,?)`,
		r.AlertType, r.Name, r.Enabled, r.ThresholdValue, r.ThresholdCount, r.NotifyUser, r.NotifyEmail, r.ServerID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse("创建失败"))
		return
	}
	id, _ := result.LastInsertId()
	c.JSON(http.StatusOK, models.SuccessResponse(map[string]int64{"id": id}))
}

func (h *AlertHandler) UpdateRule(c *gin.Context) {
	id := c.Param("id")
	var r models.AlertRule
	if err := c.ShouldBindJSON(&r); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse("无效的请求数据"))
		return
	}
	if !normalizeAlertRule(&r) {
		c.JSON(http.StatusBadRequest, models.ErrorResponse("无效的告警类型"))
		return
	}
	result, err := h.db().Exec(
		`UPDATE alert_rules SET alert_type=?, name=?, enabled=?, threshold_value=?, threshold_count=?, notify_user=?, notify_email=?, server_id=?, updated_at=CURRENT_TIMESTAMP WHERE id=?`,
		r.AlertType, r.Name, r.Enabled, r.ThresholdValue, r.ThresholdCount, r.NotifyUser, r.NotifyEmail, r.ServerID, id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse("更新失败"))
		return
	}
	if rows, _ := result.RowsAffected(); rows == 0 {
		c.JSON(http.StatusNotFound, models.ErrorResponse("规则不存在"))
		return
	}
	c.JSON(http.StatusOK, models.SuccessResponse(nil))
}

func normalizeAlertRule(r *models.AlertRule) bool {
	r.AlertType = strings.TrimSpace(r.AlertType)
	r.Name = strings.TrimSpace(r.Name)
	r.NotifyEmail = strings.TrimSpace(r.NotifyEmail)
	if r.ThresholdCount <= 0 {
		r.ThresholdCount = 3
	}

	switch r.AlertType {
	case "server_expiry":
		if r.Name == "" {
			r.Name = "服务器到期提醒"
		}
		if r.ThresholdValue <= 0 {
			r.ThresholdValue = 30
		}
	case "website_expiry":
		if r.Name == "" {
			r.Name = "网站到期提醒"
		}
		if r.ThresholdValue <= 0 {
			r.ThresholdValue = 30
		}
	case "http_probe_down":
		if r.Name == "" {
			r.Name = "HTTP 探测异常"
		}
		r.ThresholdValue = 0
	case "cpu_high":
		if r.Name == "" {
			r.Name = "CPU 过高"
		}
		if r.ThresholdValue <= 0 {
			r.ThresholdValue = 90
		}
	case "memory_high":
		if r.Name == "" {
			r.Name = "内存过高"
		}
		if r.ThresholdValue <= 0 {
			r.ThresholdValue = 90
		}
	case "disk_high":
		if r.Name == "" {
			r.Name = "磁盘过高"
		}
		if r.ThresholdValue <= 0 {
			r.ThresholdValue = 90
		}
	case "server_offline":
		if r.Name == "" {
			r.Name = "服务器离线"
		}
		if r.ThresholdValue <= 0 {
			r.ThresholdValue = 5
		}
	default:
		return false
	}
	return true
}

func (h *AlertHandler) DeleteRule(c *gin.Context) {
	id := c.Param("id")
	result, err := h.db().Exec("DELETE FROM alert_rules WHERE id = ?", id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse("删除失败"))
		return
	}
	if rows, _ := result.RowsAffected(); rows == 0 {
		c.JSON(http.StatusNotFound, models.ErrorResponse("规则不存在"))
		return
	}
	c.JSON(http.StatusOK, models.SuccessResponse(nil))
}

func (h *AlertHandler) GetLog(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "30"))
	alertType := c.Query("type")
	serverID := c.Query("server_id")
	resolved := c.Query("resolved")

	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 30
	}

	where := "WHERE 1=1"
	args := []interface{}{}
	if alertType != "" {
		where += " AND alert_type = ?"
		args = append(args, alertType)
	}
	if serverID != "" {
		where += " AND server_id = ?"
		args = append(args, serverID)
	}
	if resolved != "" {
		where += " AND resolved = ?"
		args = append(args, resolved)
	}

	var total int
	h.db().QueryRow("SELECT COUNT(*) FROM alert_log "+where, args...).Scan(&total)

	offset := (page - 1) * pageSize
	rows, err := h.db().Query(
		"SELECT id, alert_type, server_id, website_id, level, message, resolved, created_at FROM alert_log "+where+" ORDER BY created_at DESC LIMIT ? OFFSET ?",
		append(args, pageSize, offset)...)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse("查询失败"))
		return
	}
	defer rows.Close()

	logs := []models.AlertLog{}
	for rows.Next() {
		var l models.AlertLog
		rows.Scan(&l.ID, &l.AlertType, &l.ServerID, &l.WebsiteID, &l.Level, &l.Message, &l.Resolved, &l.CreatedAt)
		logs = append(logs, l)
	}
	c.JSON(http.StatusOK, models.SuccessResponse(models.PaginatedResult{
		Items: logs, Total: total, Page: page, PageSize: pageSize,
	}))
}

func (h *AlertHandler) TestSMTP(c *gin.Context) {
	var req struct {
		Email string `json:"email"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.Email == "" {
		c.JSON(http.StatusBadRequest, models.ErrorResponse("请输入测试邮箱"))
		return
	}
	if err := executor.TestSMTP(req.Email); err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse("SMTP 测试失败: "+err.Error()))
		return
	}
	c.JSON(http.StatusOK, models.SuccessResponse(map[string]string{"message": "测试邮件已发送"}))
}
