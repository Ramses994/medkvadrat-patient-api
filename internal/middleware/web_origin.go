package middleware

import (
	"net/http"
	"strings"

	"github.com/medkvadrat/medkvadrat-patient-api/internal/config"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/response"
)

// RequireWebOrigin accepts web-cabinet requests whose Origin exactly matches one of allowed
// (scheme://host[:port]). Referer, Host and X-Forwarded-Proto are not used. Empty allowlist → 403.
func RequireWebOrigin(allowed []string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		origin := strings.TrimSpace(r.Header.Get("Origin"))
		if !config.OriginAllowed(origin, allowed) {
			response.Error(w, http.StatusForbidden, "FORBIDDEN", "Чужой Origin")
			return
		}
		next.ServeHTTP(w, r)
	})
}
