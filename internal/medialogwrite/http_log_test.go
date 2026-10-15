package medialogwrite

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/medkvadrat/medkvadrat-patient-api/internal/middleware"
	_ "modernc.org/sqlite"
)

func TestGuardBook_WriteLogUnavailable(t *testing.T) {
	dir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dir, "log.db"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`
CREATE TABLE medialog_write_log (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  request_id TEXT,
  route TEXT NOT NULL,
  patient_id INTEGER,
  planning_id INTEGER,
  pl_subj_id INTEGER,
  decision TEXT NOT NULL,
  result TEXT,
  error_code TEXT
)`)
	if err != nil {
		t.Fatal(err)
	}
	_ = db.Close()

	deps := HTTPDeps{
		Guard:  NewGuard(true, true, []int64{1}, []int64{10}),
		Lookup: stubPlanningLookup{plSubj: 10},
		Log:    NewLogRepo(db),
	}
	r := httptest.NewRequest(http.MethodPost, "/api/me/appointments", nil)
	ctx := context.WithValue(r.Context(), middleware.RequestIDKey, "req-log")
	r = r.WithContext(ctx)
	w := httptest.NewRecorder()

	svcCalled := false
	_, ok := deps.GuardBook(ctx, w, r, "POST /api/me/appointments", 1, 100)
	if ok {
		svcCalled = true
	}
	if svcCalled {
		t.Fatal("must not proceed when log unavailable")
	}
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d", w.Code)
	}
	var body struct {
		ErrorDetails struct {
			Code string `json:"code"`
		} `json:"error_details"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if body.ErrorDetails.Code != "WRITE_LOG_UNAVAILABLE" {
		t.Fatalf("code=%q", body.ErrorDetails.Code)
	}
}

type blockingLookup struct{}

func (blockingLookup) PlSubjIDForPlanning(ctx context.Context, _ int64) (int64, error) {
	<-ctx.Done()
	return 0, ctx.Err()
}

func (blockingLookup) PlanningIDForMotconsu(context.Context, int64, int64) (int64, error) {
	return 0, nil
}

func TestGuardBook_LookupTimeout(t *testing.T) {
	deps := HTTPDeps{
		Guard:  NewGuard(true, true, []int64{1}, []int64{10}),
		Lookup: blockingLookup{},
	}
	r := httptest.NewRequest(http.MethodPost, "/api/me/appointments", nil)
	ctx := context.WithValue(r.Context(), middleware.RequestIDKey, "req-timeout")
	r = r.WithContext(ctx)
	w := httptest.NewRecorder()

	start := time.Now()
	_, ok := deps.GuardBook(ctx, w, r, "POST /api/me/appointments", 1, 100)
	if ok {
		t.Fatal("expected timeout deny")
	}
	if time.Since(start) < MedialogQueryTimeout/2 {
		t.Fatalf("finished too fast: %v", time.Since(start))
	}
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d", w.Code)
	}
}

func TestGuardCancel_NoPlanning(t *testing.T) {
	deps := HTTPDeps{
		Guard:  NewGuard(true, true, []int64{1}, []int64{10}),
		Lookup: stubPlanningLookup{planningErr: ErrNoPlanning},
	}
	r := httptest.NewRequest(http.MethodDelete, "/api/me/appointments/5", nil)
	ctx := context.WithValue(r.Context(), middleware.RequestIDKey, "req-noplan")
	r = r.WithContext(ctx)
	w := httptest.NewRecorder()

	_, ok := deps.GuardCancel(ctx, w, r, "DELETE /api/me/appointments/{motconsu_id}", 1, 5)
	if ok {
		t.Fatal("expected deny")
	}
	if w.Code != http.StatusConflict {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}
