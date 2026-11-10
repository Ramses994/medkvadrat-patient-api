package service

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/medkvadrat/medkvadrat-patient-api/internal/booking"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/repo"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/writeops"
)

// Tests for audit 18 of PR #23 (e27de18): owner and line in the last snapshot.

// B4: after the third Owner read, the visit moves to another patient or line
// before the final snapshot — no procedure call.
func TestFixes6_LastSnapshotOwnerAndLine(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*booking.Snap)
		status int
	}{
		{"other patient", func(s *booking.Snap) { s.PlanningPatientID = 2 }, http.StatusNotFound},
		{"forbidden line", func(s *booking.Snap) { s.PlanningPlSubjID = 999999 }, http.StatusConflict},
		{"other allowed line", func(s *booking.Snap) { s.PlanningPlSubjID = 1120 }, http.StatusConflict},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e, c, p, _ := testEngine(t)
			cancelOwner(c)
			c.adm = oneAdmission()
			first, last := oneSnapSvc(), oneSnapSvc()
			tc.mutate(&first) // both pre-call reads agree on the moved visit
			tc.mutate(&last)
			c.snaps = []booking.Snap{first, last}
			p.record = false
			key := []string{"91919191-9191-4191-8191-919191919101", "91919191-9191-4191-8191-919191919102", "91919191-9191-4191-8191-919191919103"}[i]
			out := e.Cancel(context.Background(), 1, 5, key)
			if out.Status != tc.status || p.calls.Load() != 0 {
				t.Fatalf("status=%d code=%s calls=%d", out.Status, out.Code, p.calls.Load())
			}
		})
	}
}

// B4: only the last snapshot moved (first read was clean) — refused too.
func TestFixes6_OwnerChangesBetweenSnapshots(t *testing.T) {
	e, c, p, _ := testEngine(t)
	cancelOwner(c)
	c.adm = oneAdmission()
	last := oneSnapSvc()
	last.PlanningPatientID = 2
	c.snaps = []booking.Snap{oneSnapSvc(), last}
	p.record = false
	out := e.Cancel(context.Background(), 1, 5, "92929292-9292-4292-8292-929292929292")
	if out.Status == http.StatusOK || p.calls.Load() != 0 {
		t.Fatalf("status=%d code=%s calls=%d", out.Status, out.Code, p.calls.Load())
	}
}

func unknownCancelOp(t *testing.T, e *BookingEngine, c *fakeClinic, p *fakeProcs, key string) *writeops.Op {
	t.Helper()
	cancelOwner(c)
	c.adm = oneAdmission()
	c.snaps = []booking.Snap{oneSnapSvc(), oneSnapSvc()}
	p.record = false
	p.next = repo.ProcResult{Sent: true, Finished: false, Err: errors.New("mssql: error after send")}
	if out := e.Cancel(context.Background(), 1, 5, key); out.Code != "CANCEL_UNKNOWN" {
		t.Fatalf("cancel %d %s", out.Status, out.Code)
	}
	op, _ := e.Ops.Find(context.Background(), 1, writeops.ActionCancel, key)
	return op
}

// B2: manual_ok refuses a cancelled visit that now belongs to another patient/line.
func TestFixes6_ManualOKRefusedWhenOwnerOrLineChanged(t *testing.T) {
	for i, tc := range []struct {
		name   string
		mutate func(*booking.Snap)
	}{
		{"patient", func(s *booking.Snap) { s.PlanningPatientID = 2 }},
		{"line", func(s *booking.Snap) { s.PlanningPlSubjID = 999999 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, c, p, _ := testEngine(t)
			ctx := context.Background()
			op := unknownCancelOp(t, e, c, p, []string{"93939393-9393-4393-8393-939393939301", "93939393-9393-4393-8393-939393939302"}[i])
			moved := oneCancelledSvc()
			tc.mutate(&moved)
			c.snaps = []booking.Snap{moved}
			out := e.Resolve(ctx, ResolveInput{OpID: op.OpID, State: writeops.StateManualOK, Note: "other", Actor: "s2s"})
			if out.Status != http.StatusUnprocessableEntity {
				t.Fatalf("manual ok accepted: %d %s", out.Status, out.Code)
			}
			if got, _ := e.Ops.Get(ctx, op.OpID); got.State != writeops.StateUnknown {
				t.Fatalf("journal %+v", got)
			}
		})
	}
}

// B2: the background reconcile never marks such a cancel ok.
func TestFixes6_ReconcileNotOKWhenOwnerChanged(t *testing.T) {
	e, c, p, _ := testEngine(t)
	ctx := context.Background()
	base := e.now()
	op := unknownCancelOp(t, e, c, p, "94949494-9494-4494-8494-949494949494")
	moved := oneCancelledSvc()
	moved.PlanningPatientID = 2
	c.snaps = []booking.Snap{moved}
	e.Now = func() time.Time { return base.Add(5 * time.Second) }
	e.reconcileDue(ctx)
	if got, _ := e.Ops.Get(ctx, op.OpID); got.State == writeops.StateOK {
		t.Fatalf("reconciled ok: %+v", got)
	}
}
