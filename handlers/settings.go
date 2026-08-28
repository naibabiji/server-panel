package handlers

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"database/sql"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"net/mail"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/naibabiji/server-panel/config"
	"github.com/naibabiji/server-panel/database"
	"github.com/naibabiji/server-panel/executor"
	"github.com/naibabiji/server-panel/i18n"
	"github.com/naibabiji/server-panel/middleware"
	"github.com/naibabiji/server-panel/models"
	"golang.org/x/crypto/bcrypt"
)

type SettingsHandler struct {
	DB                    *sql.DB
	AfterRestoreScheduled func()
}

var panelSuffixPattern = regexp.MustCompile(`^[A-Za-z0-9]{4,}$`)

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
		c.JSON(http.StatusNotFound, models.ErrorResponse(i18n.TE(c.Request, "errors.settings.setting_not_found")))
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
		c.JSON(http.StatusBadRequest, models.ErrorResponse(i18n.TE(c.Request, "errors.invalid_request")))
		return
	}
	h.db().Exec("INSERT OR REPLACE INTO settings (skey, svalue) VALUES ('panel_title', ?)", req.PanelTitle)
	if config.AppConfig != nil {
		config.AppConfig.Panel.PanelTitle = req.PanelTitle
		config.SaveConfig(config.AppConfig)
	}
	c.JSON(http.StatusOK, models.SuccessResponse(nil))
}

func (h *SettingsHandler) GetPanelAccess(c *gin.Context) {
	cfg := config.AppConfig
	if cfg == nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse(i18n.TE(c.Request, "errors.settings.config_not_loaded")))
		return
	}
	c.JSON(http.StatusOK, models.SuccessResponse(map[string]interface{}{
		"tls_port":            cfg.Panel.TLSPort,
		"random_suffix":       cfg.Panel.RandomSuffix,
		"trusted_proxies":     cfg.Panel.TrustedProxies,
		"trust_reverse_proxy": len(cfg.Panel.TrustedProxies) > 0,
		"trust_cloudflare":    cfg.Panel.TrustCloudflare,
		"restart_note":        i18n.TE(c.Request, "errors.settings.restart_note"),
	}))
}

func (h *SettingsHandler) UpdatePanelAccess(c *gin.Context) {
	var req struct {
		TLSPort           int       `json:"tls_port"`
		RandomSuffix      string    `json:"random_suffix"`
		TrustedProxies    *[]string `json:"trusted_proxies"`
		TrustReverseProxy *bool     `json:"trust_reverse_proxy"`
		TrustCloudflare   *bool     `json:"trust_cloudflare"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse(i18n.TE(c.Request, "errors.invalid_request")))
		return
	}

	cfg := config.AppConfig
	if cfg == nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse(i18n.TE(c.Request, "errors.settings.config_not_loaded")))
		return
	}

	if req.TLSPort < 1 || req.TLSPort > 65535 {
		c.JSON(http.StatusBadRequest, models.ErrorResponse(i18n.TE(c.Request, "errors.settings.https_port_range")))
		return
	}
	if !panelSuffixPattern.MatchString(req.RandomSuffix) {
		c.JSON(http.StatusBadRequest, models.ErrorResponse(i18n.TE(c.Request, "errors.settings.random_path_format")))
		return
	}

	portChanged := req.TLSPort != cfg.Panel.TLSPort
	if portChanged {
		if err := checkPortAvailable(req.TLSPort); err != nil {
			c.JSON(http.StatusBadRequest, models.ErrorResponse(i18n.TE(c.Request, "errors.settings.https_port_unavailable", i18n.P{"error": err.Error()})))
			return
		}
	}

	trustedProxies := cfg.Panel.TrustedProxies
	if req.TrustReverseProxy != nil || req.TrustedProxies != nil {
		useReverseProxy := len(cfg.Panel.TrustedProxies) > 0
		if req.TrustReverseProxy != nil {
			useReverseProxy = *req.TrustReverseProxy
		} else if req.TrustedProxies != nil {
			useReverseProxy = len(*req.TrustedProxies) > 0
		}

		trustedProxies = []string{}
		if useReverseProxy {
			input := []string{"127.0.0.1", "::1"}
			if req.TrustedProxies != nil && len(*req.TrustedProxies) > 0 {
				input = *req.TrustedProxies
			}
			var err error
			trustedProxies, err = normalizeTrustedProxies(c, input)
			if err != nil {
				c.JSON(http.StatusBadRequest, models.ErrorResponse(err.Error()))
				return
			}
		}
	}

	trustCloudflare := cfg.Panel.TrustCloudflare
	if req.TrustCloudflare != nil {
		trustCloudflare = *req.TrustCloudflare
	}
	if portChanged {
		if _, err := executor.AllowPanelPort(req.TLSPort); err != nil {
			c.JSON(http.StatusInternalServerError, models.ErrorResponse(i18n.TE(c.Request, "errors.settings.firewall_open_failed", i18n.P{"error": err.Error()})))
			return
		}
	}

	changed := req.TLSPort != cfg.Panel.TLSPort ||
		req.RandomSuffix != cfg.Panel.RandomSuffix ||
		trustCloudflare != cfg.Panel.TrustCloudflare ||
		!equalStringSlices(trustedProxies, cfg.Panel.TrustedProxies)
	cfg.Panel.TLSPort = req.TLSPort
	cfg.Panel.RandomSuffix = req.RandomSuffix
	cfg.Panel.TrustedProxies = trustedProxies
	cfg.Panel.TrustCloudflare = trustCloudflare
	if err := config.SaveConfig(cfg); err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse(i18n.TE(c.Request, "errors.settings.save_config_failed")))
		return
	}
	if changed {
		executor.RestartPanelService()
	}

	message := i18n.TE(c.Request, "errors.settings.access_saved")
	if changed {
		message = i18n.TE(c.Request, "errors.settings.access_saved_restarting")
	}
	c.JSON(http.StatusOK, models.SuccessResponse(map[string]interface{}{
		"message":             message,
		"tls_port":            cfg.Panel.TLSPort,
		"random_suffix":       cfg.Panel.RandomSuffix,
		"trusted_proxies":     cfg.Panel.TrustedProxies,
		"trust_reverse_proxy": len(cfg.Panel.TrustedProxies) > 0,
		"trust_cloudflare":    cfg.Panel.TrustCloudflare,
	}))
}

func normalizeTrustedProxies(c *gin.Context, values []string) ([]string, error) {
	out := make([]string, 0, len(values))
	seen := make(map[string]bool, len(values))
	for _, raw := range values {
		value := strings.TrimSpace(raw)
		if value == "" {
			continue
		}
		if net.ParseIP(value) == nil {
			if _, _, err := net.ParseCIDR(value); err != nil {
				return nil, fmt.Errorf("%s", i18n.TE(c.Request, "errors.settings.trusted_proxy_invalid", i18n.P{"value": value}))
			}
		}
		if seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out, nil
}

func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func checkPortAvailable(port int) error {
	ln, err := net.Listen("tcp", ":"+strconv.Itoa(port))
	if err != nil {
		return err
	}
	return ln.Close()
}

func (h *SettingsHandler) GetSMTPConfig(c *gin.Context) {
	cfg := make(map[string]string)
	keys := []string{"smtp_host", "smtp_port", "smtp_encryption", "smtp_user", "smtp_pass", "admin_email"}
	args := make([]interface{}, len(keys))
	for i, k := range keys {
		args[i] = k
	}
	placeholders := strings.Repeat("?,", len(keys))
	rows, err := h.db().Query("SELECT skey, svalue FROM settings WHERE skey IN ("+placeholders[:len(placeholders)-1]+")", args...)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var k, v string
			if rows.Scan(&k, &v) == nil {
				cfg[k] = v
			}
		}
	}
	c.JSON(http.StatusOK, models.SuccessResponse(cfg))
}

var allowedSMTPKeys = map[string]bool{
	"smtp_host": true, "smtp_port": true, "smtp_encryption": true,
	"smtp_user": true, "smtp_pass": true, "admin_email": true,
}

func (h *SettingsHandler) UpdateSMTPConfig(c *gin.Context) {
	var req map[string]string
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse(i18n.TE(c.Request, "errors.invalid_request")))
		return
	}
	for k, v := range req {
		if !allowedSMTPKeys[k] {
			c.JSON(http.StatusBadRequest, models.ErrorResponse(i18n.TE(c.Request, "errors.settings.config_key_not_allowed", i18n.P{"key": k})))
			return
		}
		h.db().Exec("INSERT OR REPLACE INTO settings (skey, svalue) VALUES (?, ?)", k, v)
	}
	c.JSON(http.StatusOK, models.SuccessResponse(nil))
}

var backupSettingKeys = []string{
	"backup_auto_enabled",
	"backup_frequency",
	"backup_email_enabled",
	"backup_keep_count",
	"backup_max_email_mb",
	"backup_last_run_at",
	"backup_last_status",
	"backup_last_error",
}

var backupEditableKeys = map[string]bool{
	"backup_auto_enabled":  true,
	"backup_frequency":     true,
	"backup_email_enabled": true,
	"backup_keep_count":    true,
	"backup_max_email_mb":  true,
}

func (h *SettingsHandler) GetBackupSettings(c *gin.Context) {
	result := make(map[string]string, len(backupSettingKeys)+1)
	keys := make([]string, 0, len(backupSettingKeys)+1)
	keys = append(keys, backupSettingKeys...)
	keys = append(keys, "admin_email")
	args := make([]interface{}, len(keys))
	for i, k := range keys {
		args[i] = k
	}
	placeholders := strings.Repeat("?,", len(keys))
	rows, err := h.db().Query("SELECT skey, svalue FROM settings WHERE skey IN ("+placeholders[:len(placeholders)-1]+")", args...)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var k, v string
			if rows.Scan(&k, &v) == nil {
				result[k] = v
			}
		}
	}
	c.JSON(http.StatusOK, models.SuccessResponse(result))
}

func (h *SettingsHandler) UpdateBackupSettings(c *gin.Context) {
	var req map[string]string
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse(i18n.TE(c.Request, "errors.invalid_request")))
		return
	}

	for k, v := range req {
		if !backupEditableKeys[k] {
			c.JSON(http.StatusBadRequest, models.ErrorResponse(i18n.TE(c.Request, "errors.settings.config_key_not_allowed", i18n.P{"key": k})))
			return
		}
		switch k {
		case "backup_auto_enabled", "backup_email_enabled":
			if v != "true" && v != "false" {
				c.JSON(http.StatusBadRequest, models.ErrorResponse(i18n.TE(c.Request, "errors.settings.bool_only", i18n.P{"key": k})))
				return
			}
		case "backup_frequency":
			if v != "daily" && v != "weekly" {
				c.JSON(http.StatusBadRequest, models.ErrorResponse(i18n.TE(c.Request, "errors.settings.backup_freq_invalid")))
				return
			}
		case "backup_keep_count":
			n, err := strconv.Atoi(v)
			if err != nil || n < 1 || n > 365 {
				c.JSON(http.StatusBadRequest, models.ErrorResponse(i18n.TE(c.Request, "errors.settings.keep_count_range")))
				return
			}
		case "backup_max_email_mb":
			n, err := strconv.Atoi(v)
			if err != nil || n < 1 || n > 100 {
				c.JSON(http.StatusBadRequest, models.ErrorResponse(i18n.TE(c.Request, "errors.settings.email_mb_range")))
				return
			}
		}
		h.db().Exec("INSERT OR REPLACE INTO settings (skey, svalue) VALUES (?, ?)", k, v)
	}

	c.JSON(http.StatusOK, models.SuccessResponse(map[string]string{"message": i18n.TE(c.Request, "settings.backup_settings_saved")}))
}

func (h *SettingsHandler) RunDatabaseBackup(c *gin.Context) {
	var req struct {
		EmailEnabled *bool `json:"email_enabled"`
	}
	_ = c.ShouldBindJSON(&req)

	emailEnabled := false
	if req.EmailEnabled != nil {
		emailEnabled = *req.EmailEnabled
	} else {
		var value string
		h.db().QueryRow("SELECT svalue FROM settings WHERE skey = 'backup_email_enabled'").Scan(&value)
		emailEnabled = value == "true"
	}

	result, err := executor.RunDatabaseBackup("manual", emailEnabled)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse(i18n.TE(c.Request, "errors.settings.backup_failed", i18n.P{"error": err.Error()})))
		return
	}
	c.JSON(http.StatusOK, models.SuccessResponse(result))
}

func (h *SettingsHandler) ListBackups(c *gin.Context) {
	items, err := executor.ListDatabaseBackups()
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse(i18n.TE(c.Request, "errors.settings.list_backups_failed", i18n.P{"error": err.Error()})))
		return
	}
	c.JSON(http.StatusOK, models.SuccessResponse(items))
}

func (h *SettingsHandler) DownloadBackup(c *gin.Context) {
	filename := c.Query("file")
	path, err := executor.ResolveBackupPath(filename)
	if err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse(i18n.TE(c.Request, "errors.settings.invalid_backup_file")))
		return
	}
	c.FileAttachment(path, filepath.Base(path))
}

func (h *SettingsHandler) RestoreBackup(c *gin.Context) {
	var req struct {
		Filename string `json:"filename"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || strings.TrimSpace(req.Filename) == "" {
		c.JSON(http.StatusBadRequest, models.ErrorResponse(i18n.TE(c.Request, "errors.settings.provide_backup_filename")))
		return
	}
	if err := executor.ScheduleRestore(strings.TrimSpace(req.Filename)); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse(i18n.TE(c.Request, "errors.settings.restore_failed", i18n.P{"error": err.Error()})))
		return
	}
	if h.AfterRestoreScheduled != nil {
		h.AfterRestoreScheduled()
	}
	c.JSON(http.StatusOK, models.SuccessResponse(map[string]string{
		"message": i18n.TE(c.Request, "errors.settings.restore_verified"),
	}))
}

func (h *SettingsHandler) RestoreBackupUpload(c *gin.Context) {
	fileHeader, err := c.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse(i18n.TE(c.Request, "errors.settings.select_backup_file")))
		return
	}
	if !strings.HasSuffix(strings.ToLower(fileHeader.Filename), ".tar.gz") {
		c.JSON(http.StatusBadRequest, models.ErrorResponse(i18n.TE(c.Request, "errors.settings.backup_ext_only")))
		return
	}
	filename, err := executor.SaveUploadedBackup(fileHeader)
	if err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse(i18n.TE(c.Request, "errors.settings.save_upload_failed", i18n.P{"error": err.Error()})))
		return
	}
	if err := executor.ScheduleRestore(filename); err != nil {
		_ = executor.RemoveBackupFile(filename)
		c.JSON(http.StatusBadRequest, models.ErrorResponse(i18n.TE(c.Request, "errors.settings.restore_failed", i18n.P{"error": err.Error()})))
		return
	}
	if h.AfterRestoreScheduled != nil {
		h.AfterRestoreScheduled()
	}
	c.JSON(http.StatusOK, models.SuccessResponse(map[string]string{
		"message": i18n.TE(c.Request, "errors.settings.restore_upload_verified"),
	}))
}

// 账户安全（统一管理面板登录 + BasicAuth）
func (h *SettingsHandler) sessionUser(c *gin.Context) string {
	if u, ok := c.Get("session_username"); ok {
		if s, ok := u.(string); ok {
			return s
		}
	}
	return ""
}

func (h *SettingsHandler) GetAccount(c *gin.Context) {
	cfg := config.AppConfig
	webUser := h.sessionUser(c)

	basicUser := ""
	basicEnabled := false
	if cfg != nil {
		basicUser = cfg.BasicAuth.Username
		basicEnabled = cfg.Security.BasicAuthEnabled
	}

	c.JSON(http.StatusOK, models.SuccessResponse(map[string]interface{}{
		"web_username":       webUser,
		"basic_username":     basicUser,
		"basic_auth_enabled": basicEnabled,
	}))
}

func (h *SettingsHandler) UpdateAccount(c *gin.Context) {
	var req struct {
		WebUsername      string `json:"web_username"`
		WebPassword      string `json:"web_password"`
		OldPassword      string `json:"old_password"`
		BasicUsername    string `json:"basic_username"`
		BasicPassword    string `json:"basic_password"`
		BasicAuthEnabled *bool  `json:"basic_auth_enabled"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse(i18n.TE(c.Request, "errors.invalid_request")))
		return
	}

	needOldPwd := req.WebUsername != "" || req.WebPassword != ""
	if needOldPwd && req.OldPassword == "" {
		c.JSON(http.StatusBadRequest, models.ErrorResponse(i18n.TE(c.Request, "errors.settings.old_password_required")))
		return
	}

	// 面板登录账户
	if needOldPwd {
		currentUser := h.sessionUser(c)
		var currentHash string
		err := h.db().QueryRow("SELECT password_hash FROM admin_users WHERE username = ?", currentUser).Scan(&currentHash)
		if err != nil || bcrypt.CompareHashAndPassword([]byte(currentHash), []byte(req.OldPassword)) != nil {
			c.JSON(http.StatusUnauthorized, models.ErrorResponse(i18n.TE(c.Request, "errors.settings.old_password_wrong")))
			return
		}
		newUser := req.WebUsername
		if newUser == "" {
			newUser = currentUser
		}
		if req.WebPassword != "" {
			if len(req.WebPassword) < 8 {
				c.JSON(http.StatusBadRequest, models.ErrorResponse(i18n.TE(c.Request, "errors.settings.password_min_length")))
				return
			}
			newHash, err := bcrypt.GenerateFromPassword([]byte(req.WebPassword), 12)
			if err != nil {
				c.JSON(http.StatusInternalServerError, models.ErrorResponse(i18n.TE(c.Request, "errors.password_processing_failed")))
				return
			}
			h.db().Exec("UPDATE admin_users SET username = ?, password_hash = ?, updated_at = CURRENT_TIMESTAMP WHERE username = ?",
				newUser, string(newHash), currentUser)
		} else {
			h.db().Exec("UPDATE admin_users SET username = ?, updated_at = CURRENT_TIMESTAMP WHERE username = ?",
				newUser, currentUser)
		}
	}

	// BasicAuth 账户 + 开关
	cfg := config.AppConfig
	configChanged := false
	if cfg != nil && (req.BasicUsername != "" || req.BasicPassword != "" || req.BasicAuthEnabled != nil) {
		if req.BasicUsername != "" {
			cfg.BasicAuth.Username = req.BasicUsername
		}
		if req.BasicPassword != "" {
			hash, err := bcrypt.GenerateFromPassword([]byte(req.BasicPassword), 12)
			if err != nil {
				c.JSON(http.StatusInternalServerError, models.ErrorResponse(i18n.TE(c.Request, "errors.password_processing_failed")))
				return
			}
			cfg.BasicAuth.PasswordHash = string(hash)
		}
		if req.BasicAuthEnabled != nil {
			cfg.Security.BasicAuthEnabled = *req.BasicAuthEnabled
			middleware.SetBasicAuthEnabled(*req.BasicAuthEnabled)
		}
		configChanged = true
		if err := config.SaveConfig(cfg); err != nil {
			c.JSON(http.StatusInternalServerError, models.ErrorResponse(i18n.TE(c.Request, "errors.settings.save_config_failed")))
			return
		}
	}

	if !needOldPwd && !configChanged {
		c.JSON(http.StatusOK, models.SuccessResponse(map[string]string{"message": i18n.TE(c.Request, "settings.nothing_to_change")}))
		return
	}

	c.JSON(http.StatusOK, models.SuccessResponse(map[string]string{"message": i18n.TE(c.Request, "errors.settings.account_updated")}))
}

// BasicAuth 账户（保留兼容旧 API）
func (h *SettingsHandler) GetBasicAuthConfig(c *gin.Context) {
	cfg := config.AppConfig
	if cfg == nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse(i18n.TE(c.Request, "errors.settings.config_not_loaded")))
		return
	}
	c.JSON(http.StatusOK, models.SuccessResponse(map[string]string{
		"username": cfg.BasicAuth.Username,
	}))
}

func (h *SettingsHandler) UpdateBasicAuthConfig(c *gin.Context) {
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"` // 留空表示不修改密码
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.Username == "" {
		c.JSON(http.StatusBadRequest, models.ErrorResponse(i18n.TE(c.Request, "errors.settings.username_required")))
		return
	}

	cfg := config.AppConfig
	if cfg == nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse(i18n.TE(c.Request, "errors.settings.config_not_loaded")))
		return
	}

	cfg.BasicAuth.Username = req.Username
	if req.Password != "" {
		hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), 12)
		if err != nil {
			c.JSON(http.StatusInternalServerError, models.ErrorResponse(i18n.TE(c.Request, "errors.password_processing_failed")))
			return
		}
		cfg.BasicAuth.PasswordHash = string(hash)
	}

	if err := config.SaveConfig(cfg); err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse(i18n.TE(c.Request, "errors.settings.save_config_failed")))
		return
	}
	c.JSON(http.StatusOK, models.SuccessResponse(map[string]string{"message": i18n.TE(c.Request, "errors.settings.basic_auth_updated")}))
}

// 面板登录账户（兼容旧 API，当前 UI 使用 GetAccount/UpdateAccount）
func (h *SettingsHandler) GetWebAccount(c *gin.Context) {
	u := h.sessionUser(c)
	c.JSON(http.StatusOK, models.SuccessResponse(map[string]string{"username": u}))
}

func (h *SettingsHandler) UpdateWebAccount(c *gin.Context) {
	var req struct {
		Username    string `json:"username"`
		OldPassword string `json:"old_password"`
		NewPassword string `json:"new_password"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.Username == "" {
		c.JSON(http.StatusBadRequest, models.ErrorResponse(i18n.TE(c.Request, "errors.settings.username_required")))
		return
	}

	currentUser := h.sessionUser(c)
	var currentHash string
	if err := h.db().QueryRow("SELECT password_hash FROM admin_users WHERE username = ?", currentUser).Scan(&currentHash); err != nil {
		c.JSON(http.StatusUnauthorized, models.ErrorResponse(i18n.TE(c.Request, "errors.settings.old_password_wrong")))
		return
	}
	if bcrypt.CompareHashAndPassword([]byte(currentHash), []byte(req.OldPassword)) != nil {
		c.JSON(http.StatusUnauthorized, models.ErrorResponse(i18n.TE(c.Request, "errors.settings.old_password_wrong")))
		return
	}

	if req.NewPassword != "" {
		if len(req.NewPassword) < 8 {
			c.JSON(http.StatusBadRequest, models.ErrorResponse(i18n.TE(c.Request, "errors.settings.password_min_length")))
			return
		}
		newHash, _ := bcrypt.GenerateFromPassword([]byte(req.NewPassword), 12)
		h.db().Exec("UPDATE admin_users SET username = ?, password_hash = ?, updated_at = CURRENT_TIMESTAMP WHERE username = ?", req.Username, string(newHash), currentUser)
	} else {
		h.db().Exec("UPDATE admin_users SET username = ?, updated_at = CURRENT_TIMESTAMP WHERE username = ?", req.Username, currentUser)
	}

	c.JSON(http.StatusOK, models.SuccessResponse(map[string]string{"message": i18n.TE(c.Request, "errors.settings.account_updated")}))
}

func (h *SettingsHandler) ChangePassword(c *gin.Context) {
	var req struct {
		OldPassword string `json:"old_password"`
		NewPassword string `json:"new_password"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.NewPassword == "" {
		c.JSON(http.StatusBadRequest, models.ErrorResponse(i18n.TE(c.Request, "errors.settings.new_password_required")))
		return
	}
	if len(req.NewPassword) < 8 {
		c.JSON(http.StatusBadRequest, models.ErrorResponse(i18n.TE(c.Request, "errors.settings.password_min_length")))
		return
	}

	currentUser := h.sessionUser(c)
	var currentHash string
	if err := h.db().QueryRow("SELECT password_hash FROM admin_users WHERE username = ?", currentUser).Scan(&currentHash); err != nil {
		c.JSON(http.StatusUnauthorized, models.ErrorResponse(i18n.TE(c.Request, "errors.settings.old_password_wrong")))
		return
	}
	if bcrypt.CompareHashAndPassword([]byte(currentHash), []byte(req.OldPassword)) != nil {
		c.JSON(http.StatusUnauthorized, models.ErrorResponse(i18n.TE(c.Request, "errors.settings.old_password_wrong")))
		return
	}

	newHash, _ := bcrypt.GenerateFromPassword([]byte(req.NewPassword), 12)
	h.db().Exec("UPDATE admin_users SET password_hash = ?, updated_at = CURRENT_TIMESTAMP WHERE username = ?", string(newHash), currentUser)
	c.JSON(http.StatusOK, models.SuccessResponse(map[string]string{"message": i18n.TE(c.Request, "errors.settings.password_changed")}))
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
		c.JSON(http.StatusBadRequest, models.ErrorResponse(i18n.TE(c.Request, "errors.invalid_request")))
		return
	}
	h.db().Exec("INSERT OR REPLACE INTO settings (skey, svalue) VALUES (?, ?)", key, req.SValue)
	c.JSON(http.StatusOK, models.SuccessResponse(nil))
}

// TLS 配置
func (h *SettingsHandler) GetTLSConfig(c *gin.Context) {
	cfg := config.AppConfig
	if cfg == nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse(i18n.TE(c.Request, "errors.settings.config_not_loaded")))
		return
	}
	c.JSON(http.StatusOK, models.SuccessResponse(map[string]interface{}{
		"tls_mode":            cfg.Panel.TLSMode,
		"domain":              cfg.Panel.Domain,
		"acme_email":          cfg.Panel.ACMEEmail,
		"tls_port":            cfg.Panel.TLSPort,
		"tls_cert_path":       cfg.Panel.TLSCertPath,
		"tls_key_path":        cfg.Panel.TLSKeyPath,
		"acme_challenge_port": cfg.Panel.ACMEChallengePort,
	}))
}

func (h *SettingsHandler) UpdateTLSConfig(c *gin.Context) {
	var req struct {
		TLSMode   string `json:"tls_mode"`
		Domain    string `json:"domain"`
		ACMEEmail string `json:"acme_email"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse(i18n.TE(c.Request, "errors.invalid_request")))
		return
	}
	cfg := config.AppConfig
	if cfg == nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse(i18n.TE(c.Request, "errors.settings.config_not_loaded")))
		return
	}
	if req.TLSMode != "" && !allowedTLSMode(req.TLSMode) {
		c.JSON(http.StatusBadRequest, models.ErrorResponse(i18n.TE(c.Request, "errors.settings.tls_mode_invalid")))
		return
	}
	domain := strings.TrimSpace(req.Domain)
	if domain != "" && !validPanelDomain(domain) {
		c.JSON(http.StatusBadRequest, models.ErrorResponse(i18n.TE(c.Request, "errors.settings.domain_format_invalid")))
		return
	}
	acmeEmail := strings.TrimSpace(req.ACMEEmail)
	if req.TLSMode == "acme" {
		if domain == "" {
			c.JSON(http.StatusBadRequest, models.ErrorResponse(i18n.TE(c.Request, "errors.settings.acme_domain_required")))
			return
		}
		if _, err := mail.ParseAddress(acmeEmail); err != nil {
			c.JSON(http.StatusBadRequest, models.ErrorResponse(i18n.TE(c.Request, "errors.settings.acme_email_required")))
			return
		}
	}
	// 首次切换到 acme 模式时探测端口；已经在 acme 模式下运行时，端口本就被本进程占用，跳过探测避免误判
	if req.TLSMode == "acme" && cfg.Panel.TLSMode != "acme" {
		if err := checkPortAvailable(cfg.Panel.ACMEChallengePort); err != nil {
			c.JSON(http.StatusBadRequest, models.ErrorResponse(i18n.TE(c.Request, "errors.settings.acme_port_in_use", i18n.P{"port": strconv.Itoa(cfg.Panel.ACMEChallengePort)})))
			return
		}
		// The current process already owns TLSPort and will reuse it after the
		// restart, so only the separate HTTP-01 challenge port must be free.
		if _, err := executor.AllowPanelPort(cfg.Panel.ACMEChallengePort); err != nil {
			c.JSON(http.StatusInternalServerError, models.ErrorResponse(i18n.TE(c.Request, "errors.settings.acme_firewall_failed", i18n.P{"port": strconv.Itoa(cfg.Panel.ACMEChallengePort), "error": err.Error()})))
			return
		}
	}
	changed := (req.TLSMode != "" && req.TLSMode != cfg.Panel.TLSMode) || domain != cfg.Panel.Domain
	if req.TLSMode != "" {
		cfg.Panel.TLSMode = req.TLSMode
	}
	cfg.Panel.Domain = domain
	cfg.Panel.ACMEEmail = acmeEmail
	if err := config.SaveConfig(cfg); err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse(i18n.TE(c.Request, "errors.settings.save_config_failed")))
		return
	}
	if changed {
		executor.RestartPanelService()
	}
	message := i18n.TE(c.Request, "errors.settings.tls_updated")
	if changed {
		message = i18n.TE(c.Request, "errors.settings.tls_updated_restarting")
	}
	if changed && cfg.Panel.TLSMode == "acme" {
		message = i18n.TE(c.Request, "errors.settings.acme_saved")
	}
	c.JSON(http.StatusOK, models.SuccessResponse(map[string]string{"message": message}))
}

func (h *SettingsHandler) IssueTLS(c *gin.Context) {
	cfg := config.AppConfig
	if cfg == nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse(i18n.TE(c.Request, "errors.settings.config_not_loaded")))
		return
	}
	domain := cfg.Panel.Domain
	if domain == "" {
		domain = "127.0.0.1"
	}
	certPath, keyPath, err := generateSelfSignedPanelCertificate(cfg.Panel.DataDir, domain)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse(i18n.TE(c.Request, "errors.settings.gen_self_signed_failed", i18n.P{"error": err.Error()})))
		return
	}
	cfg.Panel.TLSMode = "self_signed"
	cfg.Panel.TLSCertPath = certPath
	cfg.Panel.TLSKeyPath = keyPath
	if err := config.SaveConfig(cfg); err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse(i18n.TE(c.Request, "errors.settings.save_config_failed")))
		return
	}
	executor.RestartPanelService()
	c.JSON(http.StatusOK, models.SuccessResponse(map[string]string{
		"message":       i18n.TE(c.Request, "errors.settings.self_signed_generated"),
		"tls_cert_path": certPath,
		"tls_key_path":  keyPath,
	}))
}

func (h *SettingsHandler) UploadTLSCertificate(c *gin.Context) {
	var req struct {
		CertPEM string `json:"cert_pem"`
		KeyPEM  string `json:"key_pem"`
		Domain  string `json:"domain"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse(i18n.TE(c.Request, "errors.invalid_request")))
		return
	}
	cfg := config.AppConfig
	if cfg == nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse(i18n.TE(c.Request, "errors.settings.config_not_loaded")))
		return
	}
	certPEM := strings.TrimSpace(req.CertPEM)
	keyPEM := strings.TrimSpace(req.KeyPEM)
	if certPEM == "" || keyPEM == "" {
		c.JSON(http.StatusBadRequest, models.ErrorResponse(i18n.TE(c.Request, "settings.cert_key_required")))
		return
	}
	if err := validateCertificatePair(c, certPEM, keyPEM, strings.TrimSpace(req.Domain)); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse(i18n.TE(c.Request, "errors.settings.cert_verify_failed", i18n.P{"error": err.Error()})))
		return
	}
	certPath, keyPath, err := writeUploadedPanelCertificate(cfg.Panel.DataDir, certPEM, keyPEM)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse(i18n.TE(c.Request, "errors.settings.write_cert_failed", i18n.P{"error": err.Error()})))
		return
	}
	if req.Domain != "" {
		cfg.Panel.Domain = strings.TrimSpace(req.Domain)
	}
	cfg.Panel.TLSMode = "uploaded"
	cfg.Panel.TLSCertPath = certPath
	cfg.Panel.TLSKeyPath = keyPath
	if err := config.SaveConfig(cfg); err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse(i18n.TE(c.Request, "errors.settings.save_config_failed")))
		return
	}
	executor.RestartPanelService()
	c.JSON(http.StatusOK, models.SuccessResponse(map[string]string{
		"message":       i18n.TE(c.Request, "errors.settings.cert_uploaded_verified"),
		"tls_cert_path": certPath,
		"tls_key_path":  keyPath,
	}))
}

func allowedTLSMode(mode string) bool {
	return mode == "self_signed" || mode == "uploaded" || mode == "acme"
}

func validPanelDomain(domain string) bool {
	if net.ParseIP(domain) != nil {
		return true
	}
	if len(domain) > 253 {
		return false
	}
	re := regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?(\.[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?)*$`)
	return re.MatchString(domain)
}

func panelCertDir(dataDir string) string {
	if dataDir == "" {
		dataDir = "/www/server/server-panel"
	}
	return filepath.Join(dataDir, "certs")
}

func generateSelfSignedPanelCertificate(dataDir, domain string) (string, string, error) {
	certDir := panelCertDir(dataDir)
	if err := os.MkdirAll(certDir, 0700); err != nil {
		return "", "", err
	}
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return "", "", err
	}
	serialLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serial, err := rand.Int(rand.Reader, serialLimit)
	if err != nil {
		return "", "", err
	}
	tmpl := x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: domain},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().AddDate(10, 0, 0),
		KeyUsage:     x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	if ip := net.ParseIP(domain); ip != nil {
		tmpl.IPAddresses = []net.IP{ip}
	} else {
		tmpl.DNSNames = []string{domain}
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &privateKey.PublicKey, privateKey)
	if err != nil {
		return "", "", err
	}
	certPath := filepath.Join(certDir, "panel.crt")
	keyPath := filepath.Join(certDir, "panel.key")
	certOut := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyOut := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(privateKey)})
	if err := os.WriteFile(certPath, certOut, 0644); err != nil {
		return "", "", err
	}
	if err := os.WriteFile(keyPath, keyOut, 0600); err != nil {
		return "", "", err
	}
	return certPath, keyPath, nil
}

func writeUploadedPanelCertificate(dataDir, certPEM, keyPEM string) (string, string, error) {
	certDir := panelCertDir(dataDir)
	if err := os.MkdirAll(certDir, 0700); err != nil {
		return "", "", err
	}
	certPath := filepath.Join(certDir, "uploaded-panel.crt")
	keyPath := filepath.Join(certDir, "uploaded-panel.key")
	if err := os.WriteFile(certPath, []byte(certPEM+"\n"), 0644); err != nil {
		return "", "", err
	}
	if err := os.WriteFile(keyPath, []byte(keyPEM+"\n"), 0600); err != nil {
		return "", "", err
	}
	return certPath, keyPath, nil
}

func validateCertificatePair(c *gin.Context, certPEM, keyPEM, domain string) error {
	pair, err := tls.X509KeyPair([]byte(certPEM), []byte(keyPEM))
	if err != nil {
		return err
	}
	if len(pair.Certificate) == 0 {
		return fmt.Errorf("%s", i18n.TE(c.Request, "errors.settings.cert_content_empty"))
	}
	cert, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return err
	}
	now := time.Now()
	if now.Before(cert.NotBefore) || now.After(cert.NotAfter) {
		return fmt.Errorf("%s", i18n.TE(c.Request, "errors.settings.cert_expired"))
	}
	if domain != "" {
		if ip := net.ParseIP(domain); ip != nil {
			if err := cert.VerifyHostname(ip.String()); err != nil {
				return fmt.Errorf("%s", i18n.TE(c.Request, "errors.settings.cert_ip_mismatch"))
			}
		} else if err := cert.VerifyHostname(domain); err != nil {
			return fmt.Errorf("%s", i18n.TE(c.Request, "errors.settings.cert_domain_mismatch"))
		}
	}
	return nil
}

// 监控参数
func (h *SettingsHandler) GetMonitoring(c *gin.Context) {
	m := make(map[string]string)
	keys := []string{"http_probe_interval_minutes", "http_probe_timeout_seconds", "metric_retention_days", "ban_duration_hours"}
	for _, k := range keys {
		var v string
		h.db().QueryRow("SELECT svalue FROM settings WHERE skey = ?", k).Scan(&v)
		m[k] = v
	}
	c.JSON(http.StatusOK, models.SuccessResponse(m))
}

func (h *SettingsHandler) UpdateMonitoring(c *gin.Context) {
	var req map[string]string
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse(i18n.TE(c.Request, "errors.invalid_request")))
		return
	}
	allowed := map[string]bool{"http_probe_interval_minutes": true, "http_probe_timeout_seconds": true, "metric_retention_days": true, "ban_duration_hours": true}
	for k, v := range req {
		if !allowed[k] {
			c.JSON(http.StatusBadRequest, models.ErrorResponse(i18n.TE(c.Request, "errors.settings.config_key_not_allowed", i18n.P{"key": k})))
			return
		}
		h.db().Exec("INSERT OR REPLACE INTO settings (skey, svalue) VALUES (?, ?)", k, v)
	}
	c.JSON(http.StatusOK, models.SuccessResponse(map[string]string{"message": i18n.TE(c.Request, "errors.settings.monitoring_saved")}))
}

func (h *SettingsHandler) GetCronStatus(c *gin.Context) {
	var lastCheck string
	h.db().QueryRow("SELECT MAX(created_at) FROM alert_log").Scan(&lastCheck)
	var lastProbe string
	h.db().QueryRow("SELECT MAX(http_probe_last_at) FROM servers WHERE http_probe_enabled = 1").Scan(&lastProbe)

	c.JSON(http.StatusOK, models.SuccessResponse(map[string]interface{}{
		"last_alert_check": lastCheck,
		"last_http_probe":  lastProbe,
	}))
}
