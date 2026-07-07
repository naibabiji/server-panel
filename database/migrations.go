package database

var migrations = []string{
	// admin_users
	`CREATE TABLE IF NOT EXISTS admin_users (
		id            INTEGER PRIMARY KEY AUTOINCREMENT,
		username      TEXT    NOT NULL UNIQUE,
		password_hash TEXT    NOT NULL,
		created_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	)`,

	// customers
	`CREATE TABLE IF NOT EXISTS customers (
		id                INTEGER PRIMARY KEY AUTOINCREMENT,
		name              TEXT NOT NULL,
		contact_person    TEXT NOT NULL DEFAULT '',
		phone             TEXT NOT NULL DEFAULT '',
		email             TEXT NOT NULL DEFAULT '',
		company           TEXT NOT NULL DEFAULT '',
		start_date        TEXT NOT NULL DEFAULT '',
		address           TEXT NOT NULL DEFAULT '',
		notes             TEXT NOT NULL DEFAULT '',
		created_at        DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at        DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	)`,

	// providers
	`CREATE TABLE IF NOT EXISTS providers (
		id                INTEGER PRIMARY KEY AUTOINCREMENT,
		name              TEXT NOT NULL UNIQUE,
		website           TEXT NOT NULL DEFAULT '',
		contact           TEXT NOT NULL DEFAULT '',
		notes             TEXT NOT NULL DEFAULT '',
		created_at        DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at        DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	)`,

	// servers
	`CREATE TABLE IF NOT EXISTS servers (
		id                    INTEGER PRIMARY KEY AUTOINCREMENT,
		name                  TEXT NOT NULL,
		ip_address            TEXT NOT NULL DEFAULT '',
		server_type           TEXT NOT NULL DEFAULT 'vps',
		os                    TEXT NOT NULL DEFAULT '',
		customer_id           INTEGER,
		cpu_cores             REAL NOT NULL DEFAULT 0,
		ram_gb                REAL NOT NULL DEFAULT 0,
		disk_gb               REAL NOT NULL DEFAULT 0,
		bandwidth             TEXT NOT NULL DEFAULT '',
		provider_id           INTEGER,
		location              TEXT NOT NULL DEFAULT '',
		ssh_port              INTEGER NOT NULL DEFAULT 22,
		ssh_username          TEXT NOT NULL DEFAULT 'root',
		ssh_password_enc      TEXT NOT NULL DEFAULT '',
		panel_type            TEXT NOT NULL DEFAULT 'none',
		panel_url             TEXT NOT NULL DEFAULT '',
		panel_username        TEXT NOT NULL DEFAULT '',
		panel_password_enc    TEXT NOT NULL DEFAULT '',
		purchase_date         TEXT NOT NULL DEFAULT '',
		expiry_date           TEXT NOT NULL DEFAULT '',
		renewal_cycle         TEXT NOT NULL DEFAULT '',
		auto_renewal          INTEGER NOT NULL DEFAULT 0,
		purchase_price        REAL NOT NULL DEFAULT 0,
		currency              TEXT NOT NULL DEFAULT 'USD',
		status                TEXT NOT NULL DEFAULT 'active',
		agent_api_key_hash    TEXT NOT NULL DEFAULT '',
		agent_api_key_enc     TEXT NOT NULL DEFAULT '',
		agent_version         TEXT NOT NULL DEFAULT '',
		last_seen_at          DATETIME,
		is_online             INTEGER NOT NULL DEFAULT 0,
		http_probe_enabled    INTEGER NOT NULL DEFAULT 0,
		http_probe_healthy    INTEGER,
		http_probe_last_at    DATETIME,
		http_probe_last_error TEXT NOT NULL DEFAULT '',
		status_page_enabled   INTEGER NOT NULL DEFAULT 0,
		status_page_token     TEXT NOT NULL DEFAULT '',
		status_page_password  TEXT NOT NULL DEFAULT '',
		notes                 TEXT NOT NULL DEFAULT '',
		created_at            DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at            DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		FOREIGN KEY (customer_id) REFERENCES customers(id) ON DELETE SET NULL,
		FOREIGN KEY (provider_id) REFERENCES providers(id) ON DELETE SET NULL
	)`,
	`CREATE INDEX IF NOT EXISTS idx_servers_status ON servers(status)`,
	`CREATE INDEX IF NOT EXISTS idx_servers_customer ON servers(customer_id)`,
	`CREATE INDEX IF NOT EXISTS idx_servers_provider ON servers(provider_id)`,
	`CREATE INDEX IF NOT EXISTS idx_servers_expiry ON servers(expiry_date)`,
	`CREATE UNIQUE INDEX IF NOT EXISTS idx_servers_agent_key_unique ON servers(agent_api_key_hash) WHERE agent_api_key_hash <> ''`,
	`CREATE UNIQUE INDEX IF NOT EXISTS idx_servers_status_page_token_unique ON servers(status_page_token) WHERE status_page_token <> ''`,

	// websites
	`CREATE TABLE IF NOT EXISTS websites (
		id              INTEGER PRIMARY KEY AUTOINCREMENT,
		name            TEXT NOT NULL DEFAULT '',
		domain          TEXT NOT NULL,
		site_type       TEXT NOT NULL DEFAULT '',
		server_id       INTEGER NOT NULL,
		customer_id     INTEGER,
		panel_type      TEXT NOT NULL DEFAULT 'none',
		panel_url       TEXT NOT NULL DEFAULT '',
		panel_username  TEXT NOT NULL DEFAULT '',
		panel_password_enc TEXT NOT NULL DEFAULT '',
		expiry_date     TEXT,
		status          TEXT NOT NULL DEFAULT 'active',
		notes           TEXT NOT NULL DEFAULT '',
		created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		FOREIGN KEY (server_id) REFERENCES servers(id) ON DELETE RESTRICT,
		FOREIGN KEY (customer_id) REFERENCES customers(id) ON DELETE SET NULL
	)`,
	`CREATE INDEX IF NOT EXISTS idx_websites_server ON websites(server_id)`,
	`CREATE INDEX IF NOT EXISTS idx_websites_customer ON websites(customer_id)`,
	`CREATE INDEX IF NOT EXISTS idx_websites_domain ON websites(domain)`,
	`CREATE INDEX IF NOT EXISTS idx_websites_expiry ON websites(expiry_date)`,

	// metrics
	`CREATE TABLE IF NOT EXISTS metrics (
		id              INTEGER PRIMARY KEY AUTOINCREMENT,
		server_id       INTEGER NOT NULL,
		cpu_percent     REAL,
		memory_percent  REAL,
		memory_used     INTEGER,
		memory_total    INTEGER,
		disk_percent    REAL,
		disk_used       INTEGER,
		disk_total      INTEGER,
		net_rx_bytes    INTEGER,
		net_tx_bytes    INTEGER,
		load_avg_1      REAL,
		load_avg_5      REAL,
		load_avg_15     REAL,
		uptime_seconds  INTEGER,
		ingest_latency_us INTEGER,
		recorded_at     DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		FOREIGN KEY (server_id) REFERENCES servers(id) ON DELETE CASCADE
	)`,
	`CREATE INDEX IF NOT EXISTS idx_metrics_server_time ON metrics(server_id, recorded_at)`,

	// alert_rules
	`CREATE TABLE IF NOT EXISTS alert_rules (
		id                     INTEGER PRIMARY KEY AUTOINCREMENT,
		alert_type             TEXT NOT NULL,
		name                   TEXT NOT NULL DEFAULT '',
		enabled                INTEGER NOT NULL DEFAULT 1,
		threshold_value        REAL NOT NULL DEFAULT 0,
		threshold_count        INTEGER NOT NULL DEFAULT 3,
		notify_user            INTEGER NOT NULL DEFAULT 0,
		notify_email           TEXT NOT NULL DEFAULT '',
		server_id              INTEGER,
		created_at             DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at             DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		FOREIGN KEY (server_id) REFERENCES servers(id) ON DELETE CASCADE
	)`,
	`CREATE INDEX IF NOT EXISTS idx_alert_rules_type ON alert_rules(alert_type, enabled)`,

	// alert_log
	`CREATE TABLE IF NOT EXISTS alert_log (
		id         INTEGER PRIMARY KEY AUTOINCREMENT,
		alert_type TEXT NOT NULL,
		server_id  INTEGER,
		website_id INTEGER,
		level      TEXT NOT NULL DEFAULT 'warning',
		message    TEXT NOT NULL,
		resolved   INTEGER NOT NULL DEFAULT 0,
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	)`,
	`CREATE INDEX IF NOT EXISTS idx_alert_log_type ON alert_log(alert_type, created_at)`,

	// firewall_bans
	`CREATE TABLE IF NOT EXISTS firewall_bans (
		id           INTEGER PRIMARY KEY AUTOINCREMENT,
		ip_address   TEXT NOT NULL,
		reason       TEXT NOT NULL DEFAULT '',
		source       TEXT NOT NULL DEFAULT 'scan_defense',
		expires_at   DATETIME,
		unbanned_at  DATETIME,
		created_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	)`,
	`CREATE INDEX IF NOT EXISTS idx_bans_ip ON firewall_bans(ip_address, unbanned_at)`,

	// whitelist
	`CREATE TABLE IF NOT EXISTS whitelist (
		id         INTEGER PRIMARY KEY AUTOINCREMENT,
		ip_address TEXT NOT NULL UNIQUE,
		notes      TEXT NOT NULL DEFAULT '',
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	)`,

	// login_attempts
	`CREATE TABLE IF NOT EXISTS login_attempts (
		id           INTEGER PRIMARY KEY AUTOINCREMENT,
		ip_address   TEXT NOT NULL,
		attempt_type TEXT NOT NULL,
		created_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	)`,
	`CREATE INDEX IF NOT EXISTS idx_attempts_ip ON login_attempts(ip_address, attempt_type, created_at)`,

	// settings
	`CREATE TABLE IF NOT EXISTS settings (
		id         INTEGER PRIMARY KEY AUTOINCREMENT,
		skey       TEXT NOT NULL UNIQUE,
		svalue     TEXT NOT NULL DEFAULT '',
		updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	)`,

	// schema_version
	`CREATE TABLE IF NOT EXISTS schema_version (
		version    TEXT NOT NULL,
		updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	)`,

	// 种子数据
	`INSERT OR IGNORE INTO settings (skey, svalue) VALUES ('smtp_host', '')`,
	`INSERT OR IGNORE INTO settings (skey, svalue) VALUES ('smtp_port', '587')`,
	`INSERT OR IGNORE INTO settings (skey, svalue) VALUES ('smtp_encryption', 'starttls')`,
	`INSERT OR IGNORE INTO settings (skey, svalue) VALUES ('smtp_user', '')`,
	`INSERT OR IGNORE INTO settings (skey, svalue) VALUES ('smtp_pass', '')`,
	`INSERT OR IGNORE INTO settings (skey, svalue) VALUES ('admin_email', '')`,
	`INSERT OR IGNORE INTO settings (skey, svalue) VALUES ('http_probe_interval_minutes', '5')`,
	`INSERT OR IGNORE INTO settings (skey, svalue) VALUES ('http_probe_timeout_seconds', '10')`,
	`INSERT OR IGNORE INTO settings (skey, svalue) VALUES ('metric_retention_days', '30')`,
	`INSERT OR IGNORE INTO settings (skey, svalue) VALUES ('ban_duration_hours', '720')`,
	`INSERT OR IGNORE INTO settings (skey, svalue) VALUES ('view_password_hash', '')`,
	`INSERT OR IGNORE INTO settings (skey, svalue) VALUES ('os_list', '["Debian 12","Debian 13","Ubuntu 22.04","Ubuntu 24.04","CentOS Stream 9","Rocky Linux 8","Rocky Linux 9","AlmaLinux 8","AlmaLinux 9","Arch Linux","其他"]')`,
	`INSERT OR IGNORE INTO settings (skey, svalue) VALUES ('site_type_list', '["WordPress","其他CMS","静态网站","自定义"]')`,
	`INSERT OR IGNORE INTO settings (skey, svalue) VALUES ('panel_title', 'Server Panel')`,
	`INSERT OR IGNORE INTO settings (skey, svalue) VALUES ('panel_auto_update_enabled', 'false')`,
	`INSERT OR IGNORE INTO settings (skey, svalue) VALUES ('panel_auto_update_mode', 'patch_only')`,
	`INSERT OR IGNORE INTO settings (skey, svalue) VALUES ('panel_auto_update_window', '03:00-05:00')`,
	`INSERT OR IGNORE INTO settings (skey, svalue) VALUES ('panel_auto_update_release_delay_minutes', '15')`,
	`INSERT OR IGNORE INTO settings (skey, svalue) VALUES ('panel_auto_update_signature_timeout_minutes', '120')`,
	`INSERT OR IGNORE INTO settings (skey, svalue) VALUES ('panel_auto_update_last_target_version', '')`,
	`INSERT OR IGNORE INTO settings (skey, svalue) VALUES ('panel_auto_update_last_check_at', '')`,
	`INSERT OR IGNORE INTO settings (skey, svalue) VALUES ('panel_auto_update_last_attempt_at', '')`,
	`INSERT OR IGNORE INTO settings (skey, svalue) VALUES ('panel_auto_update_last_status', '')`,
	`INSERT OR IGNORE INTO settings (skey, svalue) VALUES ('panel_auto_update_last_stage', '')`,
	`INSERT OR IGNORE INTO settings (skey, svalue) VALUES ('panel_auto_update_last_error', '')`,
	`INSERT OR IGNORE INTO settings (skey, svalue) VALUES ('panel_auto_update_last_success_at', '')`,
	`INSERT OR IGNORE INTO settings (skey, svalue) VALUES ('panel_auto_update_last_success_version', '')`,
	`INSERT OR IGNORE INTO settings (skey, svalue) VALUES ('panel_auto_update_signature_wait_version', '')`,
	`INSERT OR IGNORE INTO settings (skey, svalue) VALUES ('panel_auto_update_signature_wait_at', '')`,
	`INSERT OR IGNORE INTO providers (name) VALUES ('DigitalOcean')`,
	`INSERT OR IGNORE INTO providers (name) VALUES ('Vultr')`,
	`INSERT OR IGNORE INTO providers (name) VALUES ('Linode')`,
	`INSERT OR IGNORE INTO providers (name) VALUES ('Hetzner')`,
	`INSERT OR IGNORE INTO providers (name) VALUES ('OVHcloud')`,
	`INSERT OR IGNORE INTO providers (name) VALUES ('Contabo')`,
	`INSERT OR IGNORE INTO providers (name) VALUES ('Hostinger')`,
	`INSERT OR IGNORE INTO providers (name) VALUES ('IONOS')`,
	`INSERT OR IGNORE INTO providers (name) VALUES ('Kamatera')`,
	`INSERT OR IGNORE INTO providers (name) VALUES ('Scaleway')`,
	`INSERT OR IGNORE INTO providers (name) VALUES ('UpCloud')`,
	`INSERT OR IGNORE INTO providers (name) VALUES ('Leaseweb')`,
	`INSERT OR IGNORE INTO providers (name) VALUES ('Oracle Cloud')`,
	`INSERT OR IGNORE INTO providers (name) VALUES ('Amazon Lightsail')`,
	`INSERT OR IGNORE INTO providers (name) VALUES ('Google Cloud')`,
	`INSERT OR IGNORE INTO providers (name) VALUES ('Microsoft Azure')`,
	`INSERT OR IGNORE INTO providers (name) VALUES ('阿里云')`,
	`INSERT OR IGNORE INTO providers (name) VALUES ('腾讯云')`,
	`INSERT OR IGNORE INTO providers (name) VALUES ('RackNerd')`,
	`INSERT OR IGNORE INTO providers (name) VALUES ('HostHatch')`,
	`INSERT OR IGNORE INTO schema_version (version) VALUES ('1.0.0')`,
}
