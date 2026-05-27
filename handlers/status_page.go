package handlers

import (
	"database/sql"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/naibabiji/server-panel/database"
	"github.com/naibabiji/server-panel/models"
	"golang.org/x/crypto/bcrypt"
)

type StatusPageHandler struct {
	DB *sql.DB
}

func (h *StatusPageHandler) db() *sql.DB {
	if h.DB != nil { return h.DB }
	return database.GetDB()
}

func (h *StatusPageHandler) GetInfo(c *gin.Context) {
	token := c.Param("token")
	var s models.Server
	var probeHealthy sql.NullInt64
	err := h.db().QueryRow(
		`SELECT name, is_online, http_probe_healthy, http_probe_last_at
		 FROM servers WHERE status_page_enabled = 1 AND status_page_token = ? AND status_page_token <> ''`, token,
	).Scan(&s.Name, &s.IsOnline, &probeHealthy, &s.HTTPProbeLastAt)
	if err != nil {
		c.JSON(http.StatusNotFound, models.ErrorResponse("状态页不存在"))
		return
	}
	if probeHealthy.Valid {
		v := int(probeHealthy.Int64)
		s.HTTPProbeHealthy = &v
	}

	c.JSON(http.StatusOK, models.SuccessResponse(map[string]interface{}{
		"name":              s.Name,
		"is_online":         s.IsOnline,
		"http_probe_healthy": s.HTTPProbeHealthy,
		"http_probe_last_at": s.HTTPProbeLastAt,
	}))
}

func (h *StatusPageHandler) GetMetrics(c *gin.Context) {
	token := c.Param("token")

	var serverID int64
	err := h.db().QueryRow(
		"SELECT id FROM servers WHERE status_page_enabled = 1 AND status_page_token = ? AND status_page_token <> ''", token,
	).Scan(&serverID)
	if err != nil {
		c.JSON(http.StatusNotFound, models.ErrorResponse("状态页不存在"))
		return
	}

	rows, err := h.db().Query(
		`SELECT recorded_at, cpu_percent, memory_percent, disk_percent, load_avg_1
		 FROM metrics WHERE server_id = ? ORDER BY recorded_at DESC LIMIT 288`, serverID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse("查询失败"))
		return
	}
	defer rows.Close()

	type point struct {
		Time string  `json:"t"`
		CPU  float64 `json:"cpu"`
		Mem  float64 `json:"mem"`
		Disk float64 `json:"disk"`
		Load float64 `json:"load"`
	}
	points := []point{}
	for rows.Next() {
		var p point
		rows.Scan(&p.Time, &p.CPU, &p.Mem, &p.Disk, &p.Load)
		points = append(points, p)
	}
	c.JSON(http.StatusOK, models.SuccessResponse(points))
}

func (h *StatusPageHandler) GetWebsites(c *gin.Context) {
	token := c.Param("token")

	rows, err := h.db().Query(
		`SELECT w.domain, w.status, w.expiry_date FROM websites w
		 JOIN servers s ON w.server_id = s.id
		 WHERE s.status_page_enabled = 1 AND s.status_page_token = ? AND s.status_page_token <> ''
		 AND w.status = 'active'`, token)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse("查询失败"))
		return
	}
	defer rows.Close()

	type webInfo struct {
		Domain     string `json:"domain"`
		Status     string `json:"status"`
		ExpiryDate string `json:"expiry_date"`
	}
	websites := []webInfo{}
	for rows.Next() {
		var w webInfo
		rows.Scan(&w.Domain, &w.Status, &w.ExpiryDate)
		websites = append(websites, w)
	}
	c.JSON(http.StatusOK, models.SuccessResponse(websites))
}

func (h *StatusPageHandler) VerifyPassword(c *gin.Context) {
	token := c.Param("token")
	var pwHash string
	err := h.db().QueryRow(
		"SELECT status_page_password FROM servers WHERE status_page_enabled = 1 AND status_page_token = ? AND status_page_token <> ''", token,
	).Scan(&pwHash)
	if err != nil || pwHash == "" {
		c.JSON(http.StatusOK, models.SuccessResponse(map[string]bool{"verified": true}))
		return
	}

	var req struct {
		Password string `json:"password"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse("无效的请求"))
		return
	}

	if bcrypt.CompareHashAndPassword([]byte(pwHash), []byte(req.Password)) != nil {
		c.JSON(http.StatusUnauthorized, models.ErrorResponse("密码错误"))
		return
	}
	c.JSON(http.StatusOK, models.SuccessResponse(map[string]bool{"verified": true}))
}
