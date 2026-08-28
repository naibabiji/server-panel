package router

import (
	"database/sql"
	"errors"
	"html/template"
	"io/fs"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-contrib/gzip"
	"github.com/gin-gonic/gin"
	"github.com/naibabiji/server-panel/config"
	"github.com/naibabiji/server-panel/handlers"
	"github.com/naibabiji/server-panel/i18n"
	"github.com/naibabiji/server-panel/middleware"
)

// i18nKeys are the message keys exposed to client-side JS as
// window.SERVER_PANEL_I18N.messages, for strings that Alpine components
// generate at runtime (as opposed to static template text, which uses the
// {{t .Lang "key"}} template func directly).
var i18nKeys = []string{
	"common.cancel", "common.confirm", "common.save", "common.close", "common.loading",
	"common.none", "common.please_input", "common.show", "common.hide",
	"common.operation_failed", "common.service_busy", "common.non_json_response",
	"common.session_expired_redirect", "common.view_password_required",
	"common.password_verify_title",
	"search.placeholder", "search.searching", "search.no_results",
	"search.servers_group", "search.websites_group",
	"auth.login", "auth.username", "auth.username_placeholder",
	"auth.password", "auth.password_placeholder", "auth.login_button",
	"auth.logging_in", "auth.login_failed", "auth.missing_credentials",
	"auth.invalid_credentials",
	"dashboard.panel_update_available", "dashboard.sys_updates_available",
	"dashboard.expire_date", "dashboard.auto_renewal_on", "dashboard.expired_days",
	"dashboard.expires_today", "dashboard.days_left", "dashboard.probe_fail_generic",
	"dashboard.probe_timeout", "dashboard.probe_conn_refused", "dashboard.probe_dns_fail",
	"dashboard.probe_tls_error", "dashboard.cores", "dashboard.uptime", "dashboard.no_data",
	"common.detail", "common.edit", "common.delete", "common.renew", "common.add",
	"common.delete_success", "common.renew_success", "common.pagination_summary",
	"common.first_page", "common.prev_page", "common.next_page", "common.last_page",
	"common.status_active", "common.status_expired", "common.status_all",
	"common.new_expiry_date",
	"server.search_placeholder", "server.provider_all", "server.customer_all",
	"server.add", "server.empty", "server.renew_title", "server.delete_confirm",
	"common.yes", "common.no", "common.save_success",
	"server_form.title_edit", "server_form.title_add",
	"server_form.prompt_customer_name", "server_form.customer_created",
	"server_form.prompt_provider_name", "server_form.provider_created",
	"server_form.name_required", "server_form.fallback_customer_name",
	"server_form.fallback_provider_name",
	"common.copy", "common.copied_default",
	"common.status_online", "common.status_probe_error", "common.status_offline", "common.status_unknown",
	"server_detail.account_label", "server_detail.agent_installed", "server_detail.agent_not_installed",
	"server_detail.agent_uninstalled_cleared", "server_detail.command_copied",
	"server_detail.copy_failed_command", "server_detail.copy_failed_content", "server_detail.copy_failed_password",
	"server_detail.cpu_value", "server_detail.disk_value", "server_detail.expires_in_days",
	"server_detail.http_probe_bad", "server_detail.http_probe_ok", "server_detail.http_probe_pending",
	"server_detail.install_agent", "server_detail.install_cmd_generated", "server_detail.install_cmd_note",
	"server_detail.install_cmd_title", "server_detail.ip_copied", "server_detail.monitor_not_installed",
	"server_detail.not_configured", "server_detail.panel_account_copied", "server_detail.panel_link_copied",
	"server_detail.password_copied", "server_detail.proxy_url_invalid", "server_detail.ram_gb_value",
	"server_detail.ram_mb_value", "server_detail.secret_default_label", "server_detail.secret_not_saved",
	"server_detail.ssh_copied", "server_detail.uninstall_agent", "server_detail.uninstall_cmd_generated",
	"server_detail.uninstall_cmd_note", "server_detail.uninstall_cmd_title", "server_detail.view_failed",
	"server_detail.view_password", "server_detail.website_status_expired", "server_detail.website_status_running",
	"server_detail.websites_load_failed",
	"server_form.cycle_2year", "server_form.cycle_3year", "server_form.cycle_monthly",
	"server_form.cycle_quarterly", "server_form.cycle_yearly", "server_form.probe_off",
	"server_form.type_dedicated", "server_form.type_other", "server_form.type_shared",
	"website.delete_confirm",
	"website_form.domain_required", "website_form.fallback_server_name",
	"website_form.panel_password_not_saved", "website_form.server_required",
	"website_form.title_add", "website_form.title_edit",
	"customer.delete_confirm", "customer.load_servers_failed", "customer.load_websites_failed",
	"customer.name_required", "customer.title_add", "customer.title_edit",
	"provider.clear_confirm", "provider.delete_confirm", "provider.name_required",
	"provider.not_saved_private_notes", "provider.private_notes_cleared",
	"provider.private_notes_placeholder_empty", "provider.private_notes_placeholder_saved",
	"provider.title_add", "provider.title_edit",
	"alert_log.resolved", "alert_log.unresolved",
	"alert_rules.count_min_error", "alert_rules.disabled", "alert_rules.enabled",
	"alert_rules.modal_title_add", "alert_rules.modal_title_edit", "alert_rules.name_required",
	"alert_rules.threshold_advance_days", "alert_rules.threshold_advance_days_label",
	"alert_rules.threshold_days_help", "alert_rules.threshold_days_placeholder",
	"alert_rules.threshold_disk_help", "alert_rules.threshold_generic_label",
	"alert_rules.threshold_offline_help", "alert_rules.threshold_offline_label",
	"alert_rules.threshold_offline_minutes", "alert_rules.threshold_offline_placeholder",
	"alert_rules.threshold_percent", "alert_rules.threshold_percent_help",
	"alert_rules.threshold_percent_streak", "alert_rules.threshold_probe_fail",
	"alert_rules.threshold_required", "alert_rules.threshold_usage_label",
	"alert_rules.threshold_usage_placeholder", "alert_rules.type_cpu_high",
	"alert_rules.type_disk_high", "alert_rules.type_http_probe_down", "alert_rules.type_memory_high",
	"alert_rules.type_server_expiry", "alert_rules.type_server_offline", "alert_rules.type_website_expiry",
	"alert_rules.update_success",
	"common.saving",
	"files.add_dir_button", "files.adding",
	"files.busy_create_dir", "files.done_create_dir", "files.busy_rename", "files.done_rename",
	"files.busy_delete", "files.done_delete", "files.busy_move", "files.done_move",
	"files.busy_copy", "files.done_copy", "files.busy_compress", "files.done_compress",
	"files.busy_extract", "files.done_extract",
	"files.clipboard_copy_msg", "files.clipboard_copy_prefix", "files.clipboard_cut_msg",
	"files.clipboard_move_prefix", "files.delete_confirm", "files.entry_type_dir",
	"files.new_dir_prompt", "files.remove_root_confirm", "files.rename_prompt", "files.root_added",
	"files.source_custom", "files.source_mounted", "files.source_panel", "files.upload_done",
	"files.uploading",
	"firewall.add_success", "firewall.delete_confirm", "firewall.unban_confirm", "firewall.unbanned",
	"local_storage.can_login", "local_storage.cannot_login", "local_storage.desc_belongs_to",
	"local_storage.desc_partitions_below", "local_storage.desc_rom", "local_storage.desc_unpartitioned",
	"local_storage.dir_exists", "local_storage.dir_not_exists", "local_storage.enter_label",
	"local_storage.filesystem_source", "local_storage.format_partition", "local_storage.fs_partitioned",
	"local_storage.fs_raw_disk", "local_storage.fs_rom", "local_storage.fs_unformatted",
	"local_storage.groups_label", "local_storage.initialize_disk", "local_storage.kind_device",
	"local_storage.kind_disk", "local_storage.kind_part", "local_storage.kind_rom",
	"local_storage.mount_and_auto", "local_storage.mounting_label", "local_storage.mp_no_need",
	"local_storage.mp_not_mounted", "local_storage.mp_use_partition_below", "local_storage.permission_mode",
	"local_storage.read_label", "local_storage.status_can_mount", "local_storage.status_has_partitions",
	"local_storage.status_mounted", "local_storage.status_not_mounted", "local_storage.status_raw_disk",
	"local_storage.status_rom", "local_storage.status_system_protected", "local_storage.sudo_suffix",
	"local_storage.uid_status", "local_storage.unmount_confirm", "local_storage.used_available",
	"local_storage.write_label",
	"monitor.not_set", "monitor.updated_at",
	"common.enter_view_password", "common.view_password_required_notice",
	"settings.account_updated_relogin", "settings.acme_confirm", "settings.already_latest",
	"settings.auto_update_saved", "settings.backup_confirm", "settings.backup_count",
	"settings.backup_generated", "settings.backup_settings_saved", "settings.basic_auth_off_confirm",
	"settings.cert_key_required", "settings.cert_uploaded", "settings.change_vp_title",
	"settings.confirm_new_vp_label", "settings.confirm_new_vp_title", "settings.confirm_phrase_mismatch",
	"settings.confirm_vp_label", "settings.confirm_vp_title", "settings.continue_button",
	"settings.current_vp_label", "settings.email_not_sent", "settings.email_sent",
	"settings.fill_domain_first", "settings.generating_backup", "settings.loading_suffix",
	"settings.new_vp_label", "settings.new_vp_mismatch", "settings.not_set", "settings.nothing_to_change",
	"settings.oplog_count", "settings.os_list_saved", "settings.packages_updatable",
	"settings.password_mismatch", "settings.reset_new_vp_title", "settings.reset_vp_confirm",
	"settings.reset_vp_confirm_label", "settings.reset_vp_confirm_message", "settings.reset_vp_title",
	"settings.restart_timeout", "settings.restarting_wait_refresh", "settings.restore_confirm",
	"settings.restore_scheduled", "settings.restoring_wait", "settings.select_backup_file",
	"settings.self_signed_confirm", "settings.self_signed_issued", "settings.set_new_vp_title",
	"settings.setup_vp_title", "settings.site_type_list_saved", "settings.sys_update_confirm",
	"settings.sys_update_done", "settings.system_up_to_date", "settings.tab_backup", "settings.tab_basic",
	"settings.tab_dictionary", "settings.tab_notification", "settings.tab_security", "settings.tab_update",
	"settings.test_email_prompt", "settings.test_email_sent", "settings.tls_saved", "settings.unknown",
	"settings.update_confirm", "settings.update_started", "settings.upload_restore_confirm",
	"settings.vp_changed", "settings.vp_label", "settings.vp_mismatch", "settings.vp_not_set",
	"settings.vp_reset_success", "settings.vp_set", "settings.vp_set_success", "settings.vp_strength_hint",
	"settings.vp_verify_success", "settings.web_password_min_length",
}

var viewPasswordSetupCache = struct {
	sync.Mutex
	ok        bool
	expiresAt time.Time
}{}

func SetupRouter(cfg *config.Config, db *sql.DB, staticFS fs.FS, templatesFS fs.FS) *gin.Engine {
	r := gin.New()
	// Verified against gin v1.10.0 source: an empty/nil list here makes
	// isTrustedProxy() always false, so ClientIP() never reads
	// X-Forwarded-For/X-Real-IP and falls back to the raw TCP peer address -
	// it does NOT mean "trust everyone". Only entries actually listed here
	// (config.Panel.TrustedProxies, default loopback - see config.go) get
	// their forwarded-for headers honored.
	if err := r.SetTrustedProxies(cfg.Panel.TrustedProxies); err != nil {
		panic(err)
	}

	r.Use(middleware.CustomRecovery())
	r.Use(middleware.SecurityHeaders())
	r.Use(middleware.ScanDefense(cfg.Panel.RandomSuffix, cfg.Security.BanDurationHours))

	loginTracker := middleware.NewLoginAttemptTracker(
		db,
		cfg.Security.MaxLoginAttempts,
		cfg.Security.AttemptWindowMinutes,
		cfg.Security.BanDurationHours,
	)
	// 定期清理超出时间窗口的历史登录失败记录，避免 login_attempts 无限增长。
	basicAuthChecker := &middleware.BasicAuthChecker{
		RecordAttempt: loginTracker.RecordAttempt,
		IsBanned:      loginTracker.IsBanned,
	}
	// 用配置中的开关初始化 BasicAuth 启用状态（config 对旧配置已默认开启）。
	middleware.SetBasicAuthEnabled(cfg.Security.BasicAuthEnabled)
	go func() {
		// AttemptWindowMinutes 配置异常（<=0）时给一个下限，避免 time.NewTicker(0) panic。
		interval := time.Duration(cfg.Security.AttemptWindowMinutes) * time.Minute
		if interval <= 0 {
			interval = 5 * time.Minute
		}
		defer func() {
			if r := recover(); r != nil {
				log.Printf("login attempt cleanup goroutine recovered: %v", r)
			}
		}()
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for range ticker.C {
			loginTracker.CleanupOldAttempts()
		}
	}()

	r.GET("/", func(c *gin.Context) { c.Status(http.StatusNotFound) })
	r.GET("/favicon.ico", func(c *gin.Context) { c.Status(http.StatusNoContent) })

	suffix := cfg.Panel.RandomSuffix
	prefix := "/" + suffix

	// gzip 压缩 HTML/JSON/CSS/JS。排除：
	//  - 备份下载（.tar.gz 已压缩，且 c.FileAttachment 需支持 Range 断点续传，
	//    gzip 对分片单独压缩会破坏 Content-Range 语义，灾备恢复时才发现损坏）
	//  - /healthz（健康检查无 body，无需压缩）
	r.Use(gzip.Gzip(gzip.DefaultCompression, gzip.WithExcludedPaths([]string{
		prefix + "/api/settings/backup/download",
		"/healthz",
	})))

	// Public status page (no auth)
	statusH := &handlers.StatusPageHandler{DB: db}
	r.GET("/status/:token", func(c *gin.Context) {
		c.HTML(http.StatusOK, "status_public.html", nil)
	})
	r.GET("/api/status/:token/info", statusH.GetInfo)
	r.GET("/api/status/:token/metrics", statusH.GetMetrics)
	r.GET("/api/status/:token/websites", statusH.GetWebsites)
	r.POST("/api/status/:token/verify", statusH.VerifyPassword)

	// Agent routes
	ag := r.Group("")
	ag.Use(middleware.MaxBodyBytes(64 * 1024))
	ag.Use(middleware.AgentIPRateLimit())
	ag.Use(middleware.AgentAuth(db))
	{
		ah := &handlers.AgentDataHandler{DB: db}
		ag.POST("/agent/ping", ah.Ping)
		ag.POST("/agent/uninstall", ah.Uninstall)
		ag.POST("/agent/metrics", ah.ReceiveMetrics)
	}

	// Panel group
	pg := r.Group(prefix)
	pg.Use(middleware.BasicAuth(basicAuthChecker))
	{
		authH := &handlers.AuthHandler{AttemptTracker: loginTracker}
		vpH := &handlers.ViewPasswordHandler{}

		pg.GET("/login", func(c *gin.Context) {
			i18n.MaybeSetLanguageCookie(c.Writer, c.Request)
			lang := i18n.LangFromRequest(c.Request)
			c.HTML(http.StatusOK, "login.html", gin.H{
				"PanelTitle":   cfg.Panel.PanelTitle,
				"PanelVersion": cfg.Panel.Version,
				"RandomSuffix": suffix,
				"AssetPrefix":  prefix + "/assets",
				"Lang":         lang,
				"MessagesJSON": i18n.MessagesJSON(lang, i18nKeys),
			})
		})
		pg.POST("/api/auth/login", authH.Login)

		protected := pg.Group("")
		protected.Use(middleware.SessionRequired())
		protected.Use(middleware.SetCSRFToken)
		protected.Use(middleware.CSRF())
		protected.Use(requireViewPasswordSetup(db, prefix))
		{
			protected.POST("/api/auth/logout", authH.Logout)
			protected.GET("/api/auth/check", authH.Check)
			protected.GET("/api/auth/csrf-token", authH.CSRFToken)

			protected.GET("/api/view-password/status", vpH.GetStatus)
			protected.POST("/api/view-password/setup", vpH.Setup)
			protected.POST("/api/view-password/change", vpH.Change)
			protected.POST("/api/view-password/unlock", vpH.Unlock)
			protected.POST("/api/view-password/lock", vpH.Lock)

			dashH := &handlers.DashboardHandler{}
			protected.GET("/api/dashboard/stats", dashH.GetStats)
			protected.GET("/api/dashboard/expiring", dashH.GetExpiring)
			protected.GET("/api/dashboard/http-probe-issues", dashH.GetHTTPProbeIssues)
			protected.GET("/api/dashboard/recent-alerts", dashH.GetRecentAlerts)
			protected.GET("/api/dashboard/host-metrics/latest", dashH.GetHostMetricsLatest)
			protected.GET("/api/dashboard/host-metrics", dashH.GetHostMetrics)

			srvH := &handlers.ServerHandler{DB: db}
			protected.GET("/api/servers", srvH.List)
			protected.POST("/api/servers", srvH.Create)
			protected.GET("/api/servers/stats", srvH.GetStats)
			protected.GET("/api/servers/:id", srvH.Get)
			protected.GET("/api/servers/:id/secrets/:field", srvH.GetSecret)
			protected.PUT("/api/servers/:id", srvH.Update)
			protected.DELETE("/api/servers/:id", srvH.Delete)
			protected.POST("/api/servers/:id/agent-key/regenerate", srvH.RegenerateAgentKey)
			protected.POST("/api/servers/:id/agent/uninstall", srvH.PrepareAgentUninstall)

			customerH := &handlers.CustomerHandler{DB: db}
			protected.GET("/api/customers", customerH.List)
			protected.POST("/api/customers", customerH.Create)
			protected.GET("/api/customers/:id", customerH.Get)
			protected.PUT("/api/customers/:id", customerH.Update)
			protected.DELETE("/api/customers/:id", customerH.Delete)

			webH := &handlers.WebsiteHandler{DB: db}
			protected.GET("/api/websites", webH.List)
			protected.POST("/api/websites", webH.Create)
			protected.GET("/api/websites/:id", webH.Get)
			protected.GET("/api/websites/:id/secrets/panel-password", webH.GetPanelPassword)
			protected.PUT("/api/websites/:id", webH.Update)
			protected.DELETE("/api/websites/:id", webH.Delete)

			provH := &handlers.ProviderHandler{DB: db}
			protected.GET("/api/providers", provH.List)
			protected.POST("/api/providers", provH.Create)
			protected.GET("/api/providers/:id", provH.Get)
			protected.GET("/api/providers/:id/secrets/private-notes", provH.GetPrivateNotes)
			protected.DELETE("/api/providers/:id/secrets/private-notes", provH.ClearPrivateNotes)
			protected.PUT("/api/providers/:id", provH.Update)
			protected.DELETE("/api/providers/:id", provH.Delete)

			settingsH := &handlers.SettingsHandler{DB: db, AfterRestoreScheduled: clearViewPasswordSetupCache}
			protected.GET("/api/settings/os-list", settingsH.GetOSList)
			protected.GET("/api/settings/site-type-list", settingsH.GetSiteTypeList)
			protected.GET("/api/settings", settingsH.GetPanelTitle)
			protected.PUT("/api/settings", settingsH.UpdatePanelTitle)
			protected.GET("/api/settings/panel-access", settingsH.GetPanelAccess)
			protected.PUT("/api/settings/panel-access", settingsH.UpdatePanelAccess)
			protected.GET("/api/settings/smtp", settingsH.GetSMTPConfig)
			protected.PUT("/api/settings/smtp", settingsH.UpdateSMTPConfig)
			protected.GET("/api/settings/backup", settingsH.GetBackupSettings)
			protected.PUT("/api/settings/backup", settingsH.UpdateBackupSettings)
			protected.POST("/api/settings/backup/run", settingsH.RunDatabaseBackup)
			protected.GET("/api/settings/backup/list", settingsH.ListBackups)
			protected.GET("/api/settings/backup/download", settingsH.DownloadBackup)
			protected.POST("/api/settings/backup/restore", settingsH.RestoreBackup)
			protected.POST("/api/settings/backup/restore-upload", middleware.MaxBodyBytes(1<<30), settingsH.RestoreBackupUpload)
			protected.GET("/api/settings/account", settingsH.GetAccount)
			protected.PUT("/api/settings/account", settingsH.UpdateAccount)
			protected.GET("/api/settings/basic-auth", settingsH.GetBasicAuthConfig)
			protected.PUT("/api/settings/basic-auth", settingsH.UpdateBasicAuthConfig)
			protected.GET("/api/settings/web-account", settingsH.GetWebAccount)
			protected.PUT("/api/settings/web-account", settingsH.UpdateWebAccount)
			protected.POST("/api/settings/change-password", settingsH.ChangePassword)
			protected.PUT("/api/settings/os-list", settingsH.UpdateOSList)
			protected.PUT("/api/settings/site-type-list", settingsH.UpdateSiteTypeList)
			protected.GET("/api/settings/cron-status", settingsH.GetCronStatus)
			protected.GET("/api/settings/tls", settingsH.GetTLSConfig)
			protected.PUT("/api/settings/tls", settingsH.UpdateTLSConfig)
			protected.POST("/api/settings/tls/issue", settingsH.IssueTLS)
			protected.POST("/api/settings/tls/upload", settingsH.UploadTLSCertificate)
			protected.GET("/api/settings/monitoring", settingsH.GetMonitoring)
			protected.PUT("/api/settings/monitoring", settingsH.UpdateMonitoring)

			updateH := &handlers.UpdateHandler{DB: db}
			protected.GET("/api/update/check", updateH.CheckUpdate)
			protected.GET("/api/update/status", updateH.GetUpdateStatus)
			protected.POST("/api/update/do", updateH.DoUpdate)
			protected.GET("/api/update/auto-settings", updateH.GetAutoUpdateSettings)
			protected.PUT("/api/update/auto-settings", updateH.UpdateAutoUpdateSettings)
			protected.GET("/api/update/logs", updateH.GetOperationLogs)

			sysUpdateH := &handlers.SystemUpdateHandler{}
			protected.GET("/api/system/updates", sysUpdateH.Check)
			protected.POST("/api/system/updates/do", sysUpdateH.Update)

			alertH := &handlers.AlertHandler{DB: db}
			protected.GET("/api/alerts/rules", alertH.ListRules)
			protected.POST("/api/alerts/rules", alertH.CreateRule)
			protected.PUT("/api/alerts/rules/:id", alertH.UpdateRule)
			protected.DELETE("/api/alerts/rules/:id", alertH.DeleteRule)
			protected.GET("/api/alerts/log", alertH.GetLog)
			protected.POST("/api/alerts/test-smtp", alertH.TestSMTP)

			fwH := &handlers.FirewallHandler{DB: db}
			protected.GET("/api/firewall/bans", fwH.ListBans)
			protected.POST("/api/firewall/unban/:id", fwH.Unban)
			protected.GET("/api/firewall/whitelist", fwH.ListWhitelist)
			protected.POST("/api/firewall/whitelist", fwH.AddWhitelist)
			protected.DELETE("/api/firewall/whitelist/:id", fwH.DeleteWhitelist)

			metricsH := &handlers.MetricsHandler{DB: db}
			protected.GET("/api/monitor/overview", metricsH.GetOverview)
			protected.GET("/api/monitor/:id/latest", metricsH.GetLatest)
			protected.GET("/api/monitor/:id", metricsH.GetServerMetrics)

			storageH := &handlers.LocalStorageHandler{}
			protected.GET("/api/local-storage/devices", storageH.ListDevices)
			protected.POST("/api/local-storage/mount", storageH.Mount)
			protected.POST("/api/local-storage/unmount", storageH.Unmount)
			protected.POST("/api/local-storage/format", storageH.Format)
			protected.POST("/api/local-storage/initialize", storageH.Initialize)
			protected.GET("/api/local-storage/users", storageH.ListUsers)
			protected.POST("/api/local-storage/permissions/check", storageH.CheckPermission)

			filesH := &handlers.FileManagerHandler{DB: db}
			protected.GET("/api/files/roots", filesH.Roots)
			protected.POST("/api/files/roots", filesH.AddRoot)
			protected.DELETE("/api/files/roots", filesH.RemoveRoot)
			protected.GET("/api/files/list", filesH.List)
			protected.GET("/api/files/download", filesH.Download)
			protected.POST("/api/files/upload", middleware.MaxBodyBytes(1<<30), filesH.Upload)
			protected.POST("/api/files/mkdir", filesH.Mkdir)
			protected.PUT("/api/files/rename", filesH.Rename)
			protected.DELETE("/api/files/delete", filesH.Delete)
			protected.POST("/api/files/transfer", filesH.Transfer)
			protected.POST("/api/files/compress", filesH.Compress)
			protected.POST("/api/files/extract", filesH.Extract)

			protected.GET("/", func(c *gin.Context) {
				c.HTML(http.StatusOK, "dashboard.html", pageData(cfg, "dashboard", "dashboard_content", c))
			})
			protected.GET("/servers", func(c *gin.Context) {
				c.HTML(http.StatusOK, "server_list.html", pageData(cfg, "server_list", "server_list_content", c))
			})
			protected.GET("/servers/new", func(c *gin.Context) {
				c.HTML(http.StatusOK, "server_form.html", pageData(cfg, "server_form", "server_form_content", c))
			})
			protected.GET("/servers/:id", func(c *gin.Context) {
				c.HTML(http.StatusOK, "server_detail.html", pageData(cfg, "server_detail", "server_detail_content", c))
			})
			protected.GET("/servers/:id/edit", func(c *gin.Context) {
				c.HTML(http.StatusOK, "server_form.html", pageData(cfg, "server_form", "server_form_content", c))
			})
			protected.GET("/customers", func(c *gin.Context) {
				c.HTML(http.StatusOK, "customer_list.html", pageData(cfg, "customer_list", "customer_list_content", c))
			})
			protected.GET("/customers/new", func(c *gin.Context) {
				c.HTML(http.StatusOK, "customer_form.html", pageData(cfg, "customer_form", "customer_form_content", c))
			})
			protected.GET("/customers/:id", func(c *gin.Context) {
				c.HTML(http.StatusOK, "customer_detail.html", pageData(cfg, "customer_detail", "customer_detail_content", c))
			})
			protected.GET("/customers/:id/edit", func(c *gin.Context) {
				c.HTML(http.StatusOK, "customer_form.html", pageData(cfg, "customer_form", "customer_form_content", c))
			})
			protected.GET("/monitor", func(c *gin.Context) {
				c.HTML(http.StatusOK, "monitor.html", pageData(cfg, "monitor", "monitor_content", c))
			})
			protected.GET("/monitor/:id", func(c *gin.Context) {
				c.HTML(http.StatusOK, "monitor_detail.html", pageData(cfg, "monitor", "monitor_detail_content", c))
			})
			protected.GET("/websites", func(c *gin.Context) {
				c.HTML(http.StatusOK, "website_list.html", pageData(cfg, "website_list", "website_list_content", c))
			})
			protected.GET("/websites/new", func(c *gin.Context) {
				c.HTML(http.StatusOK, "website_form.html", pageData(cfg, "website_form", "website_form_content", c))
			})
			protected.GET("/websites/:id", func(c *gin.Context) {
				c.HTML(http.StatusOK, "website_detail.html", pageData(cfg, "website_detail", "website_detail_content", c))
			})
			protected.GET("/websites/:id/edit", func(c *gin.Context) {
				c.HTML(http.StatusOK, "website_form.html", pageData(cfg, "website_form", "website_form_content", c))
			})
			protected.GET("/providers", func(c *gin.Context) {
				c.HTML(http.StatusOK, "provider_list.html", pageData(cfg, "provider_list", "provider_list_content", c))
			})
			protected.GET("/providers/new", func(c *gin.Context) {
				c.HTML(http.StatusOK, "provider_form.html", pageData(cfg, "provider_form", "provider_form_content", c))
			})
			protected.GET("/providers/:id", func(c *gin.Context) {
				c.HTML(http.StatusOK, "provider_detail.html", pageData(cfg, "provider_detail", "provider_detail_content", c))
			})
			protected.GET("/providers/:id/edit", func(c *gin.Context) {
				c.HTML(http.StatusOK, "provider_form.html", pageData(cfg, "provider_form", "provider_form_content", c))
			})
			protected.GET("/alerts", func(c *gin.Context) {
				c.HTML(http.StatusOK, "alert_rules.html", pageData(cfg, "alert_rules", "alert_rules_content", c))
			})
			protected.GET("/alerts/log", func(c *gin.Context) {
				c.HTML(http.StatusOK, "alert_log.html", pageData(cfg, "alert_log", "alert_log_content", c))
			})
			protected.GET("/settings", func(c *gin.Context) {
				c.HTML(http.StatusOK, "settings.html", pageData(cfg, "settings", "settings_content", c))
			})
			protected.GET("/firewall", func(c *gin.Context) {
				c.HTML(http.StatusOK, "firewall.html", pageData(cfg, "firewall", "firewall_content", c))
			})
			protected.GET("/local-storage", func(c *gin.Context) {
				c.HTML(http.StatusOK, "local_storage.html", pageData(cfg, "local_storage", "local_storage_content", c))
			})
			protected.GET("/files", func(c *gin.Context) {
				c.HTML(http.StatusOK, "files.html", pageData(cfg, "files", "files_content", c))
			})
		}
	}

	staticSubFS, _ := fs.Sub(staticFS, "static")
	// 静态资源长缓存（1 年 + immutable）。资源 URL 带 ?v=PanelVersion 做 cache-busting：
	// 版本号一变（每次发版）浏览器即视为新文件重新下载，避免更新后旧 JS 跑新后端。
	// 此 r.Use 注册在 StaticFS 之前、其余路由之后，故仅 /assets 走到它。
	r.Use(func(c *gin.Context) {
		if strings.HasPrefix(c.Request.URL.Path, prefix+"/assets/") {
			c.Header("Cache-Control", "public, max-age=31536000, immutable")
		}
		c.Next()
	})
	r.StaticFS(prefix+"/assets", http.FS(staticSubFS))

	tmpl := template.Must(template.New("").Funcs(i18n.FuncMap()).ParseFS(templatesFS, "templates/*.html"))
	r.SetHTMLTemplate(tmpl)

	return r
}

func requireViewPasswordSetup(db *sql.DB, prefix string) gin.HandlerFunc {
	return func(c *gin.Context) {
		// 命中缓存且未过期时直接放行，不再每请求查库。缓存只在 setup==true
		// 时写入（写时即为真），且 setup 只会 false→true（无删除入口），因此
		// 缓存为真时不可能比 DB 更"新变假"；缓存未命中/过期才回源并刷新。
		if cachedViewPasswordSetup() {
			c.Next()
			return
		}
		setup, err := isViewPasswordSetup(db)
		if err != nil {
			log.Printf("read view password setup status failed: %v", err)
			if cachedViewPasswordSetup() {
				c.Next()
				return
			}
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
				"success": false,
				"message": i18n.TE(c.Request, "common.view_password_status_failed_refresh"),
			})
			return
		}
		if setup {
			cacheViewPasswordSetup()
			c.Next()
			return
		}
		// 主动失效：回源发现 setup==false 时清掉可能残留的旧缓存，
		// 避免恢复等场景下 DB 已变但缓存仍为真的窗口。
		clearViewPasswordSetupCache()

		path := c.Request.URL.Path
		relativePath := strings.TrimPrefix(path, prefix)
		if relativePath == "/settings" ||
			relativePath == "/api/view-password/status" ||
			relativePath == "/api/view-password/setup" ||
			relativePath == "/api/auth/logout" ||
			relativePath == "/api/auth/check" ||
			relativePath == "/api/auth/csrf-token" ||
			(c.Request.Method == http.MethodGet && relativePath == "/api/update/auto-settings") ||
			(c.Request.Method == http.MethodGet && strings.HasPrefix(relativePath, "/api/settings")) {
			c.Next()
			return
		}

		if strings.HasPrefix(relativePath, "/api/") {
			c.AbortWithStatusJSON(http.StatusPreconditionRequired, gin.H{
				"success": false,
				"message": i18n.TE(c.Request, "common.view_password_required"),
			})
			return
		}

		c.Redirect(http.StatusFound, prefix+"/settings?view_password_required=1#security")
		c.Abort()
	}
}

func isViewPasswordSetup(db *sql.DB) (bool, error) {
	if db == nil {
		return false, errors.New("database is not initialized")
	}
	var hash string
	var err error
	for i := 0; i < 3; i++ {
		err = db.QueryRow("SELECT svalue FROM settings WHERE skey = 'view_password_hash'").Scan(&hash)
		if err == nil {
			return hash != "", nil
		}
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		time.Sleep(time.Duration(100*(i+1)) * time.Millisecond)
	}
	return false, err
}

func cacheViewPasswordSetup() {
	viewPasswordSetupCache.Lock()
	defer viewPasswordSetupCache.Unlock()
	viewPasswordSetupCache.ok = true
	viewPasswordSetupCache.expiresAt = time.Now().Add(10 * time.Minute)
}

func clearViewPasswordSetupCache() {
	viewPasswordSetupCache.Lock()
	defer viewPasswordSetupCache.Unlock()
	viewPasswordSetupCache.ok = false
	viewPasswordSetupCache.expiresAt = time.Time{}
}

func cachedViewPasswordSetup() bool {
	viewPasswordSetupCache.Lock()
	defer viewPasswordSetupCache.Unlock()
	return viewPasswordSetupCache.ok && time.Now().Before(viewPasswordSetupCache.expiresAt)
}

func pageData(cfg *config.Config, active string, contentTpl string, c *gin.Context) gin.H {
	i18n.MaybeSetLanguageCookie(c.Writer, c.Request)
	lang := i18n.LangFromRequest(c.Request)
	title := cfg.Panel.PanelTitle
	if t := i18n.T(lang, "title."+active); t != "title."+active {
		title = t + " — " + title
	}
	csrfToken := middleware.GetCSRFToken(c)
	return gin.H{
		"Title":           title,
		"PanelTitle":      cfg.Panel.PanelTitle,
		"PanelVersion":    cfg.Panel.Version,
		"TLSMode":         cfg.Panel.TLSMode,
		"ContentTemplate": contentTpl,
		"RandomSuffix":    cfg.Panel.RandomSuffix,
		"Active":          active,
		"AssetPrefix":     "/" + cfg.Panel.RandomSuffix + "/assets",
		"CSRFToken":       csrfToken,
		"Lang":            lang,
		"MessagesJSON":    i18n.MessagesJSON(lang, i18nKeys),
	}
}
