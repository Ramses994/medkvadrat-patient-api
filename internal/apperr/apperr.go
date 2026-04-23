package apperr

import "net/http"

type AppError struct {
	Status  int
	Code    string
	Message string
}

func (e *AppError) Error() string { return e.Message }

func New(status int, code, msg string) *AppError {
	return &AppError{Status: status, Code: code, Message: msg}
}

var (
	ErrUnauthorized = New(http.StatusUnauthorized, "UNAUTHORIZED", "Unauthorized")
)
