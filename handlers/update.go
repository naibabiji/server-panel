package handlers

import (
	"database/sql"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/naibabiji/server-panel/config"
	"github.com/naibabiji/server-panel/database"
	"github.com/naibabiji/server-panel/executor"
	"github.com/naibabiji/server-panel/models"
)

type UpdateHandler struct {
	DB *sql.DB
}

var panelReleaseCache = struct {
	sync.Mutex
	release  *executor.GithubRelease
	expireAt time.Time
}{}

const panelReleaseCacheTTL = 5 * time.Minute

func (h *UpdateHandler) db() *sql.DB {
	if h.DB != nil {
		return h.DB
	}
	return database.GetDB()
}

func (h *UpdateHandler) currentVersion() string {
	if config.AppConfig != nil {
		return config.AppConfig.Panel.Version
	}
	return ""
}

// CheckUpdate queries GitHub for the latest release and compares it against
// the running version. Read-only, safe to poll.
func (h *UpdateHandler) CheckUpdate(c *gin.Context) {
	current := h.currentVersion()
	release, err := cachedLatestPanelRelease()
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse("检查更新失败: "+err.Error()))
		return
	}
	hasUpdate := executor.CompareVersions(release.TagName, current) > 0
	notes := release.Body
	if idx := strings.Index(notes, "**Full Changelog**"); idx >= 0 {
		notes = strings.TrimSpace(notes[:idx])
	}
	c.JSON(http.StatusOK, models.SuccessResponse(map[string]interface{}{
		"current_version": current,
		"latest_version":  release.TagName,
		"release_notes":   notes,
		"has_update":      hasUpdate,
	}))
}

func cachedLatestPanelRelease() (*executor.GithubRelease, error) {
	now := time.Now()
	panelReleaseCache.Lock()
	if panelReleaseCache.release != nil && now.Before(panelReleaseCache.expireAt) {
		release := panelReleaseCache.release
		panelReleaseCache.Unlock()
		return release, nil
	}
	panelReleaseCache.Unlock()

	release, err := executor.FetchLatestPanelRelease()
	if err != nil {
		return nil, err
	}

	panelReleaseCache.Lock()
	panelReleaseCache.release = release
	panelReleaseCache.expireAt = now.Add(panelReleaseCacheTTL)
	panelReleaseCache.Unlock()
	return release, nil
}

// GetUpdateStatus returns the in-memory progress of the current (or most
// recently finished) update run, polled by the settings page while an
// update is in progress.
func (h *UpdateHandler) GetUpdateStatus(c *gin.Context) {
	c.JSON(http.StatusOK, models.SuccessResponse(executor.SnapshotPanelUpdateStatus()))
}

// DoUpdate starts a manual update run. It returns immediately; progress and
// outcome are tracked via GetUpdateStatus and the operation log.
func (h *UpdateHandler) DoUpdate(c *gin.Context) {
	err := executor.ExecutePanelUpdate(executor.PanelUpdateOptions{
		CurrentVersion: h.currentVersion(),
		ConfigPath:     config.ConfigPath(),
		Config:         config.AppConfig,
		Trigger:        "manual",
		UseWatchdog:    true,
	})
	if err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse(err.Error()))
		return
	}
	c.JSON(http.StatusOK, models.SuccessResponse(map[string]string{
		"message": "更新已开始，请勿关闭本页面，面板即将重启",
	}))
}

var autoUpdateSettingKeys = []string{
	"panel_auto_update_enabled",
	"panel_auto_update_mode",
	"panel_auto_update_window",
	"panel_auto_update_release_delay_minutes",
	"panel_auto_update_signature_timeout_minutes",
	"panel_auto_update_last_check_at",
	"panel_auto_update_last_attempt_at",
	"panel_auto_update_last_status",
	"panel_auto_update_last_stage",
	"panel_auto_update_last_error",
	"panel_auto_update_last_success_at",
	"panel_auto_update_last_success_version",
	"panel_auto_update_last_target_version",
	"panel_auto_update_signature_wait_version",
	"panel_auto_update_signature_wait_at",
}

var autoUpdateEditableKeys = map[string]bool{
	"panel_auto_update_enabled":                   true,
	"panel_auto_update_mode":                      true,
	"panel_auto_update_window":                    true,
	"panel_auto_update_release_delay_minutes":     true,
	"panel_auto_update_signature_timeout_minutes": true,
}

func (h *UpdateHandler) GetAutoUpdateSettings(c *gin.Context) {
	result := make(map[string]string, len(autoUpdateSettingKeys))
	for _, k := range autoUpdateSettingKeys {
		var v string
		h.db().QueryRow("SELECT svalue FROM settings WHERE skey = ?", k).Scan(&v)
		result[k] = v
	}
	c.JSON(http.StatusOK, models.SuccessResponse(result))
}

func (h *UpdateHandler) UpdateAutoUpdateSettings(c *gin.Context) {
	var req map[string]string
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse("无效的请求数据"))
		return
	}
	if mode, ok := req["panel_auto_update_mode"]; ok && mode != "patch_only" && mode != "all_stable" {
		c.JSON(http.StatusBadRequest, models.ErrorResponse("自动更新模式仅支持 patch_only 或 all_stable"))
		return
	}
	if window, ok := req["panel_auto_update_window"]; ok && !isValidAutoUpdateWindow(window) {
		c.JSON(http.StatusBadRequest, models.ErrorResponse("自动更新时间窗口格式必须为 HH:MM-HH:MM"))
		return
	}
	for k, v := range req {
		if !autoUpdateEditableKeys[k] {
			c.JSON(http.StatusBadRequest, models.ErrorResponse("不允许的配置项: "+k))
			return
		}
		h.db().Exec("INSERT OR REPLACE INTO settings (skey, svalue) VALUES (?, ?)", k, v)
	}
	c.JSON(http.StatusOK, models.SuccessResponse(nil))
}

func isValidAutoUpdateWindow(window string) bool {
	parts := strings.SplitN(window, "-", 2)
	if len(parts) != 2 {
		return false
	}
	return isValidClock(parts[0]) && isValidClock(parts[1])
}

func isValidClock(value string) bool {
	parts := strings.SplitN(strings.TrimSpace(value), ":", 2)
	if len(parts) != 2 {
		return false
	}
	hour, errHour := strconv.Atoi(parts[0])
	minute, errMinute := strconv.Atoi(parts[1])
	return errHour == nil && errMinute == nil && hour >= 0 && hour <= 23 && minute >= 0 && minute <= 59
}

// GetOperationLogs returns a paginated view of the generic operation_logs
// audit table (panel updates today; other subsystems may write to it later).
func (h *UpdateHandler) GetOperationLogs(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}

	var total int
	h.db().QueryRow("SELECT COUNT(*) FROM operation_logs").Scan(&total)

	offset := (page - 1) * pageSize
	rows, err := h.db().Query(
		`SELECT id, operation, target, status, message, created_at
		 FROM operation_logs ORDER BY id DESC LIMIT ? OFFSET ?`,
		pageSize, offset)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.ErrorResponse("查询失败"))
		return
	}
	defer rows.Close()

	logs := []models.OperationLog{}
	for rows.Next() {
		var l models.OperationLog
		rows.Scan(&l.ID, &l.Operation, &l.Target, &l.Status, &l.Message, &l.CreatedAt)
		logs = append(logs, l)
	}
	c.JSON(http.StatusOK, models.SuccessResponse(models.PaginatedResult{
		Items: logs, Total: total, Page: page, PageSize: pageSize,
	}))
}
