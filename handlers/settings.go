package handlers

import (
	"database/sql"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/naibabiji/server-panel/config"
	"github.com/naibabiji/server-panel/database"
	"github.com/naibabiji/server-panel/models"
	"golang.org/x/crypto/bcrypt"
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
	c.JSON(http.StatusOK, models.SuccessResponse(map[string]string{"skey": key, "svalue": value}))
}

func (h *SettingsHandler) GetPanelTitle(c *gin.Context) {
	var title string
	h.db().QueryRow("SELECT svalue FROM settings WHERE skey = 'panel_title'").Scan(&title)
	c.JSON(http.StatusOK, models.SuccessResponse(map[string]string{"panel_title": title}))
}

func (h *SettingsHandler) UpdatePanelTitle(c *gin.Context) {
	var req struct {
		PanelTitle string `json:"panel_title"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse("无效的请求数据"))
		return
	}
	h.db().Exec("INSERT OR REPLACE INTO settings (skey, svalue) VALUES ('panel_title', ?)", req.PanelTitle)
	if config.AppConfig != nil {
		config.AppConfig.Panel.PanelTitle = req.PanelTitle
	}
	c.JSON(http.StatusOK, models.SuccessResponse(nil))
}

func (h *SettingsHandler) GetSMTPConfig(c *gin.Context) {
	cfg := make(map[string]string)
	keys := []string{"smtp_host", "smtp_port", "smtp_encryption", "smtp_user", "smtp_pass", "admin_email"}
	for _, k := range keys {
		var v string
		h.db().QueryRow("SELECT svalue FROM settings WHERE skey = ?", k).Scan(&v)
		cfg[k] = v
	}
	c.JSON(http.StatusOK, models.SuccessResponse(cfg))
}

func (h *SettingsHandler) UpdateSMTPConfig(c *gin.Context) {
	var req map[string]string
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse("无效的请求数据"))
		return
	}
	for k, v := range req {
		h.db().Exec("INSERT OR REPLACE INTO settings (skey, svalue) VALUES (?, ?)", k, v)
	}
	c.JSON(http.StatusOK, models.SuccessResponse(nil))
}

func (h *SettingsHandler) ChangePassword(c *gin.Context) {
	var req struct {
		OldPassword string `json:"old_password"`
		NewPassword string `json:"new_password"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.NewPassword == "" {
		c.JSON(http.StatusBadRequest, models.ErrorResponse("请输入新密码"))
		return
	}
	if len(req.NewPassword) < 6 {
		c.JSON(http.StatusBadRequest, models.ErrorResponse("密码至少需要 6 个字符"))
		return
	}

	var currentHash string
	h.db().QueryRow("SELECT password_hash FROM admin_users LIMIT 1").Scan(&currentHash)
	if bcrypt.CompareHashAndPassword([]byte(currentHash), []byte(req.OldPassword)) != nil {
		c.JSON(http.StatusUnauthorized, models.ErrorResponse("旧密码错误"))
		return
	}

	newHash, err := bcrypt.GenerateFromPassword([]byte(req.NewPassword), bcrypt.DefaultCost)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse("密码处理失败"))
		return
	}
	h.db().Exec("UPDATE admin_users SET password_hash = ?, updated_at = CURRENT_TIMESTAMP", string(newHash))
	c.JSON(http.StatusOK, models.SuccessResponse(map[string]string{"message": "密码修改成功"}))
}

func (h *SettingsHandler) UpdateOSList(c *gin.Context) {
	h.updateListSetting(c, "os_list")
}

func (h *SettingsHandler) UpdateSiteTypeList(c *gin.Context) {
	h.updateListSetting(c, "site_type_list")
}

func (h *SettingsHandler) updateListSetting(c *gin.Context, key string) {
	var req struct {
		SValue string `json:"svalue"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse("无效的请求数据"))
		return
	}
	h.db().Exec("INSERT OR REPLACE INTO settings (skey, svalue) VALUES (?, ?)", key, req.SValue)
	c.JSON(http.StatusOK, models.SuccessResponse(nil))
}

func (h *SettingsHandler) GetCronStatus(c *gin.Context) {
	// 查询最近告警日志作为计划任务运行状态的参考
	var lastCheck string
	h.db().QueryRow("SELECT MAX(created_at) FROM alert_log").Scan(&lastCheck)
	var lastProbe string
	h.db().QueryRow("SELECT MAX(http_probe_last_at) FROM servers WHERE http_probe_enabled = 1").Scan(&lastProbe)

	c.JSON(http.StatusOK, models.SuccessResponse(map[string]interface{}{
		"last_alert_check": lastCheck,
		"last_http_probe":  lastProbe,
	}))
}
