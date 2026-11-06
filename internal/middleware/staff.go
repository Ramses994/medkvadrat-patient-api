package middleware

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/medkvadrat/medkvadrat-patient-api/internal/response"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/staffsession"
)

type staffCtxKey struct{}

func StaffUpdatedBy(r *http.Request) string {
	if v, ok := r.Context().Value(staffCtxKey{}).(string); ok && v != "" {
		return v
	}
	return "staff"
}

func isDashboardJSON(path string) bool {
	return strings.HasPrefix(path, "/dashboard/data") || strings.HasPrefix(path, "/dashboard/waitlist")
}

func staffNoStore(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
}

func staffError(w http.ResponseWriter, status int, code, msg string) {
	staffNoStore(w)
	response.Error(w, status, code, msg)
}

// RequireDashboardConfig rejects every /dashboard* route before cookies when
// DASHBOARD_PASSWORD is empty or DASHBOARD_SECRET is shorter than 32 bytes.
type RequireDashboardConfig struct {
	Password string
	Secret   []byte
}

func (d RequireDashboardConfig) Ready() bool {
	return strings.TrimSpace(d.Password) != "" && !staffsession.WeakSecret(d.Secret)
}

func (d RequireDashboardConfig) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/dashboard") {
			next.ServeHTTP(w, r)
			return
		}
		if !d.Ready() {
			staffError(w, http.StatusServiceUnavailable, "DASHBOARD_NOT_CONFIGURED", "Дашборд не настроен")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// RequireStaff checks the mk_staff session cookie.
type RequireStaff struct {
	Secret []byte
	Now    func() time.Time
}

func (s RequireStaff) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s RequireStaff) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if staffsession.WeakSecret(s.Secret) {
			staffError(w, http.StatusServiceUnavailable, "DASHBOARD_NOT_CONFIGURED", "Дашборд не настроен")
			return
		}
		c, err := r.Cookie(staffsession.CookieName)
		ok := false
		if err == nil && c != nil {
			ok, _ = staffsession.Valid(s.Secret, c.Value, s.now())
		}
		if !ok {
			if isDashboardJSON(r.URL.Path) {
				staffError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Требуется вход")
				return
			}
			staffNoStore(w)
			http.Redirect(w, r, "/dashboard/login", http.StatusFound)
			return
		}
		ctx := context.WithValue(r.Context(), staffCtxKey{}, staffsession.UpdatedBy(c.Value))
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
