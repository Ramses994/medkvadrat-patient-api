package middleware

import (
	"net/http"
	"net/url"
	"strings"
)

// RequireDashboardMutation checks Origin against this host and X-Dashboard-Request: 1.
func RequireDashboardMutation(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		staffNoStore(w)
		if r.Header.Get("X-Dashboard-Request") != "1" {
			staffError(w, http.StatusForbidden, "FORBIDDEN", "Некорректный запрос дашборда")
			return
		}
		origin := strings.TrimSpace(r.Header.Get("Origin"))
		if origin == "" {
			staffError(w, http.StatusForbidden, "FORBIDDEN", "Origin обязателен")
			return
		}
		u, err := url.Parse(origin)
		if err != nil || u.Host == "" || !strings.EqualFold(u.Host, r.Host) {
			staffError(w, http.StatusForbidden, "FORBIDDEN", "Чужой Origin")
			return
		}
		next.ServeHTTP(w, r)
	})
}
