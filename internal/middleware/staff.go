package middleware

import (
	"net/http"
	"strings"
	"time"

	"github.com/medkvadrat/medkvadrat-patient-api/internal/response"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/staffsession"
)

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
		c, err := r.Cookie(staffsession.CookieName)
		ok := err == nil && c != nil && staffsession.Valid(s.Secret, c.Value, s.now())
		if !ok {
			if strings.HasPrefix(r.URL.Path, "/dashboard/data") {
				response.Error(w, http.StatusUnauthorized, "UNAUTHORIZED", "Требуется вход")
				return
			}
			http.Redirect(w, r, "/dashboard/login", http.StatusFound)
			return
		}
		next.ServeHTTP(w, r)
	})
}
