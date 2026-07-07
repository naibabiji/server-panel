package models

type Provider struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	Website   string `json:"website"`
	Contact   string `json:"contact"`
	Notes     string `json:"notes"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}
