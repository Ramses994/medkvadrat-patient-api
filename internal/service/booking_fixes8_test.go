package service

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/medkvadrat/medkvadrat-patient-api/internal/booking"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/writeops"
)

// Tests for audit 20 of PR #23 (637c888).

func billAdmission() booking.Admission {
	a := oneAdmission()
	a.Answers[0].BillNull = false
	return a
}

// B4.2: when Admit refuses, the owner from PLANNING is still checked first —
// someone else's visit is 404 in the first and in the final snapshot; the
// patient's own cancelled visit stays ALREADY_CANCELLED.
func TestFixes8_AdmitRefusalChecksOwnerFirst(t *testing.T) {
	foreign := func(cancelled int) booking.Snap {
		s := oneSnapSvc()
		s.PlanningPatientID = 2
		s.PlanningCancelled = cancelled
		return s
	}
	own := func(cancelled int) booking.Snap {
		s := oneSnapSvc()
		s.PlanningCancelled = cancelled
		return s
	}
	cases := []struct {
		name    string
		adm     booking.Admission
		admNext *booking.Admission
		snaps   []booking.Snap
		status  int
		code    string
		journal string // "" = no journal row expected
	}{
		{"first: foreign with bill", billAdmission(), nil, []booking.Snap{foreign(0)}, 404, "NOT_FOUND", ""},
		{"first: foreign cancelled", billAdmission(), nil, []booking.Snap{foreign(1)}, 404, "NOT_FOUND", ""},
		{"first: own cancelled", billAdmission(), nil, []booking.Snap{own(1)}, 409, "ALREADY_CANCELLED", ""},
		{"first: own with bill", billAdmission(), nil, []booking.Snap{own(0)}, 409, "CANCEL_NOT_SUPPORTED", ""},
		{"final: foreign with bill", oneAdmission(), ptrAdm(billAdmission()), []booking.Snap{oneSnapSvc(), foreign(0)}, 404, "NOT_FOUND", writeops.StateRejected},
		{"final: foreign cancelled", oneAdmission(), ptrAdm(billAdmission()), []booking.Snap{oneSnapSvc(), foreign(1)}, 404, "NOT_FOUND", writeops.StateRejected},
		{"final: own cancelled", oneAdmission(), ptrAdm(billAdmission()), []booking.Snap{oneSnapSvc(), own(1)}, 409, "ALREADY_CANCELLED", writeops.StateRejected},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e, c, p, _ := testEngine(t)
			ctx := context.Background()
			cancelOwner(c)
			c.adm = tc.adm
			c.admNext = tc.admNext
			c.snaps = tc.snaps
			p.record = false
			key := []string{
				"b1b1b1b1-b1b1-4b1b-8b1b-b1b1b1b1b101", "b1b1b1b1-b1b1-4b1b-8b1b-b1b1b1b1b102", "b1b1b1b1-b1b1-4b1b-8b1b-b1b1b1b1b103",
				"b1b1b1b1-b1b1-4b1b-8b1b-b1b1b1b1b104", "b1b1b1b1-b1b1-4b1b-8b1b-b1b1b1b1b105", "b1b1b1b1-b1b1-4b1b-8b1b-b1b1b1b1b106",
				"b1b1b1b1-b1b1-4b1b-8b1b-b1b1b1b1b107",
			}[i]
			out := e.Cancel(ctx, 1, 5, key)
			if out.Status != tc.status || out.Code != tc.code || p.calls.Load() != 0 {
				t.Fatalf("status=%d code=%s calls=%d", out.Status, out.Code, p.calls.Load())
			}
			op, _ := e.Ops.Find(ctx, 1, writeops.ActionCancel, key)
			if tc.journal == "" && op != nil {
				t.Fatalf("unexpected journal row %+v", op)
			}
			if tc.journal != "" && (op == nil || op.State != tc.journal) {
				t.Fatalf("journal %+v", op)
			}
		})
	}
}

func ptrAdm(a booking.Admission) *booking.Admission { return &a }

// B4.4: the time limit passes before the final snapshot — 422 CANCEL_TOO_LATE,
// no call, the operation is closed as rejected.
func TestFixes8_TimeLimitCheckedOnFinalSnapshot(t *testing.T) {
	e, c, p, _ := testEngine(t)
	ctx := context.Background()
	clock := time.Date(2026, 11, 6, 12, 0, 0, 0, time.FixedZone("MSK", 3*3600))
	e.Now = func() time.Time { return clock }
	cancelOwner(c) // visit at 2026-11-08T10:00, limit 24 h
	c.adm = oneAdmission()
	c.snaps = []booking.Snap{oneSnapSvc(), oneSnapSvc(), oneCancelledSvc()}
	c.onSnap = func(n int) {
		if n == 2 { // just before the final snapshot: less than 24 h left
			clock = time.Date(2026, 11, 7, 10, 30, 0, 0, time.FixedZone("MSK", 3*3600))
		}
	}
	p.record = false
	key := "b2b2b2b2-b2b2-4b2b-8b2b-b2b2b2b2b2b2"
	out := e.Cancel(ctx, 1, 5, key)
	if out.Status != http.StatusUnprocessableEntity || out.Code != "CANCEL_TOO_LATE" || p.calls.Load() != 0 {
		t.Fatalf("status=%d code=%s calls=%d", out.Status, out.Code, p.calls.Load())
	}
	if op, _ := e.Ops.Find(ctx, 1, writeops.ActionCancel, key); op == nil || op.State != writeops.StateRejected {
		t.Fatalf("journal %+v", op)
	}
}

// Replay: a cancel rejected as NOT_FOUND replays the same 404 with the same
// message, even if the visit belongs to the patient again; no call.
func TestFixes8_RejectedNotFoundReplays404(t *testing.T) {
	e, c, p, _ := testEngine(t)
	ctx := context.Background()
	cancelOwner(c)
	c.adm = oneAdmission()
	foreign := oneSnapSvc()
	foreign.PlanningPatientID = 2
	c.snaps = []booking.Snap{oneSnapSvc(), foreign}
	p.record = false
	key := "b3b3b3b3-b3b3-4b3b-8b3b-b3b3b3b3b3b3"
	first := e.Cancel(ctx, 1, 5, key)
	if first.Status != http.StatusNotFound || first.Code != "NOT_FOUND" {
		t.Fatalf("first %d %s", first.Status, first.Code)
	}
	c.snaps = []booking.Snap{oneSnapSvc()} // the visit is the patient's again
	again := e.Cancel(ctx, 1, 5, key)
	if again.Status != http.StatusNotFound || again.Code != "NOT_FOUND" || again.Message != first.Message || p.calls.Load() != 0 {
		t.Fatalf("replay %d %s %q (first %q) calls=%d", again.Status, again.Code, again.Message, first.Message, p.calls.Load())
	}
}
