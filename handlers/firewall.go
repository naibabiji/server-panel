package handlers

import (
	"database/sql"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/naibabiji/server-panel/database"
	"github.com/naibabiji/server-panel/executor"
	"github.com/naibabiji/server-panel/i18n"
	"github.com/naibabiji/server-panel/models"
)

type FirewallHandler struct {
	DB *sql.DB
}

func (h *FirewallHandler) db() *sql.DB {
	if h.DB != nil {
		return h.DB
	}
	return database.GetDB()
}

func (h *FirewallHandler) ListBans(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "30"))
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 30
	}

	var total int
	h.db().QueryRow("SELECT COUNT(*) FROM firewall_bans WHERE unbanned_at IS NULL").Scan(&total)

	offset := (page - 1) * pageSize
	rows, err := h.db().Query(
		`SELECT id, ip_address, reason, source, expires_at, unbanned_at, created_at
		 FROM firewall_bans WHERE unbanned_at IS NULL ORDER BY created_at DESC LIMIT ? OFFSET ?`,
		pageSize, offset)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse(i18n.TE(c.Request, "errors.query_failed")))
		return
	}
	defer rows.Close()

	bans := []models.FirewallBan{}
	for rows.Next() {
		var b models.FirewallBan
		var expiresAt, unbannedAt sql.NullString
		rows.Scan(&b.ID, &b.IPAddress, &b.Reason, &b.Source, &expiresAt, &unbannedAt, &b.CreatedAt)
		if expiresAt.Valid {
			b.ExpiresAt = expiresAt.String
		}
		bans = append(bans, b)
	}
	c.JSON(http.StatusOK, models.SuccessResponse(models.PaginatedResult{
		Items: bans, Total: total, Page: page, PageSize: pageSize,
	}))
}

func (h *FirewallHandler) Unban(c *gin.Context) {
	id := c.Param("id")
	var ip string
	err := h.db().QueryRow("SELECT ip_address FROM firewall_bans WHERE id = ? AND unbanned_at IS NULL", id).Scan(&ip)
	if err != nil {
		c.JSON(http.StatusNotFound, models.ErrorResponse(i18n.TE(c.Request, "errors.firewall.ban_not_found")))
		return
	}
	executor.UnbanIP(ip)
	c.JSON(http.StatusOK, models.SuccessResponse(map[string]string{"message": i18n.TE(c.Request, "firewall.unbanned")}))
}

func (h *FirewallHandler) ListWhitelist(c *gin.Context) {
	rows, err := h.db().Query("SELECT id, ip_address, notes, created_at FROM whitelist ORDER BY created_at DESC")
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse(i18n.TE(c.Request, "errors.query_failed")))
		return
	}
	defer rows.Close()

	items := []models.WhitelistEntry{}
	for rows.Next() {
		var w models.WhitelistEntry
		rows.Scan(&w.ID, &w.IPAddress, &w.Notes, &w.CreatedAt)
		items = append(items, w)
	}
	c.JSON(http.StatusOK, models.SuccessResponse(items))
}

func (h *FirewallHandler) AddWhitelist(c *gin.Context) {
	var req struct {
		IPAddress string `json:"ip_address"`
		Notes     string `json:"notes"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.IPAddress == "" {
		c.JSON(http.StatusBadRequest, models.ErrorResponse(i18n.TE(c.Request, "errors.firewall.enter_ip")))
		return
	}
	_, err := h.db().Exec("INSERT INTO whitelist (ip_address, notes) VALUES (?,?)", req.IPAddress, req.Notes)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse(i18n.TE(c.Request, "errors.firewall.add_failed_exists")))
		return
	}
	executor.UnbanIP(req.IPAddress)
	c.JSON(http.StatusOK, models.SuccessResponse(nil))
}

func (h *FirewallHandler) DeleteWhitelist(c *gin.Context) {
	id := c.Param("id")
	result, err := h.db().Exec("DELETE FROM whitelist WHERE id = ?", id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse(i18n.TE(c.Request, "errors.delete_failed")))
		return
	}
	if rows, _ := result.RowsAffected(); rows == 0 {
		c.JSON(http.StatusNotFound, models.ErrorResponse(i18n.TE(c.Request, "errors.firewall.whitelist_not_found")))
		return
	}
	c.JSON(http.StatusOK, models.SuccessResponse(nil))
}
