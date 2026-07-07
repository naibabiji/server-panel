package models

// PanelType 常量
const (
	PanelTypeNone        = "none"
	PanelTypeBaota       = "baota"
	PanelTypeWPPanel     = "wp-panel"
	PanelType1Panel      = "1panel"
	PanelTypeCyberPanel  = "cyberpanel"
	PanelTypeDirectAdmin = "directadmin"
	PanelTypeCPanel      = "cpanel"
	PanelTypeOther       = "other"
)

var PanelTypes = []string{PanelTypeNone, PanelTypeBaota, PanelTypeWPPanel, PanelType1Panel, PanelTypeCyberPanel, PanelTypeDirectAdmin, PanelTypeCPanel, PanelTypeOther}

// ServerType 常量
const (
	ServerTypeVPS       = "vps"
	ServerTypeDedicated = "dedicated"
	ServerTypeShared    = "shared"
	ServerTypeOtherS    = "other"
)

var ServerTypes = []string{ServerTypeVPS, ServerTypeDedicated, ServerTypeShared, ServerTypeOtherS}

// ServerStatus 常量
const (
	ServerStatusActive  = "active"
	ServerStatusExpired = "expired"
)

var ServerStatuses = []string{ServerStatusActive, ServerStatusExpired}

// RenewalCycle 常量
const (
	RenewalMonthly   = "monthly"
	RenewalQuarterly = "quarterly"
	RenewalYearly    = "yearly"
	Renewal2Year     = "2year"
	Renewal3Year     = "3year"
)

var RenewalCycles = []string{RenewalMonthly, RenewalQuarterly, RenewalYearly, Renewal2Year, Renewal3Year}

// Currency 常量
var Currencies = []string{"USD", "CNY", "EUR", "JPY"}

// AutoRenewal 常量
var AutoRenewalOptions = []string{"是", "否"}

type Server struct {
	ID                 int64   `json:"id"`
	Name               string  `json:"name"`
	IPAddress          string  `json:"ip_address"`
	ServerType         string  `json:"server_type"`
	OS                 string  `json:"os"`
	CustomerID         *int64  `json:"customer_id"`
	CustomerName       string  `json:"customer_name,omitempty"`
	CPUCores           float64 `json:"cpu_cores"`
	RAMGB              float64 `json:"ram_gb"`
	DiskGB             float64 `json:"disk_gb"`
	Bandwidth          string  `json:"bandwidth"`
	ProviderID         *int64  `json:"provider_id"`
	ProviderName       string  `json:"provider_name,omitempty"`
	Location           string  `json:"location"`
	SSHPort            int     `json:"ssh_port"`
	SSHUsername        string  `json:"ssh_username"`
	SSHPasswordEnc     string  `json:"-"`
	SSHPassword        string  `json:"ssh_password,omitempty"`
	PanelType          string  `json:"panel_type"`
	PanelURL           string  `json:"panel_url"`
	PanelUsername      string  `json:"panel_username"`
	PanelPasswordEnc   string  `json:"-"`
	PanelPassword      string  `json:"panel_password,omitempty"`
	PurchaseDate       string  `json:"purchase_date"`
	ExpiryDate         string  `json:"expiry_date"`
	RenewalCycle       string  `json:"renewal_cycle"`
	AutoRenewal        int     `json:"auto_renewal"`
	PurchasePrice      float64 `json:"purchase_price"`
	Currency           string  `json:"currency"`
	Status             string  `json:"status"`
	AgentAPIKeyHash    string  `json:"-"`
	AgentAPIKeyEnc     string  `json:"-"`
	AgentAPIKey        string  `json:"agent_api_key,omitempty"`
	AgentVersion       string  `json:"agent_version"`
	LastSeenAt         string  `json:"last_seen_at"`
	IsOnline           bool    `json:"is_online"`
	HTTPProbeEnabled   int     `json:"http_probe_enabled"`
	HTTPProbeHealthy   *int    `json:"http_probe_healthy"`
	HTTPProbeLastAt    string  `json:"http_probe_last_at"`
	HTTPProbeLastError string  `json:"http_probe_last_error"`
	StatusPageEnabled  int     `json:"status_page_enabled"`
	StatusPageToken    string  `json:"status_page_token"`
	StatusPagePassword string  `json:"-"`
	Notes              string  `json:"notes"`
	CreatedAt          string  `json:"created_at"`
	UpdatedAt          string  `json:"updated_at"`
}

// ServerPublic 公开状态页视图（仅暴露非敏感字段）
type ServerPublic struct {
	Name     string `json:"name"`
	IsOnline bool   `json:"is_online"`
}
