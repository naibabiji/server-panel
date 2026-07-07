package models

const (
	AlertTypeServerExpiry  = "server_expiry"
	AlertTypeWebsiteExpiry = "website_expiry"
	AlertTypeHTTPProbe     = "http_probe_down"
	AlertTypeCPUHigh       = "cpu_high"
	AlertTypeMemoryHigh    = "memory_high"
	AlertTypeDiskHigh      = "disk_high"
	AlertTypeServerOffline = "server_offline"
)

type AlertRule struct {
	ID              int64   `json:"id"`
	AlertType       string  `json:"alert_type"`
	Name            string  `json:"name"`
	Enabled         int     `json:"enabled"`
	ThresholdValue  float64 `json:"threshold_value"`
	ThresholdCount  int     `json:"threshold_count"`
	NotifyUser      int     `json:"notify_user"`
	NotifyEmail     string  `json:"notify_email"`
	ServerID        *int64  `json:"server_id"`
	ServerName      string  `json:"server_name,omitempty"`
	CreatedAt       string  `json:"created_at"`
	UpdatedAt       string  `json:"updated_at"`
}

type AlertLog struct {
	ID        int64  `json:"id"`
	AlertType string `json:"alert_type"`
	ServerID  *int64 `json:"server_id"`
	WebsiteID *int64 `json:"website_id"`
	Level     string `json:"level"`
	Message   string `json:"message"`
	Resolved  int    `json:"resolved"`
	CreatedAt string `json:"created_at"`
}
