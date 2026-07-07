package models

type Customer struct {
	ID            int64  `json:"id"`
	Name          string `json:"name"`
	ContactPerson string `json:"contact_person"`
	Phone         string `json:"phone"`
	Email         string `json:"email"`
	Company       string `json:"company"`
	StartDate     string `json:"start_date"`
	Address       string `json:"address"`
	Notes         string `json:"notes"`
	CreatedAt     string `json:"created_at"`
	UpdatedAt     string `json:"updated_at"`
}
