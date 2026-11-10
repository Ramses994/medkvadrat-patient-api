package service

import (
	"context"
	"net/http"
	"testing"

	"github.com/medkvadrat/medkvadrat-patient-api/internal/booking"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/writeops"
)

// Tests for audit 19 of PR #23 (bc461af).

// B4.4: the in_flight journal row exists before the final snapshot is read.
func TestFixes7_InFlightBeforeFinalSnapshot(t *testing.T) {
	e, c, p, _ := testEngine(t)
	ctx := context.Background()
	key := "a1a1a1a1-a1a1-4a1a-8a1a-a1a1a1a1a1a1"
	cancelOwner(c)
	c.adm = oneAdmission()
	c.snaps = []booking.Snap{oneSnapSvc(), oneSnapSvc(), oneCancelledSvc()}
	seen := map[int]string{}
	c.onSnap = func(n int) {
		op, _ := e.Ops.Find(ctx, 1, writeops.ActionCancel, key)
		if op == nil {
			seen[n] = "none"
		} else {
			seen[n] = op.State
		}
	}
	p.record = false
	if out := e.Cancel(ctx, 1, 5, key); out.Status != http.StatusOK {
		t.Fatalf("cancel %d %s", out.Status, out.Code)
	}
	if seen[1] != "none" || seen[2] != writeops.StateInFlight {
		t.Fatalf("journal at snapshot reads: %v", seen)
	}
}

// B4.1: the visit moves to another patient / forbidden line in the final read —
// no call, the operation is closed as rejected and the patient is not blocked.
func TestFixes7_FinalSnapshotChangeRejectsWithoutCall(t *testing.T) {
	for i, tc := range []struct {
		name   string
		mutate func(*booking.Snap)
		status int
	}{
		{"patient", func(s *booking.Snap) { s.PlanningPatientID = 2 }, http.StatusNotFound},
		{"line", func(s *booking.Snap) { s.PlanningPlSubjID = 999999 }, http.StatusConflict},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, c, p, _ := testEngine(t)
			ctx := context.Background()
			key := []string{"a2a2a2a2-a2a2-4a2a-8a2a-a2a2a2a2a201", "a2a2a2a2-a2a2-4a2a-8a2a-a2a2a2a2a202"}[i]
			cancelOwner(c)
			c.adm = oneAdmission()
			last := oneSnapSvc()
			tc.mutate(&last)
			c.snaps = []booking.Snap{oneSnapSvc(), last}
			p.record = false
			out := e.Cancel(ctx, 1, 5, key)
			if out.Status != tc.status || p.calls.Load() != 0 {
				t.Fatalf("status=%d code=%s calls=%d", out.Status, out.Code, p.calls.Load())
			}
			op, _ := e.Ops.Find(ctx, 1, writeops.ActionCancel, key)
			if op == nil || op.State != writeops.StateRejected {
				t.Fatalf("journal %+v", op)
			}
			if open, err := e.Ops.HasOpen(ctx, 1); err != nil || open {
				t.Fatalf("patient blocked: open=%v err=%v", open, err)
			}
		})
	}
}

// B4.2: someone else's visit is the same 404 whether it is active or cancelled,
// in the first or in the final snapshot — never ALREADY_CANCELLED.
func TestFixes7_ForeignVisitIsAlways404(t *testing.T) {
	type snaps func() []booking.Snap
	foreign := func(cancelled int) booking.Snap {
		s := oneSnapSvc()
		s.PlanningPatientID = 2
		s.PlanningCancelled = cancelled
		return s
	}
	cases := []struct {
		name  string
		snaps snaps
	}{
		{"first active", func() []booking.Snap { return []booking.Snap{foreign(0)} }},
		{"first cancelled", func() []booking.Snap { return []booking.Snap{foreign(1)} }},
		{"final active", func() []booking.Snap { return []booking.Snap{oneSnapSvc(), foreign(0)} }},
		{"final cancelled", func() []booking.Snap { return []booking.Snap{oneSnapSvc(), foreign(1)} }},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e, c, p, _ := testEngine(t)
			cancelOwner(c)
			c.adm = oneAdmission()
			c.snaps = tc.snaps()
			p.record = false
			key := []string{"a3a3a3a3-a3a3-4a3a-8a3a-a3a3a3a3a301", "a3a3a3a3-a3a3-4a3a-8a3a-a3a3a3a3a302", "a3a3a3a3-a3a3-4a3a-8a3a-a3a3a3a3a303", "a3a3a3a3-a3a3-4a3a-8a3a-a3a3a3a3a304"}[i]
			out := e.Cancel(context.Background(), 1, 5, key)
			if out.Status != http.StatusNotFound || out.Code != "NOT_FOUND" || p.calls.Load() != 0 {
				t.Fatalf("status=%d code=%s calls=%d", out.Status, out.Code, p.calls.Load())
			}
		})
	}
}

// B4.2: the patient's own already-cancelled visit still answers ALREADY_CANCELLED.
func TestFixes7_OwnCancelledVisitIsAlreadyCancelled(t *testing.T) {
	e, c, p, _ := testEngine(t)
	cancelOwner(c)
	c.adm = oneAdmission()
	cancelled := oneSnapSvc()
	cancelled.PlanningCancelled = 1
	c.snaps = []booking.Snap{oneSnapSvc(), cancelled}
	p.record = false
	out := e.Cancel(context.Background(), 1, 5, "a4a4a4a4-a4a4-4a4a-8a4a-a4a4a4a4a4a4")
	if out.Status != http.StatusConflict || out.Code != "ALREADY_CANCELLED" || p.calls.Load() != 0 {
		t.Fatalf("status=%d code=%s calls=%d", out.Status, out.Code, p.calls.Load())
	}
}
