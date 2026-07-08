package main

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/naibabiji/server-panel/config"
	"github.com/naibabiji/server-panel/database"
	"github.com/naibabiji/server-panel/executor"
	"github.com/naibabiji/server-panel/middleware"
	"github.com/naibabiji/server-panel/router"
	"golang.org/x/crypto/acme/autocert"
	"golang.org/x/crypto/bcrypt"
)

var (
	Version        = "dev"
	BuildTime      = "unknown"
	unbanAll       = flag.Bool("unban-all", false, "清除面板所有封禁IP并清空登录尝试记录")
	resetPassword  = flag.Bool("reset-password", false, "重置管理员密码（BasicAuth 和面板登录）")
	hashPassword   = flag.String("hash-password", "", "生成指定密码的 bcrypt 哈希后退出")
	showInfo       = flag.Bool("info", false, "打印版本/端口/路径信息后退出（更新前的冒烟测试）")
	updateWatchdog = flag.String("update-watchdog", "", "内部使用：以看护进程身份监控更新后的健康检查并按需回滚")
	restoreBackup  = flag.String("restore-backup", "", "从设置页生成的备份归档(.tar.gz)恢复数据库和密钥后退出；请先停止 server-panel 服务再执行")
)

func main() {
	configPath := flag.String("config", "/www/server/server-panel/config.json", "配置文件路径")
	flag.Parse()

	if *hashPassword != "" {
		hash, err := bcrypt.GenerateFromPassword([]byte(*hashPassword), 12)
		if err != nil {
			log.Fatalf("生成密码哈希失败: %v", err)
		}
		fmt.Println(string(hash))
		return
	}

	fmt.Printf("Server Panel %s (build %s)\n", Version, BuildTime)

	cfg, err := config.LoadConfig(*configPath)
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}
	cfg.Panel.Version = Version

	if *showInfo {
		fmt.Printf("version=%s tls_port=%d data_dir=%s tls_mode=%s\n",
			Version, cfg.Panel.TLSPort, cfg.Panel.DataDir, cfg.Panel.TLSMode)
		return
	}
	if *updateWatchdog != "" {
		if err := database.Open(cfg.SQLite.Path); err != nil {
			log.Printf("watchdog: failed to open database for audit/settings: %v", err)
		} else {
			defer database.Close()
		}
		executor.RunUpdateWatchdog(*updateWatchdog, cfg)
		return
	}

	os.MkdirAll(cfg.Panel.DataDir, 0700)
	os.MkdirAll(cfg.Panel.LogDir, 0700)

	if *restoreBackup != "" {
		if err := runRestoreBackup(cfg, *restoreBackup); err != nil {
			log.Fatalf("恢复备份失败: %v", err)
		}
		return
	}

	if err := database.Open(cfg.SQLite.Path); err != nil {
		log.Fatalf("Failed to open database: %v", err)
	}
	defer database.Close()

	if err := database.RunMigrations(); err != nil {
		log.Fatalf("Failed to run migrations: %v", err)
	}
	if err := database.RunUpgrades(); err != nil {
		log.Fatalf("Failed to run upgrades: %v", err)
	}

	if *unbanAll {
		executor.InitNFTables(cfg.Panel.TLSPort)
		runUnbanAll()
		return
	}
	if *resetPassword {
		runResetPassword(cfg, *configPath)
		database.Close()
		fmt.Printf("正在重启服务...\n")
		if err := exec.Command("systemctl", "restart", "server-panel").Run(); err != nil {
			fmt.Printf("重启失败: %v，请手动执行 systemctl restart server-panel\n", err)
		} else {
			fmt.Printf("服务已重启\n")
		}
		return
	}

	seedAdminUser(cfg)
	syncPanelTitleSetting(cfg)

	r := router.SetupRouter(cfg, database.GetDB(), StaticFS, TemplatesFS)
	r.GET("/healthz", func(c *gin.Context) {
		if err := database.GetDB().Ping(); err != nil {
			c.Status(http.StatusInternalServerError)
			return
		}
		c.Status(http.StatusOK)
	})

	executor.StartMetricCleanup(1 * time.Hour)
	executor.StartHTTPProber(5 * time.Minute)
	executor.StartAgentOfflineChecker(60 * time.Second)
	executor.StartAlertChecker(60 * time.Second)
	executor.StartDatabaseBackupScheduler(1 * time.Hour)
	executor.StartAutoRenewalChecker(24 * time.Hour)
	executor.InitNFTables(cfg.Panel.TLSPort)
	executor.StartBanCleanup(1 * time.Minute)
	middleware.StartAgentRateLimiterCleanup(5 * time.Minute)
	if cfg.Panel.TrustCloudflare {
		executor.StartCloudflareIPRefresh(24 * time.Hour)
	}
	executor.FinalizePendingPanelUpdate(cfg, Version)
	executor.StartPanelAutoUpdateScheduler(Version, *configPath, cfg)

	go func() {
		for {
			time.Sleep(30 * time.Minute)
			middleware.GlobalSessionStore.CleanExpired()
		}
	}()

	go func() {
		addrTLS := fmt.Sprintf(":%d", cfg.Panel.TLSPort)
		if cfg.Panel.TLSMode == "acme" && cfg.Panel.Domain != "" {
			manager := &autocert.Manager{
				Cache:      autocert.DirCache(cfg.Panel.ACMEStoragePath),
				Prompt:     autocert.AcceptTOS,
				HostPolicy: autocert.HostWhitelist(cfg.Panel.Domain),
				Email:      cfg.Panel.ACMEEmail,
			}
			go func() {
				addrChallenge := fmt.Sprintf(":%d", cfg.Panel.ACMEChallengePort)
				log.Printf("ACME challenge server listening on %s", addrChallenge)
				challengeSrv := &http.Server{
					Addr:              addrChallenge,
					Handler:           manager.HTTPHandler(nil),
					ReadHeaderTimeout: 15 * time.Second,
					ReadTimeout:       30 * time.Second,
					IdleTimeout:       2 * time.Minute,
				}
				if err := challengeSrv.ListenAndServe(); err != nil {
					log.Printf("ACME challenge server error: %v", err)
				}
			}()
			log.Printf("HTTPS server listening on %s (mode: acme, domain: %s)", addrTLS, cfg.Panel.Domain)
			srv := &http.Server{
				Addr:              addrTLS,
				Handler:           r,
				TLSConfig:         manager.TLSConfig(),
				ReadHeaderTimeout: 15 * time.Second,
				ReadTimeout:       60 * time.Second,
				IdleTimeout:       2 * time.Minute,
				// WriteTimeout intentionally unset: it bounds the whole
				// request lifecycle including handler time, and the system
				// package update endpoint runs "apt upgrade" synchronously
				// for up to 2 hours (handlers/system_update.go).
			}
			if err := srv.ListenAndServeTLS("", ""); err != nil {
				log.Printf("TLS server error: %v", err)
			}
			return
		}
		if cfg.Panel.TLSPort > 0 && cfg.Panel.TLSCertPath != "" && cfg.Panel.TLSKeyPath != "" {
			log.Printf("HTTPS server listening on %s (mode: %s)", addrTLS, cfg.Panel.TLSMode)
			srv := &http.Server{
				Addr:              addrTLS,
				Handler:           r,
				ReadHeaderTimeout: 15 * time.Second,
				ReadTimeout:       60 * time.Second,
				IdleTimeout:       2 * time.Minute,
				// WriteTimeout intentionally unset, see comment above.
			}
			if err := srv.ListenAndServeTLS(cfg.Panel.TLSCertPath, cfg.Panel.TLSKeyPath); err != nil {
				log.Printf("TLS server error: %v", err)
			}
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	sig := <-quit
	log.Printf("Received signal %v, shutting down...", sig)
}

// runRestoreBackup restores a database.CreateFullBackupArchive backup onto
// this panel's live paths. It must run before database.Open, since it
// replaces the live database file out from under it; the server must be
// stopped for the duration (there is no live DB connection to guard here
// because callers invoke this ahead of the normal startup path).
func runRestoreBackup(cfg *config.Config, archivePath string) error {
	if _, err := os.Stat(archivePath); err != nil {
		return fmt.Errorf("找不到备份文件: %w", err)
	}

	tmpDir, err := os.MkdirTemp("", "server-panel-restore-*")
	if err != nil {
		return fmt.Errorf("创建临时目录失败: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath, secretKeyPath, err := database.ExtractFullBackupArchive(archivePath, tmpDir)
	if err != nil {
		return err
	}
	if err := database.VerifyDBBackup(dbPath); err != nil {
		return fmt.Errorf("备份数据库校验失败，未执行恢复: %w", err)
	}

	liveDBPath := cfg.SQLite.Path
	liveSecretKeyPath := filepath.Join(cfg.Panel.DataDir, "secret.key")
	preRestoreSuffix := "." + time.Now().UTC().Format("20060102-150405") + ".pre-restore"

	if err := os.MkdirAll(filepath.Dir(liveDBPath), 0700); err != nil {
		return fmt.Errorf("创建数据库目录失败: %w", err)
	}
	if _, err := os.Stat(liveDBPath); err == nil {
		if err := copyFile(liveDBPath, liveDBPath+preRestoreSuffix); err != nil {
			return fmt.Errorf("备份当前数据库失败，未执行恢复: %w", err)
		}
		fmt.Printf("当前数据库已另存为 %s\n", liveDBPath+preRestoreSuffix)
	}
	if secretKeyPath != "" {
		if _, err := os.Stat(liveSecretKeyPath); err == nil {
			if err := copyFile(liveSecretKeyPath, liveSecretKeyPath+preRestoreSuffix); err != nil {
				return fmt.Errorf("备份当前密钥失败，未执行恢复: %w", err)
			}
			fmt.Printf("当前密钥已另存为 %s\n", liveSecretKeyPath+preRestoreSuffix)
		}
	}

	if err := database.RestoreDatabaseFile(dbPath, liveDBPath); err != nil {
		return fmt.Errorf("写入数据库失败: %w", err)
	}
	fmt.Printf("数据库已恢复到 %s\n", liveDBPath)

	if secretKeyPath != "" {
		if err := copyFile(secretKeyPath, liveSecretKeyPath); err != nil {
			return fmt.Errorf("写入密钥失败: %w", err)
		}
		fmt.Printf("密钥已恢复到 %s\n", liveSecretKeyPath)
	} else {
		fmt.Println("警告: 备份中不包含 secret.key。若数据库中仍以旧版设置项形式保存着加密密钥会自动生效；否则已加密的敏感字段（SSH/面板密码等）将无法解密。")
	}

	fmt.Println("恢复完成，请启动 server-panel 服务。")
	return nil
}

func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0600)
}

func runUnbanAll() {
	executor.UnbanAllIPs()
	fmt.Printf("已清空所有封禁 IP 和登录尝试记录\n")
}

func runResetPassword(cfg *config.Config, configPath string) {
	password := generatePassword(16)
	hash, err := bcrypt.GenerateFromPassword([]byte(password), 12)
	if err != nil {
		log.Fatalf("生成密码哈希失败: %v", err)
	}
	hashStr := string(hash)

	cfg.Admin.PasswordHash = hashStr
	cfg.BasicAuth.Username = "admin"
	cfg.BasicAuth.PasswordHash = hashStr
	config.AppConfig = cfg

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		log.Fatalf("序列化配置失败: %v", err)
	}
	if err := os.WriteFile(configPath, data, 0600); err != nil {
		log.Fatalf("写入配置文件失败: %v", err)
	}

	db := database.GetDB()
	db.Exec("DELETE FROM admin_users WHERE username = 'spadmin'")
	db.Exec("INSERT INTO admin_users (username, password_hash) VALUES ('spadmin', ?)", hashStr)

	fmt.Printf("密码已重置\n")
	fmt.Printf("BasicAuth: admin / %s\n", password)
	fmt.Printf("面板登录: spadmin / %s\n", password)
}

func generatePassword(length int) string {
	b := make([]byte, length*2)
	rand.Read(b)
	s := base64.RawURLEncoding.EncodeToString(b)
	var result []byte
	for i := 0; i < len(s) && len(result) < length; i++ {
		c := s[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') {
			result = append(result, c)
		}
	}
	return string(result)
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

func syncPanelTitleSetting(cfg *config.Config) {
	if cfg == nil || cfg.Panel.PanelTitle == "" {
		return
	}
	db := database.GetDB()
	if db == nil {
		return
	}

	var title string
	_ = db.QueryRow("SELECT svalue FROM settings WHERE skey = 'panel_title'").Scan(&title)
	if title == "" || title == "Server Panel" {
		_, _ = db.Exec("INSERT OR REPLACE INTO settings (skey, svalue) VALUES ('panel_title', ?)", cfg.Panel.PanelTitle)
	}
}
