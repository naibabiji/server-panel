package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

var (
	AppConfig  *Config
	configPath string
)

// securityKeyPresent reports whether the "security" block in the raw config
// JSON explicitly contains the "basic_auth_enabled" key. This lets LoadConfig
// distinguish "field absent (old config)" from "explicitly set to false".
func securityKeyPresent(raw []byte) bool {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(raw, &top); err != nil {
		return false
	}
	secRaw, ok := top["security"]
	if !ok {
		return false
	}
	var sec map[string]json.RawMessage
	if err := json.Unmarshal(secRaw, &sec); err != nil {
		return false
	}
	_, ok = sec["basic_auth_enabled"]
	return ok
}

// ConfigPath returns the path LoadConfig was last called with, so other
// packages (e.g. the panel self-update flow, which needs to hand it to a
// watchdog subprocess) don't need their own copy of it.
func ConfigPath() string {
	return configPath
}

func SaveConfig(cfg *Config) error {
	path := configPath
	if path == "" {
		path = "/www/server/server-panel/config.json"
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化配置失败: %w", err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("写入配置失败: %w", err)
	}
	return nil
}

type Config struct {
	Panel     PanelConfig     `json:"panel"`
	SQLite    SQLiteConfig    `json:"sqlite"`
	Admin     AdminConfig     `json:"admin"`
	BasicAuth BasicAuthConfig `json:"basic_auth"`
	Security  SecurityConfig  `json:"security"`
	Systemd   SystemdConfig   `json:"systemd"`
}

type PanelConfig struct {
	Version           string `json:"version"`
	TLSPort           int    `json:"tls_port"`
	TLSCertPath       string `json:"tls_cert_path"`
	TLSKeyPath        string `json:"tls_key_path"`
	TLSMode           string `json:"tls_mode"`
	Domain            string `json:"domain"`
	ACMEEmail         string `json:"acme_email"`
	ACMEDirectoryURL  string `json:"acme_directory_url"`
	ACMEStoragePath   string `json:"acme_storage_path"`
	ACMEChallengePort int    `json:"acme_challenge_port"`
	RandomSuffix      string `json:"random_suffix"`
	DataDir           string `json:"data_dir"`
	LogDir            string `json:"log_dir"`
	PanelTitle        string `json:"panel_title"`
	// TrustedProxies lists the IPs/CIDRs of reverse proxies allowed to set
	// X-Forwarded-For/X-Real-IP. Defaults to loopback (see LoadConfig) to
	// cover a same-host Nginx/Caddy out of the box; set to [] explicitly to
	// trust nothing, or add a specific address for a remote reverse proxy.
	TrustedProxies []string `json:"trusted_proxies"`
	// TrustCloudflare enables recognizing Cloudflare's published edge IP
	// ranges (auto-refreshed periodically) and reading the real visitor IP
	// from CF-Connecting-IP for requests coming from them. Only enable this
	// if the panel is actually fronted by Cloudflare.
	TrustCloudflare bool `json:"trust_cloudflare"`
}

type SQLiteConfig struct {
	Path string `json:"path"`
}

type AdminConfig struct {
	Username     string `json:"username"`
	PasswordHash string `json:"password_hash"`
}

type BasicAuthConfig struct {
	Username     string `json:"username"`
	PasswordHash string `json:"password_hash"`
}

type SecurityConfig struct {
	BasicAuthEnabled     bool `json:"basic_auth_enabled"`
	MaxLoginAttempts     int  `json:"max_login_attempts"`
	AttemptWindowMinutes int  `json:"attempt_window_minutes"`
	BanDurationHours     int  `json:"ban_duration_hours"`
}

type SystemdConfig struct {
	ServiceName string `json:"service_name"`
	ServicePath string `json:"service_path"`
	BinaryPath  string `json:"binary_path"`
}

func LoadConfig(path string) (*Config, error) {
	configPath = path
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config: %w", err)
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config: %w", err)
	}

	if cfg.Panel.DataDir == "" {
		cfg.Panel.DataDir = "/www/server/server-panel"
	}
	if cfg.Panel.LogDir == "" {
		cfg.Panel.LogDir = cfg.Panel.DataDir + "/logs"
	}
	if cfg.Panel.TLSMode == "" {
		cfg.Panel.TLSMode = "self_signed"
	}
	if cfg.Panel.ACMEChallengePort == 0 {
		cfg.Panel.ACMEChallengePort = 80
	}
	if cfg.Panel.ACMEStoragePath == "" {
		cfg.Panel.ACMEStoragePath = filepath.Join(cfg.Panel.DataDir, "certs", "acme")
	}
	if cfg.Panel.PanelTitle == "" {
		cfg.Panel.PanelTitle = "Server Panel"
	}
	if cfg.Panel.TrustedProxies == nil {
		// Trust a same-host reverse proxy by default: the kernel drops
		// inbound packets that claim a loopback source address on a
		// non-loopback interface, so honoring X-Forwarded-For from
		// 127.0.0.1/::1 doesn't let a remote attacker spoof ClientIP().
		// Set "trusted_proxies": [] explicitly to opt out.
		cfg.Panel.TrustedProxies = []string{"127.0.0.1", "::1"}
	}
	if cfg.Security.MaxLoginAttempts == 0 {
		cfg.Security.MaxLoginAttempts = 5
	}
	if cfg.Security.AttemptWindowMinutes == 0 {
		cfg.Security.AttemptWindowMinutes = 5
	}
	if cfg.Security.BanDurationHours == 0 {
		cfg.Security.BanDurationHours = 24
	}
	// 旧配置可能没有 basic_auth_enabled 字段（之前版本强制开启 BasicAuth）。
	// 为向后兼容、避免升级后悄悄关闭这层防护：仅当字段缺失时才默认开启；
	// 若用户显式写为 false，则尊重其关闭选择。
	if !securityKeyPresent(data) && !cfg.Security.BasicAuthEnabled {
		cfg.Security.BasicAuthEnabled = true
	}
	if cfg.Panel.TrustedProxies == nil {
		// Trust a same-host reverse proxy by default: the kernel drops
		// inbound packets that claim a loopback source address on a
		// non-loopback interface, so honoring X-Forwarded-For from
		// 127.0.0.1/::1 doesn't let a remote attacker spoof ClientIP().
		// Set "trusted_proxies": [] explicitly to opt out.
		cfg.Panel.TrustedProxies = []string{"127.0.0.1", "::1"}
	}
	if cfg.Systemd.ServiceName == "" {
		cfg.Systemd.ServiceName = "server-panel"
	}
	if cfg.Systemd.ServicePath == "" {
		cfg.Systemd.ServicePath = "/etc/systemd/system/server-panel.service"
	}
	if cfg.Systemd.BinaryPath == "" {
		cfg.Systemd.BinaryPath = "/usr/local/bin/server-panel"
	}

	AppConfig = &cfg
	return &cfg, nil
}
