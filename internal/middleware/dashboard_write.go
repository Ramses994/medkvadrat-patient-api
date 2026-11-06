package middleware

import (
	"net/http"
	"net/url"
	"strings"
)

// RequireSameOrigin rejects a mutating dashboard request whose Origin or Referer host is not this host.
func RequireSameOrigin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		staffNoStore(w)
		if !sameDashboardHost(r) {
			staffError(w, http.StatusForbidden, "FORBIDDEN", "Чужой Origin")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// RequireDashboardMutation checks Origin or Referer and X-Dashboard-Request: 1.
func RequireDashboardMutation(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		staffNoStore(w)
		if r.Header.Get("X-Dashboard-Request") != "1" {
			staffError(w, http.StatusForbidden, "FORBIDDEN", "Некорректный запрос дашборда")
			return
		}
		if !sameDashboardHost(r) {
			staffError(w, http.StatusForbidden, "FORBIDDEN", "Чужой Origin")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func sameDashboardHost(r *http.Request) bool {
	raw := strings.TrimSpace(r.Header.Get("Origin"))
	if raw == "" {
		raw = strings.TrimSpace(r.Header.Get("Referer"))
	}
	if raw == "" {
		return false
	}
	u, err := url.Parse(raw)
	return err == nil && u.Host != "" && strings.EqualFold(u.Host, r.Host)
}
