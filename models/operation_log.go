package models

type OperationLog struct {
	ID        int64  `json:"id"`
	Operation string `json:"operation"`
	Target    string `json:"target"`
	Status    string `json:"status"`
	Message   string `json:"message"`
	CreatedAt string `json:"created_at"`
}
