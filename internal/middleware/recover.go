package middleware

import (
	"log/slog"
	"net/http"

	"github.com/medkvadrat/medkvadrat-patient-api/internal/response"
)

func Recover(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				logger.Error("panic recovered", "request_id", GetRequestID(r.Context()), "recover", rec)
				response.Error(w, http.StatusInternalServerError, "INTERNAL", "Внутренняя ошибка")
			}
		}()
		next.ServeHTTP(w, r)
	})
}
