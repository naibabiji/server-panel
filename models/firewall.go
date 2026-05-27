package models

type FirewallBan struct {
	ID         int64  `json:"id"`
	IPAddress  string `json:"ip_address"`
	Reason     string `json:"reason"`
	Source     string `json:"source"`
	ExpiresAt  string `json:"expires_at"`
	UnbannedAt string `json:"unbanned_at"`
	CreatedAt  string `json:"created_at"`
}

type WhitelistEntry struct {
	ID        int64  `json:"id"`
	IPAddress string `json:"ip_address"`
	Notes     string `json:"notes"`
	CreatedAt string `json:"created_at"`
}
