package middleware

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/medkvadrat/medkvadrat-patient-api/internal/response"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/staff"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/staffsession"
)

type staffCtxKey struct{}

func StaffUpdatedBy(r *http.Request) string {
	if v, ok := r.Context().Value(staffCtxKey{}).(string); ok && v != "" {
		return v
	}
	return "staff"
}

func wantsDashboardJSON(r *http.Request) bool {
	p := r.URL.Path
	if strings.HasPrefix(p, "/dashboard/data") || strings.HasPrefix(p, "/dashboard/waitlist") || strings.HasPrefix(p, "/dashboard/staff") || p == "/dashboard/me" {
		return true
	}
	return r.Method == http.MethodPost && p == "/dashboard/password"
}

func passwordChangeExempt(path string) bool {
	return path == "/dashboard/password" || path == "/dashboard/logout"
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
	Lookup func(ctx context.Context, userID int64) (staff.Principal, error)
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
		var userID int64
		var version int
		ok := false
		if err == nil && c != nil {
			userID, version, ok, _ = staffsession.Open(s.Secret, c.Value, s.now())
		}
		if !ok {
			if wantsDashboardJSON(r) {
				staffError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Требуется вход")
				return
			}
			staffNoStore(w)
			http.Redirect(w, r, "/dashboard/login", http.StatusFound)
			return
		}
		principal := staff.Principal{ID: userID, Login: "staff", All: true, BranchIDs: append([]int(nil), staff.DashboardBranchIDs...), SessionVersion: version}
		if s.Lookup != nil {
			principal, err = s.Lookup(r.Context(), userID)
			if err != nil || principal.SessionVersion != version {
				if wantsDashboardJSON(r) {
					staffError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Требуется вход")
					return
				}
				staffNoStore(w)
				http.Redirect(w, r, "/dashboard/login", http.StatusFound)
				return
			}
		}
		if principal.MustChangePassword && !passwordChangeExempt(r.URL.Path) {
			if wantsDashboardJSON(r) {
				staffError(w, http.StatusForbidden, "PASSWORD_CHANGE_REQUIRED", "Смените пароль")
				return
			}
			staffNoStore(w)
			http.Redirect(w, r, "/dashboard/password", http.StatusFound)
			return
		}
		ctx := staff.WithPrincipal(r.Context(), principal)
		ctx = context.WithValue(ctx, staffCtxKey{}, principal.Login)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
