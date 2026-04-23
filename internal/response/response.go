package response

import (
	"encoding/json"
	"net/http"
)

type ResponseOK struct {
	Success bool        `json:"success"`
	Data    interface{} `json:"data,omitempty"`
	Error   string      `json:"error,omitempty"`

	// DEPRECATED: `error` as string will be removed after max-bot migrates to v2.
	// Target removal: 2026-06-01.
	ErrorDetails *ErrorDetails `json:"error_details,omitempty"`
}

// NOTE: current max-bot expects `error` to be a string. We'll migrate to
// {error:{code,message}} in step 2/3 together with clients.
type ErrorDetails struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func OK(w http.ResponseWriter, data interface{}) {
	writeJSON(w, http.StatusOK, ResponseOK{Success: true, Data: data})
}

func NoContent(w http.ResponseWriter) {
	w.WriteHeader(http.StatusNoContent)
}

func Error(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, ResponseOK{
		Success: false,
		Error:   msg,
		ErrorDetails: &ErrorDetails{
			Code:    code,
			Message: msg,
		},
	})
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
