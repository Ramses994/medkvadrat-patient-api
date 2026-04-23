package middleware

import (
	"context"
	"net/http"
	"strings"

	"github.com/medkvadrat/medkvadrat-patient-api/internal/auth"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/response"
)

type ctxKeyPatientID struct{}

func PatientIDFromContext(ctx context.Context) (int64, bool) {
	v := ctx.Value(ctxKeyPatientID{})
	id, ok := v.(int64)
	return id, ok && id > 0
}

func WithPatientID(ctx context.Context, patientID int64) context.Context {
	return context.WithValue(ctx, ctxKeyPatientID{}, patientID)
}

type RequirePatient struct {
	JWTSecret []byte
}

func (m RequirePatient) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := strings.TrimSpace(r.Header.Get("Authorization"))
		const pfx = "Bearer "
		if !strings.HasPrefix(h, pfx) {
			response.Error(w, http.StatusUnauthorized, "UNAUTHORIZED", "Unauthorized")
			return
		}
		tok := strings.TrimSpace(strings.TrimPrefix(h, pfx))
		if tok == "" {
			response.Error(w, http.StatusUnauthorized, "UNAUTHORIZED", "Unauthorized")
			return
		}
		claims, err := auth.ParseAccess(m.JWTSecret, tok)
		if err != nil {
			response.Error(w, http.StatusUnauthorized, "UNAUTHORIZED", "Unauthorized")
			return
		}
		ctx := context.WithValue(r.Context(), ctxKeyPatientID{}, claims.Sub)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
