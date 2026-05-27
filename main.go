package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/naibabiji/server-panel/config"
	"github.com/naibabiji/server-panel/database"
	"github.com/naibabiji/server-panel/executor"
	"github.com/naibabiji/server-panel/router"
)

var (
	Version   = "dev"
	BuildTime = "unknown"
)

func main() {
	configPath := flag.String("config", "/www/server/server-panel/config.json", "配置文件路径")
	flag.Parse()

	fmt.Printf("Server Panel %s (build %s)\n", Version, BuildTime)

	// 加载配置
	cfg, err := config.LoadConfig(*configPath)
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}
	cfg.Panel.Version = Version

	// 创建数据目录
	os.MkdirAll(cfg.Panel.DataDir, 0700)
	os.MkdirAll(cfg.Panel.LogDir, 0700)

	// 打开数据库
	if err := database.Open(cfg.SQLite.Path); err != nil {
		log.Fatalf("Failed to open database: %v", err)
	}
	defer database.Close()

	// 运行迁移
	if err := database.RunMigrations(); err != nil {
		log.Fatalf("Failed to run migrations: %v", err)
	}

	// 运行升级
	if err := database.RunUpgrades(); err != nil {
		log.Fatalf("Failed to run upgrades: %v", err)
	}

	// 确保管理员账户存在
	seedAdminUser(cfg)

	// 设置路由
	r := router.SetupRouter(cfg, database.GetDB(), StaticFS, TemplatesFS)

	// 启动后台任务
	executor.StartMetricCleanup(1 * time.Hour)
	executor.StartHTTPProber(5 * time.Minute) // 间隔由 http_probe_interval_minutes 设置决定

	// 启动服务器
	go func() {
		addrHTTP := fmt.Sprintf(":%d", cfg.Panel.Port)
		log.Printf("HTTP server listening on %s", addrHTTP)

		// HSTS 仅在 ACME 模式 + 证书有效时启用
		_ = http.ListenAndServe(addrHTTP, r)
	}()

	go func() {
		if cfg.Panel.TLSPort > 0 && cfg.Panel.TLSCertPath != "" && cfg.Panel.TLSKeyPath != "" {
			addrTLS := fmt.Sprintf(":%d", cfg.Panel.TLSPort)
			log.Printf("HTTPS server listening on %s (mode: %s)", addrTLS, cfg.Panel.TLSMode)

			if err := r.RunTLS(addrTLS, cfg.Panel.TLSCertPath, cfg.Panel.TLSKeyPath); err != nil {
				log.Fatalf("Failed to start TLS server: %v", err)
			}
		}
	}()

	// 等待信号
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	sig := <-quit
	log.Printf("Received signal %v, shutting down...", sig)
}

func seedAdminUser(cfg *config.Config) {
	db := database.GetDB()
	if db == nil {
		return
	}

	var count int
	db.QueryRow("SELECT COUNT(*) FROM admin_users").Scan(&count)
	if count == 0 {
		if cfg.Admin.Username != "" && cfg.Admin.PasswordHash != "" {
			db.Exec("INSERT OR IGNORE INTO admin_users (username, password_hash) VALUES (?, ?)",
				cfg.Admin.Username, cfg.Admin.PasswordHash)
			log.Printf("Admin user '%s' created from config", cfg.Admin.Username)
		}
	}
}
