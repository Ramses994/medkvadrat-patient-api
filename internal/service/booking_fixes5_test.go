package service

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/medkvadrat/medkvadrat-patient-api/internal/booking"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/config"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/repo"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/writeops"
)

// Tests for audit 17 of PR #23 (fcc2f83).

// 2a: the answer moved to visit 6 between the reads — refused before the call.
func TestFixes5_CancelAnswerMovedToAnotherVisitRefused(t *testing.T) {
	e, c, p, _ := testEngine(t)
	cancelOwner(c)
	c.adm = oneAdmission()
	moved := oneSnapSvc()
	moved.AnswPlanning = i64(6)
	c.snaps = []booking.Snap{oneSnapSvc(), moved}
	p.record = false
	out := e.Cancel(context.Background(), 1, 5, "81818181-8181-4181-8181-818181818181")
	if out.Status != http.StatusConflict || out.Code != "CANCEL_NOT_SUPPORTED" || p.calls.Load() != 0 {
		t.Fatalf("status=%d code=%s calls=%d", out.Status, out.Code, p.calls.Load())
	}
}

// 2b: after a finished call the answer points at another direction — not 200.
func TestFixes5_CancelRelinkAfterCallIsNotOK(t *testing.T) {
	e, c, p, _ := testEngine(t)
	ctx := context.Background()
	cancelOwner(c)
	c.adm = oneAdmission()
	after := oneCancelledSvc()
	after.AnswPatdirec = i64(33)
	c.snaps = []booking.Snap{oneSnapSvc(), oneSnapSvc(), after}
	p.record = false
	p.next = repo.ProcResult{Sent: true, Finished: true, HasRow: true}
	key := "82828282-8282-4282-8282-828282828282"
	out := e.Cancel(ctx, 1, 5, key)
	if out.Status == http.StatusOK || p.calls.Load() != 1 {
		t.Fatalf("status=%d code=%s calls=%d", out.Status, out.Code, p.calls.Load())
	}
	op, _ := e.Ops.Find(ctx, 1, writeops.ActionCancel, key)
	if op == nil || op.State == writeops.StateOK {
		t.Fatalf("journal %+v", op)
	}
}

// 2b: manual_ok for a cancel is refused when the answer was re-linked.
func TestFixes5_ManualCancelOKRefusedOnRelink(t *testing.T) {
	e, c, p, _ := testEngine(t)
	ctx := context.Background()
	cancelOwner(c)
	c.adm = oneAdmission()
	c.snaps = []booking.Snap{oneSnapSvc(), oneSnapSvc()}
	p.record = false
	p.next = repo.ProcResult{Sent: true, Finished: false, Err: errors.New("mssql: error after send")}
	key := "83838383-8383-4383-8383-838383838383"
	if out := e.Cancel(ctx, 1, 5, key); out.Code != "CANCEL_UNKNOWN" {
		t.Fatalf("cancel %d %s", out.Status, out.Code)
	}
	relinked := oneCancelledSvc()
	relinked.AnswPatdirec = i64(33)
	c.snaps = []booking.Snap{relinked}
	op, _ := e.Ops.Find(ctx, 1, writeops.ActionCancel, key)
	out := e.Resolve(ctx, ResolveInput{OpID: op.OpID, State: writeops.StateManualOK, Note: "other", Actor: "s2s"})
	if out.Status != http.StatusUnprocessableEntity {
		t.Fatalf("manual ok accepted: %d %s", out.Status, out.Code)
	}
	got, _ := e.Ops.Get(ctx, op.OpID)
	if got.State != writeops.StateUnknown {
		t.Fatalf("journal %+v", got)
	}
}

// 3: finished_at is the real finish moment; the reconcile waits 2 s from it,
// through the production path (reconcileDue), not Sweep.
func TestFixes5_ReconcileWaitsTwoSecondsAfterRealFinish(t *testing.T) {
	e, c, p, _ := testEngine(t)
	ctx := context.Background()
	base := e.now()
	clock := base
	e.Now = func() time.Time { return clock }
	cancelOwner(c)
	c.adm = oneAdmission()
	c.snaps = []booking.Snap{oneSnapSvc(), oneSnapSvc(), oneCancelledSvc()}
	p.record = false
	p.next = repo.ProcResult{Sent: true, Finished: false, Err: errors.New("mssql: error after send")}
	e.AfterProc = func() error { clock = base.Add(10 * time.Second); return nil } // the batch took 10 s
	key := "84848484-8484-4484-8484-848484848484"
	if out := e.Cancel(ctx, 1, 5, key); out.Code != "CANCEL_UNKNOWN" {
		t.Fatalf("cancel %d %s", out.Status, out.Code)
	}
	op, _ := e.Ops.Find(ctx, 1, writeops.ActionCancel, key)
	fin, err := time.Parse(time.RFC3339Nano, op.FinishedAt.String)
	if err != nil || !fin.Equal(base.Add(10*time.Second)) {
		t.Fatalf("finished_at %q err=%v", op.FinishedAt.String, err)
	}
	clock = base.Add(11 * time.Second)
	e.reconcileDue(ctx)
	if op, _ = e.Ops.Get(ctx, op.OpID); op.State != writeops.StateUnknown {
		t.Fatalf("reconciled 1 s after finish: %s", op.State)
	}
	clock = base.Add(13 * time.Second)
	e.reconcileDue(ctx)
	if op, _ = e.Ops.Get(ctx, op.OpID); op.State != writeops.StateOK {
		t.Fatalf("not reconciled 3 s after finish: %s", op.State)
	}
	if p.calls.Load() != 1 {
		t.Fatalf("calls=%d", p.calls.Load())
	}
}

// 4a: the visit disappears on the third owner read — 404, not 409.
func TestFixes5_CancelGoneOnThirdReadIs404(t *testing.T) {
	e, c, p, _ := testEngine(t)
	row := repo.OwnerRow{Found: true, PatientID: 1, PlSubjID: 1119, OrgID: 496, DateStart: "2026-11-08T10:00:00"}
	c.ownerSeq = []repo.OwnerRow{row, row, {}}
	p.record = false
	out := e.Cancel(context.Background(), 1, 5, "85858585-8585-4585-8585-858585858585")
	if out.Status != http.StatusNotFound || p.calls.Load() != 0 {
		t.Fatalf("status=%d code=%s calls=%d", out.Status, out.Code, p.calls.Load())
	}
}

// 4b: a pair whose configuration could not be read is 503, not 404.
func TestFixes5_BookPairReadFailureIs503(t *testing.T) {
	e, c, p, _ := testEngine(t)
	c.pairErr = repo.ErrPairUnavailable
	out := e.Book(context.Background(), 1, tok(t, 1, 1119, "2026-11-08T10:00:00", e.now()), "86868686-8686-4686-8686-868686868686")
	if out.Status != http.StatusServiceUnavailable || p.calls.Load() != 0 {
		t.Fatalf("status=%d code=%s calls=%d", out.Status, out.Code, p.calls.Load())
	}
	c.pairErr = repo.ErrInactivePair
	out = e.Book(context.Background(), 1, tok(t, 1, 1119, "2026-11-08T10:00:00", e.now()), "86868686-8686-4686-8686-868686868687")
	if out.Status != http.StatusNotFound {
		t.Fatalf("inactive status=%d", out.Status)
	}
}

// 4b: the services layer keeps "failed to load" apart from "not offered".
func TestFixes5_ActivePairDistinguishesFailedFromInactive(t *testing.T) {
	s := &Services{Config: config.Config{}}
	st := s.booking()
	st.active = map[string]repo.BookingOption{pairKey(1119, 10000688): {PlSubjID: 1119, PlExamID: 10000688}}
	st.failed = map[string]bool{pairKey(1120, 10000688): true}
	st.pairsAt = time.Now()
	if _, err := s.activePair(context.Background(), 1119, 10000688); err != nil {
		t.Fatalf("active: %v", err)
	}
	if _, err := s.activePair(context.Background(), 1120, 10000688); !errors.Is(err, repo.ErrPairUnavailable) {
		t.Fatalf("failed: %v", err)
	}
	if _, err := s.activePair(context.Background(), 2000, 10000688); !errors.Is(err, repo.ErrInactivePair) {
		t.Fatalf("absent: %v", err)
	}
}
