package router

import (
	"database/sql"
	"html/template"
	"io/fs"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/naibabiji/server-panel/config"
	"github.com/naibabiji/server-panel/handlers"
	"github.com/naibabiji/server-panel/middleware"
)

func SetupRouter(cfg *config.Config, db *sql.DB, staticFS fs.FS, templatesFS fs.FS) *gin.Engine {
	r := gin.New()
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
	basicAuthChecker := &middleware.BasicAuthChecker{
		RecordAttempt: loginTracker.RecordAttempt,
		IsBanned:      loginTracker.IsBanned,
	}

	r.GET("/", func(c *gin.Context) { c.Status(http.StatusNotFound) })
	r.GET("/favicon.ico", func(c *gin.Context) { c.Status(http.StatusNoContent) })

	suffix := cfg.Panel.RandomSuffix
	prefix := "/" + suffix

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
			c.HTML(http.StatusOK, "login.html", gin.H{
				"PanelTitle":   cfg.Panel.PanelTitle,
				"RandomSuffix": suffix,
				"AssetPrefix":  prefix + "/assets",
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
			protected.PUT("/api/providers/:id", provH.Update)
			protected.DELETE("/api/providers/:id", provH.Delete)

			settingsH := &handlers.SettingsHandler{DB: db}
			protected.GET("/api/settings/os-list", settingsH.GetOSList)
			protected.GET("/api/settings/site-type-list", settingsH.GetSiteTypeList)
			protected.GET("/api/settings", settingsH.GetPanelTitle)
			protected.PUT("/api/settings", settingsH.UpdatePanelTitle)
			protected.GET("/api/settings/panel-access", settingsH.GetPanelAccess)
			protected.PUT("/api/settings/panel-access", settingsH.UpdatePanelAccess)
			protected.GET("/api/settings/smtp", settingsH.GetSMTPConfig)
			protected.PUT("/api/settings/smtp", settingsH.UpdateSMTPConfig)
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
		}
	}

	staticSubFS, _ := fs.Sub(staticFS, "static")
	r.StaticFS(prefix+"/assets", http.FS(staticSubFS))

	tmpl := template.Must(template.New("").ParseFS(templatesFS, "templates/*.html"))
	r.SetHTMLTemplate(tmpl)

	return r
}

func requireViewPasswordSetup(db *sql.DB, prefix string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if isViewPasswordSetup(db) {
			c.Next()
			return
		}

		path := c.Request.URL.Path
		relativePath := strings.TrimPrefix(path, prefix)
		if relativePath == "/settings" ||
			relativePath == "/api/view-password/status" ||
			relativePath == "/api/view-password/setup" ||
			relativePath == "/api/auth/logout" ||
			relativePath == "/api/auth/check" ||
			relativePath == "/api/auth/csrf-token" ||
			(c.Request.Method == http.MethodGet && strings.HasPrefix(relativePath, "/api/settings")) {
			c.Next()
			return
		}

		if strings.HasPrefix(relativePath, "/api/") {
			c.AbortWithStatusJSON(http.StatusPreconditionRequired, gin.H{
				"success": false,
				"message": "请先设置查看密码",
			})
			return
		}

		c.Redirect(http.StatusFound, prefix+"/settings?view_password_required=1#security")
		c.Abort()
	}
}

func isViewPasswordSetup(db *sql.DB) bool {
	var hash string
	_ = db.QueryRow("SELECT svalue FROM settings WHERE skey = 'view_password_hash'").Scan(&hash)
	return hash != ""
}

func pageData(cfg *config.Config, active string, contentTpl string, c *gin.Context) gin.H {
	title := cfg.Panel.PanelTitle
	titles := map[string]string{
		"dashboard":       "仪表盘",
		"server_list":     "服务器管理",
		"server_detail":   "服务器详情",
		"server_form":     "编辑服务器",
		"customer_list":   "客户管理",
		"customer_detail": "客户详情",
		"customer_form":   "编辑客户",
		"website_list":    "网站管理",
		"website_detail":  "网站详情",
		"website_form":    "编辑网站",
		"provider_list":   "服务商管理",
		"provider_detail": "服务商详情",
		"provider_form":   "编辑服务商",
		"monitor":         "性能监控",
		"firewall":        "安全防御",
		"alert_rules":     "告警规则",
		"alert_log":       "告警日志",
		"settings":        "系统设置",
	}
	if t, ok := titles[active]; ok {
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
	}
}
