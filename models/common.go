package models

type ApiResponse struct {
	Success   bool        `json:"success"`
	Message   string      `json:"message,omitempty"`
	ErrorCode string      `json:"error_code,omitempty"`
	Data      interface{} `json:"data,omitempty"`
}

const (
	ErrorCodeSessionRequired = "SESSION_REQUIRED"
	ErrorCodeSessionExpired  = "SESSION_EXPIRED"
)

type PaginatedResult struct {
	Items    interface{} `json:"items"`
	Total    int         `json:"total"`
	Page     int         `json:"page"`
	PageSize int         `json:"page_size"`
}

func SuccessResponse(data interface{}) ApiResponse {
	return ApiResponse{Success: true, Data: data}
}

func ErrorResponse(msg string) ApiResponse {
	return ApiResponse{Success: false, Message: msg}
}

func ErrorResponseWithCode(msg, code string) ApiResponse {
	return ApiResponse{Success: false, Message: msg, ErrorCode: code}
}
