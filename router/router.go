package router

import (
	"database/sql"
	"html/template"
	"io/fs"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/naibabiji/server-panel/config"
	"github.com/naibabiji/server-panel/handlers"
	"github.com/naibabiji/server-panel/middleware"
)

func SetupRouter(cfg *config.Config, db *sql.DB, staticFS fs.FS, templatesFS fs.FS) *gin.Engine {
	r := gin.New()

	r.Use(middleware.CustomRecovery())
	r.Use(middleware.SecurityHeaders())

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

	ag := r.Group("")
	ag.Use(middleware.AgentAuth(db))
	{
		ah := &handlers.AgentDataHandler{DB: db}
		ag.POST("/agent/ping", ah.Ping)
		ag.POST("/agent/metrics", ah.ReceiveMetrics)
	}

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
		{
			protected.POST("/api/auth/logout", authH.Logout)
			protected.GET("/api/auth/check", authH.Check)
			protected.GET("/api/auth/csrf-token", authH.CSRFToken)

			protected.GET("/api/view-password/status", vpH.GetStatus)
			protected.POST("/api/view-password/setup", vpH.Setup)
			protected.POST("/api/view-password/unlock", vpH.Unlock)
			protected.POST("/api/view-password/lock", vpH.Lock)

			dashH := &handlers.DashboardHandler{}
			protected.GET("/api/dashboard/stats", dashH.GetStats)
			protected.GET("/api/dashboard/expiring", dashH.GetExpiring)
			protected.GET("/api/dashboard/recent-alerts", dashH.GetRecentAlerts)

			srvH := &handlers.ServerHandler{DB: db}
			protected.GET("/api/servers", srvH.List)
			protected.POST("/api/servers", srvH.Create)
			protected.GET("/api/servers/stats", srvH.GetStats)
			protected.GET("/api/servers/:id", srvH.Get)
			protected.PUT("/api/servers/:id", srvH.Update)
			protected.DELETE("/api/servers/:id", srvH.Delete)

			userH := &handlers.UserHandler{DB: db}
			protected.GET("/api/users", userH.List)
			protected.POST("/api/users", userH.Create)
			protected.GET("/api/users/:id", userH.Get)
			protected.PUT("/api/users/:id", userH.Update)
			protected.DELETE("/api/users/:id", userH.Delete)

			webH := &handlers.WebsiteHandler{DB: db}
			protected.GET("/api/websites", webH.List)
			protected.POST("/api/websites", webH.Create)
			protected.GET("/api/websites/:id", webH.Get)
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
			protected.GET("/api/settings/smtp", settingsH.GetSMTPConfig)
			protected.PUT("/api/settings/smtp", settingsH.UpdateSMTPConfig)
			protected.POST("/api/settings/change-password", settingsH.ChangePassword)
			protected.PUT("/api/settings/os-list", settingsH.UpdateOSList)
			protected.PUT("/api/settings/site-type-list", settingsH.UpdateSiteTypeList)
			protected.GET("/api/settings/cron-status", settingsH.GetCronStatus)

			alertH := &handlers.AlertHandler{DB: db}
			protected.GET("/api/alerts/rules", alertH.ListRules)
			protected.POST("/api/alerts/rules", alertH.CreateRule)
			protected.PUT("/api/alerts/rules/:id", alertH.UpdateRule)
			protected.DELETE("/api/alerts/rules/:id", alertH.DeleteRule)
			protected.GET("/api/alerts/log", alertH.GetLog)
			protected.POST("/api/alerts/test-smtp", alertH.TestSMTP)

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
			protected.GET("/users", func(c *gin.Context) {
				c.HTML(http.StatusOK, "user_list.html", pageData(cfg, "user_list", "user_list_content", c))
			})
			protected.GET("/users/new", func(c *gin.Context) {
				c.HTML(http.StatusOK, "user_form.html", pageData(cfg, "user_form", "user_form_content", c))
			})
			protected.GET("/users/:id/edit", func(c *gin.Context) {
				c.HTML(http.StatusOK, "user_form.html", pageData(cfg, "user_form", "user_form_content", c))
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
			protected.GET("/websites/:id/edit", func(c *gin.Context) {
				c.HTML(http.StatusOK, "website_form.html", pageData(cfg, "website_form", "website_form_content", c))
			})
			protected.GET("/providers", func(c *gin.Context) {
				c.HTML(http.StatusOK, "provider_list.html", pageData(cfg, "provider_list", "provider_list_content", c))
			})
			protected.GET("/providers/new", func(c *gin.Context) {
				c.HTML(http.StatusOK, "provider_form.html", pageData(cfg, "provider_form", "provider_form_content", c))
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
		}
	}

	staticSubFS, _ := fs.Sub(staticFS, "static")
	r.StaticFS(prefix+"/assets", http.FS(staticSubFS))

	tmpl := template.Must(template.New("").ParseFS(templatesFS, "templates/*.html"))
	r.SetHTMLTemplate(tmpl)

	return r
}

func pageData(cfg *config.Config, active string, contentTpl string, c *gin.Context) gin.H {
	title := cfg.Panel.PanelTitle
	titles := map[string]string{
		"dashboard":     "仪表盘",
		"server_list":   "服务器管理",
		"server_detail": "服务器详情",
		"server_form":   "编辑服务器",
		"user_list":     "用户管理",
		"user_form":     "编辑用户",
		"website_list":  "网站管理",
		"website_form":  "编辑网站",
		"provider_list": "服务商管理",
		"provider_form": "编辑服务商",
		"monitor":       "性能监控",
		"firewall":      "安全防御",
		"alert_rules":   "告警规则",
		"alert_log":     "告警日志",
		"settings":      "系统设置",
	}
	if t, ok := titles[active]; ok {
		title = t + " — " + title
	}
	csrfToken := middleware.GetCSRFToken(c)
	return gin.H{
		"Title":           title,
		"PanelTitle":      cfg.Panel.PanelTitle,
		"PanelVersion":    cfg.Panel.Version,
		"ContentTemplate": contentTpl,
		"RandomSuffix":    cfg.Panel.RandomSuffix,
		"Active":          active,
		"AssetPrefix":     "/" + cfg.Panel.RandomSuffix + "/assets",
		"CSRFToken":       csrfToken,
	}
}
