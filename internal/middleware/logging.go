package middleware

import (
	"log/slog"
	"net/http"
	"time"
)

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

func Logging(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		start := time.Now()

		next.ServeHTTP(sw, r)

		logger.Info(
			"http request",
			"request_id", GetRequestID(r.Context()),
			"method", r.Method,
			"route", r.URL.Path,
			"status", sw.status,
			"latency_ms", time.Since(start).Milliseconds(),
		)
	})
}
