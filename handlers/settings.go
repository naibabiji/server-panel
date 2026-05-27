package handlers

import (
	"database/sql"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/naibabiji/server-panel/database"
	"github.com/naibabiji/server-panel/models"
)

type SettingsHandler struct {
	DB *sql.DB
}

func (h *SettingsHandler) db() *sql.DB {
	if h.DB != nil {
		return h.DB
	}
	return database.GetDB()
}

func (h *SettingsHandler) GetOSList(c *gin.Context) {
	h.getSetting(c, "os_list")
}

func (h *SettingsHandler) GetSiteTypeList(c *gin.Context) {
	h.getSetting(c, "site_type_list")
}

func (h *SettingsHandler) getSetting(c *gin.Context, key string) {
	var value string
	err := h.db().QueryRow("SELECT svalue FROM settings WHERE skey = ?", key).Scan(&value)
	if err != nil {
		c.JSON(http.StatusNotFound, models.ErrorResponse("设置不存在"))
		return
	}

	c.JSON(http.StatusOK, models.SuccessResponse(map[string]string{
		"skey":   key,
		"svalue": value,
	}))
}
