package api

import "net/http"

import "github.com/medkvadrat/medkvadrat-patient-api/internal/response"

func OK(w http.ResponseWriter, data interface{}) {
	response.OK(w, data)
}

func NoContent(w http.ResponseWriter) {
	response.NoContent(w)
}

func Error(w http.ResponseWriter, status int, code, msg string) {
	response.Error(w, status, code, msg)
}
