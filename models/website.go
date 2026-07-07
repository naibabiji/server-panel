package models

const (
	WebsiteStatusActive  = "active"
	WebsiteStatusExpired = "expired"
)

var WebsiteStatuses = []string{WebsiteStatusActive, WebsiteStatusExpired}

type Website struct {
	ID               int64  `json:"id"`
	Name             string `json:"name"`
	Domain           string `json:"domain"`
	SiteType         string `json:"site_type"`
	ServerID         int64  `json:"server_id"`
	ServerName       string `json:"server_name,omitempty"`
	CustomerID       *int64 `json:"customer_id"`
	CustomerName     string `json:"customer_name,omitempty"`
	PanelType        string `json:"panel_type"`
	PanelURL         string `json:"panel_url"`
	PanelUsername    string `json:"panel_username"`
	PanelPasswordEnc string `json:"-"`
	PanelPassword    string `json:"panel_password,omitempty"`
	ExpiryDate       string `json:"expiry_date"`
	Status           string `json:"status"`
	Notes            string `json:"notes"`
	CreatedAt        string `json:"created_at"`
	UpdatedAt        string `json:"updated_at"`
}
