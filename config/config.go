package config

import (
	"encoding/json"
	"fmt"
	"os"
)

var AppConfig *Config

type Config struct {
	Panel     PanelConfig     `json:"panel"`
	SQLite    SQLiteConfig    `json:"sqlite"`
	Admin     AdminConfig     `json:"admin"`
	BasicAuth BasicAuthConfig `json:"basic_auth"`
	Security  SecurityConfig  `json:"security"`
	Systemd   SystemdConfig   `json:"systemd"`
}

type PanelConfig struct {
	Version            string `json:"version"`
	Port               int    `json:"port"`
	TLSPort            int    `json:"tls_port"`
	TLSCertPath        string `json:"tls_cert_path"`
	TLSKeyPath         string `json:"tls_key_path"`
	TLSMode            string `json:"tls_mode"`
	Domain             string `json:"domain"`
	PublicURL          string `json:"public_url"`
	ACMEEmail          string `json:"acme_email"`
	ACMEDirectoryURL  string `json:"acme_directory_url"`
	ACMEStoragePath    string `json:"acme_storage_path"`
	ACMEChallengePort  int    `json:"acme_challenge_port"`
	RandomSuffix       string `json:"random_suffix"`
	DataDir            string `json:"data_dir"`
	LogDir             string `json:"log_dir"`
	PanelTitle         string `json:"panel_title"`
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
	if cfg.Panel.ACMEDirectoryURL == "" {
		cfg.Panel.ACMEDirectoryURL = "https://acme-v02.api.letsencrypt.org/directory"
	}
	if cfg.Panel.PanelTitle == "" {
		cfg.Panel.PanelTitle = "Server Panel"
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
