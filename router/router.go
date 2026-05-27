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

	// Agent routes
	ag := r.Group("")
	ag.Use(middleware.AgentAuth(db))
	{
		ah := &handlers.AgentDataHandler{DB: db}
		ag.POST(prefix+"/agent/ping", ah.Ping)
		ag.POST(prefix+"/agent/metrics", ah.ReceiveMetrics)
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
		{
			// Auth
			protected.POST("/api/auth/logout", authH.Logout)
			protected.GET("/api/auth/check", authH.Check)
			protected.GET("/api/auth/csrf-token", authH.CSRFToken)

			// View Password
			protected.GET("/api/view-password/status", vpH.GetStatus)
			protected.POST("/api/view-password/setup", vpH.Setup)
			protected.POST("/api/view-password/unlock", vpH.Unlock)
			protected.POST("/api/view-password/lock", vpH.Lock)

			// Dashboard
			dashH := &handlers.DashboardHandler{}
			protected.GET("/api/dashboard/stats", dashH.GetStats)
			protected.GET("/api/dashboard/expiring", dashH.GetExpiring)
			protected.GET("/api/dashboard/recent-alerts", dashH.GetRecentAlerts)

			// Servers
			srvH := &handlers.ServerHandler{DB: db}
			protected.GET("/api/servers", srvH.List)
			protected.POST("/api/servers", srvH.Create)
			protected.GET("/api/servers/stats", srvH.GetStats)
			protected.GET("/api/servers/:id", srvH.Get)
			protected.PUT("/api/servers/:id", srvH.Update)
			protected.DELETE("/api/servers/:id", srvH.Delete)

			// Users
			userH := &handlers.UserHandler{DB: db}
			protected.GET("/api/users", userH.List)
			protected.POST("/api/users", userH.Create)
			protected.GET("/api/users/:id", userH.Get)
			protected.PUT("/api/users/:id", userH.Update)
			protected.DELETE("/api/users/:id", userH.Delete)

			// Page routes
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
		}
	}

	// Static files
	staticSubFS, _ := fs.Sub(staticFS, "static")
	r.StaticFS(prefix+"/assets", http.FS(staticSubFS))

	// Templates
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
		"provider_list": "服务商管理",
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
