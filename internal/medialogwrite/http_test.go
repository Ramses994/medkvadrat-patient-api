package medialogwrite

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/medkvadrat/medkvadrat-patient-api/internal/middleware"
)

type stubPlanningLookup struct {
	plSubj      int64
	plSubjErr   error
	planningID  int64
	planningErr error
}

func (s stubPlanningLookup) PlSubjIDForPlanning(context.Context, int64) (int64, error) {
	return s.plSubj, s.plSubjErr
}

func (s stubPlanningLookup) PlanningIDForMotconsu(context.Context, int64, int64) (int64, error) {
	return s.planningID, s.planningErr
}

func TestGuardBook_CheckOrder(t *testing.T) {
	tests := []struct {
		name       string
		guard      Guard
		wantCode   string
		wantStatus int
	}{
		{
			name:       "write disabled before rw missing",
			guard:      NewGuard(false, false, []int64{1}, []int64{10}),
			wantCode:   "WRITE_DISABLED",
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "rw not configured after allowlists pass",
			guard:      NewGuard(true, false, []int64{1}, []int64{10}),
			wantCode:   "WRITE_NOT_CONFIGURED",
			wantStatus: http.StatusServiceUnavailable,
		},
		{
			name:       "patient not allowed",
			guard:      NewGuard(true, true, nil, []int64{10}),
			wantCode:   "WRITE_NOT_ALLOWED",
			wantStatus: http.StatusForbidden,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			deps := HTTPDeps{
				Guard:  tc.guard,
				Lookup: stubPlanningLookup{plSubj: 10},
			}
			r := httptest.NewRequest(http.MethodPost, "/api/me/appointments", nil)
			ctx := context.WithValue(r.Context(), middleware.RequestIDKey, "req-test")
			r = r.WithContext(ctx)
			w := httptest.NewRecorder()

			_, ok := deps.GuardBook(ctx, w, r, "POST /api/me/appointments", 1, 100)
			if ok {
				t.Fatal("expected guard deny")
			}
			if w.Code != tc.wantStatus {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
			var body struct {
				ErrorDetails struct {
					Code string `json:"code"`
				} `json:"error_details"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if body.ErrorDetails.Code != tc.wantCode {
				t.Fatalf("code=%q want %q", body.ErrorDetails.Code, tc.wantCode)
			}
		})
	}
}
