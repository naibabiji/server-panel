package handlers

import (
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/naibabiji/server-panel/database"
	"github.com/naibabiji/server-panel/middleware"
	"github.com/naibabiji/server-panel/models"
	"golang.org/x/crypto/bcrypt"
)

type StatusPageHandler struct {
	DB *sql.DB
}

func (h *StatusPageHandler) db() *sql.DB {
	if h.DB != nil {
		return h.DB
	}
	return database.GetDB()
}

func (h *StatusPageHandler) GetInfo(c *gin.Context) {
	token := c.Param("token")
	if _, ok := h.authorizeStatusToken(c, token); !ok {
		return
	}

	var s models.Server
	var probeHealthy sql.NullInt64
	var probeLast sql.NullString
	err := h.db().QueryRow(
		`SELECT name, is_online, http_probe_healthy, http_probe_last_at
		 FROM servers WHERE status_page_enabled = 1 AND status_page_token = ? AND status_page_token <> ''`, token,
	).Scan(&s.Name, &s.IsOnline, &probeHealthy, &probeLast)
	if err != nil {
		c.JSON(http.StatusNotFound, models.ErrorResponse("状态页不存在"))
		return
	}
	if probeHealthy.Valid {
		v := int(probeHealthy.Int64)
		s.HTTPProbeHealthy = &v
	}
	if probeLast.Valid {
		s.HTTPProbeLastAt = probeLast.String
	}

	c.JSON(http.StatusOK, models.SuccessResponse(map[string]interface{}{
		"name":               s.Name,
		"is_online":          s.IsOnline,
		"http_probe_healthy": s.HTTPProbeHealthy,
		"http_probe_last_at": s.HTTPProbeLastAt,
	}))
}

func (h *StatusPageHandler) GetMetrics(c *gin.Context) {
	token := c.Param("token")

	serverID, ok := h.authorizeStatusToken(c, token)
	if !ok {
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
		var cpu, mem, disk, load sql.NullFloat64
		if err := rows.Scan(&p.Time, &cpu, &mem, &disk, &load); err != nil {
			c.JSON(http.StatusInternalServerError, models.ErrorResponse("读取指标失败"))
			return
		}
		if cpu.Valid {
			p.CPU = cpu.Float64
		}
		if mem.Valid {
			p.Mem = mem.Float64
		}
		if disk.Valid {
			p.Disk = disk.Float64
		}
		if load.Valid {
			p.Load = load.Float64
		}
		points = append(points, p)
	}
	c.JSON(http.StatusOK, models.SuccessResponse(points))
}

func (h *StatusPageHandler) GetWebsites(c *gin.Context) {
	token := c.Param("token")
	if _, ok := h.authorizeStatusToken(c, token); !ok {
		return
	}

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
		var expiryDate sql.NullString
		if err := rows.Scan(&w.Domain, &w.Status, &expiryDate); err != nil {
			c.JSON(http.StatusInternalServerError, models.ErrorResponse("读取网站失败"))
			return
		}
		if expiryDate.Valid {
			w.ExpiryDate = expiryDate.String
		}
		websites = append(websites, w)
	}
	c.JSON(http.StatusOK, models.SuccessResponse(websites))
}

func (h *StatusPageHandler) VerifyPassword(c *gin.Context) {
	token := c.Param("token")
	var serverID int64
	var pwHash string
	err := h.db().QueryRow(
		"SELECT id, status_page_password FROM servers WHERE status_page_enabled = 1 AND status_page_token = ? AND status_page_token <> ''",
		token,
	).Scan(&serverID, &pwHash)
	if err != nil {
		c.JSON(http.StatusNotFound, models.ErrorResponse("状态页不存在"))
		return
	}
	if pwHash == "" {
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

	http.SetCookie(c.Writer, &http.Cookie{
		Name:     statusCookieName(token),
		Value:    statusCookieValue(token, pwHash),
		MaxAge:   86400,
		Path:     "/",
		HttpOnly: true,
		Secure:   middleware.IsSecureRequest(c),
		SameSite: http.SameSiteLaxMode,
	})
	c.JSON(http.StatusOK, models.SuccessResponse(map[string]bool{"verified": true}))
}

func (h *StatusPageHandler) authorizeStatusToken(c *gin.Context, token string) (int64, bool) {
	var serverID int64
	var pwHash string
	err := h.db().QueryRow(
		"SELECT id, status_page_password FROM servers WHERE status_page_enabled = 1 AND status_page_token = ? AND status_page_token <> ''",
		token,
	).Scan(&serverID, &pwHash)
	if err != nil {
		c.JSON(http.StatusNotFound, models.ErrorResponse("状态页不存在"))
		return 0, false
	}
	if pwHash == "" {
		return serverID, true
	}

	cookieValue, err := c.Cookie(statusCookieName(token))
	if err != nil || !hmac.Equal([]byte(cookieValue), []byte(statusCookieValue(token, pwHash))) {
		c.JSON(http.StatusForbidden, models.ErrorResponse("需要密码"))
		return 0, false
	}
	return serverID, true
}

func statusCookieName(token string) string {
	sum := sha256.Sum256([]byte(token))
	return "sp_status_" + hex.EncodeToString(sum[:8])
}

func statusCookieValue(token, pwHash string) string {
	mac := hmac.New(sha256.New, []byte(pwHash))
	mac.Write([]byte(token))
	return hex.EncodeToString(mac.Sum(nil))
}
