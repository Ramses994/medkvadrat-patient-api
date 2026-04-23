package middleware

import (
	"net/http"
	"strings"

	"github.com/medkvadrat/medkvadrat-patient-api/internal/response"
)

type Auth struct {
	Token string
}

func (a Auth) RequireBearer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// In dev mode token can be empty: keep behavior (no auth).
		if strings.TrimSpace(a.Token) == "" {
			next.ServeHTTP(w, r)
			return
		}
		h := r.Header.Get("Authorization")
		if h != "Bearer "+a.Token {
			response.Error(w, http.StatusUnauthorized, "UNAUTHORIZED", "Unauthorized")
			return
		}
		next.ServeHTTP(w, r)
	})
}
