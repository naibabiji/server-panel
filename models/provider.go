package models

type Provider struct {
	ID              int64  `json:"id"`
	Name            string `json:"name"`
	Website         string `json:"website"`
	Contact         string `json:"contact"`
	PrivateNotes    string `json:"private_notes,omitempty"`
	HasPrivateNotes bool   `json:"has_private_notes"`
	Notes           string `json:"notes"`
	CreatedAt       string `json:"created_at"`
	UpdatedAt       string `json:"updated_at"`
}
