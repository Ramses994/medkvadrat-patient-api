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

// Tests for docs/lk/prompts/cursor-lk-2-fixes-4.md (audit 16).

func i64(v int64) *int64 { return &v }

// oneAdmission is a "one direction" visit 5: answer 7 -> direction 22 owned by visit 5.
func oneAdmission() booking.Admission {
	dir := booking.Direction{ID: 22, PlanningCR: i64(5)}
	return booking.Admission{
		Found: true, Active: true,
		Answers:      []booking.Answer{{ID: 7, PatdirecID: 22, HasPatdirec: true, BillNull: true}},
		Directions:   []booking.Direction{dir},
		OwnDirection: &dir,
	}
}

// oneSnapSvc is the snapshot LoadSnap returns for that visit, with the links read
// from DIR_ANSW.PATDIREC_ID and PATDIREC.PLANNING_CR_ID.
func oneSnapSvc() booking.Snap {
	return booking.Snap{
		ReadOK: true, PlanningFound: true,
		AnswExpected: true, AnswFound: true, AnswPlanning: i64(5), AnswPatdirec: i64(22),
		DirExpected: true, DirFound: true, DirPlanningCR: i64(5),
		A: []int64{7}, P: []int64{22},
	}
}

func oneCancelledSvc() booking.Snap {
	s := oneSnapSvc()
	s.PlanningCancelled = 1
	s.AnswState = 2
	s.DirCancelled = 1
	s.DirState = 3
	return s
}

func cancelOwner(c *fakeClinic) {
	c.owners[5] = repo.OwnerRow{Found: true, PatientID: 1, PlSubjID: 1119, OrgID: 496, DateStart: "2026-11-08T10:00:00"}
}

// 1. Manual resolve never stores an operator planning_id for a cancel.
func TestFixes4_ManualCancelRejectsPlanningID(t *testing.T) {
	e, _, p, _ := testEngine(t)
	ctx := context.Background()
	now := e.now()
	key := "71717171-7171-4171-8171-717171717171"
	op := e.newOp(1, writeops.ActionCancel, key, writeops.CancelIntent(5), `{}`, now.Add(-20*time.Minute))
	op.PlanningID.Int64, op.PlanningID.Valid = 5, true
	if err := e.Ops.InsertInFlight(ctx, op); err != nil {
		t.Fatal(err)
	}
	saved, err := e.Ops.Find(ctx, 1, writeops.ActionCancel, key)
	if err != nil || saved == nil {
		t.Fatal(err)
	}
	if _, err := e.Ops.Transition(ctx, saved.OpID, writeops.StateInFlight, writeops.Patch{
		State: writeops.StateUnknown, Code: "CANCEL_UNKNOWN", PlanningID: saved.PlanningID, ContextJSON: `{}`,
	}); err != nil {
		t.Fatal(err)
	}
	for _, state := range []string{writeops.StateManualOK, writeops.StateManualRejected} {
		out := e.Resolve(ctx, ResolveInput{OpID: saved.OpID, State: state, Note: "other", Actor: "s2s", PlanningID: i64(999)})
		if out.Status != http.StatusBadRequest {
			t.Fatalf("%s: status=%d code=%s", state, out.Status, out.Code)
		}
	}
	got, err := e.Ops.Get(ctx, saved.OpID)
	if err != nil || got == nil || got.State != writeops.StateUnknown || got.PlanningID.Int64 != 5 {
		t.Fatalf("journal changed: %+v err=%v", got, err)
	}
	if p.calls.Load() != 0 {
		t.Fatalf("calls=%d", p.calls.Load())
	}
}

// 1. A book confirmation still needs a read-back that matches the intent.
func TestFixes4_ManualBookForeignVisitRefused(t *testing.T) {
	e, c, _, _ := testEngine(t)
	ctx := context.Background()
	now := e.now()
	key := "72727272-7272-4272-8272-727272727272"
	op := e.newOp(1, writeops.ActionBook, key, writeops.BookIntent(1119, 10000688, "2026-11-08T10:00:00"), `{}`, now.Add(-20*time.Minute))
	if err := e.Ops.InsertInFlight(ctx, op); err != nil {
		t.Fatal(err)
	}
	saved, _ := e.Ops.Find(ctx, 1, writeops.ActionBook, key)
	if _, err := e.Ops.Transition(ctx, saved.OpID, writeops.StateInFlight, writeops.Patch{State: writeops.StateUnknown, Code: "BOOKING_UNKNOWN", ContextJSON: `{}`}); err != nil {
		t.Fatal(err)
	}
	// Visit 900 belongs to patient 2.
	c.rows[900] = repo.CreatedRow{Found: true, PatientID: 2, PlSubjID: 1119, PlExamID: 10000688, DateStart: "2026-11-08T10:00:00", CreatorID: 2058}
	out := e.Resolve(ctx, ResolveInput{OpID: saved.OpID, State: writeops.StateManualOK, Note: "other", Actor: "s2s", PlanningID: i64(900)})
	if out.Status == http.StatusOK {
		t.Fatalf("foreign visit accepted: %+v", out)
	}
	got, _ := e.Ops.Get(ctx, saved.OpID)
	if got.State != writeops.StateUnknown || got.PlanningID.Valid {
		t.Fatalf("journal changed: %+v", got)
	}
}

// 2. The last snapshot carries the actual answer -> direction link: a re-link
// 22 -> 33 with unchanged id sets is refused before the call.
func TestFixes4_CancelRelinkedAnswerRefused(t *testing.T) {
	e, c, p, _ := testEngine(t)
	cancelOwner(c)
	c.adm = oneAdmission()
	relinked := oneSnapSvc()
	relinked.AnswPatdirec = i64(33)
	c.snaps = []booking.Snap{oneSnapSvc(), relinked}
	p.record = false
	out := e.Cancel(context.Background(), 1, 5, "73737373-7373-4373-8373-737373737373")
	if out.Status != http.StatusConflict || out.Code != "CANCEL_NOT_SUPPORTED" || p.calls.Load() != 0 {
		t.Fatalf("status=%d code=%s calls=%d", out.Status, out.Code, p.calls.Load())
	}
}

// 2. The happy "one direction" path still passes with the actual links.
func TestFixes4_CancelOneDirectionStillAllowed(t *testing.T) {
	e, c, p, _ := testEngine(t)
	cancelOwner(c)
	c.adm = oneAdmission()
	c.snaps = []booking.Snap{oneSnapSvc(), oneSnapSvc(), oneCancelledSvc()}
	p.record = false
	p.next = repo.ProcResult{Sent: true, Finished: true, HasRow: true}
	out := e.Cancel(context.Background(), 1, 5, "74747474-7474-4474-8474-747474747474")
	if out.Status != http.StatusOK || p.calls.Load() != 1 {
		t.Fatalf("status=%d code=%s calls=%d", out.Status, out.Code, p.calls.Load())
	}
}

// 3. An error inside the sent cancel batch is unknown even when an immediate
// read would show a full cancel; only the background reconcile confirms it.
func TestFixes4_UnfinishedCancelBatchIsUnknownUntilReconcile(t *testing.T) {
	e, c, p, _ := testEngine(t)
	ctx := context.Background()
	base := e.now()
	cancelOwner(c)
	c.adm = oneAdmission()
	c.snaps = []booking.Snap{oneSnapSvc(), oneSnapSvc(), oneCancelledSvc()}
	p.record = false
	p.next = repo.ProcResult{Sent: true, Finished: false, Err: errors.New("mssql: error after send")}
	key := "75757575-7575-4575-8575-757575757575"
	out := e.Cancel(ctx, 1, 5, key)
	if out.Status != http.StatusServiceUnavailable || out.Code != "CANCEL_UNKNOWN" || p.calls.Load() != 1 {
		t.Fatalf("status=%d code=%s calls=%d", out.Status, out.Code, p.calls.Load())
	}
	op, err := e.Ops.Find(ctx, 1, writeops.ActionCancel, key)
	if err != nil || op == nil || op.State != writeops.StateUnknown {
		t.Fatalf("journal %+v err=%v", op, err)
	}
	// Retry with the same key replays unknown and does not call again.
	if again := e.Cancel(ctx, 1, 5, key); again.Status == http.StatusOK || p.calls.Load() != 1 {
		t.Fatalf("replay status=%d calls=%d", again.Status, p.calls.Load())
	}
	e.Now = func() time.Time { return base.Add(5 * time.Second) }
	e.Sweep(ctx)
	op, _ = e.Ops.Find(ctx, 1, writeops.ActionCancel, key)
	if op == nil || op.State != writeops.StateOK {
		t.Fatalf("after reconcile %+v", op)
	}
	if p.calls.Load() != 1 {
		t.Fatalf("calls=%d", p.calls.Load())
	}
}

// 4. A read error on any step of the cancel path is 503 before any decision,
// with zero procedure calls.
func TestFixes4_CancelReadErrorsAre503(t *testing.T) {
	cases := []struct {
		name string
		set  func(c *fakeClinic)
	}{
		{"owner first", func(c *fakeClinic) { c.ownerErrOn = 1 }},
		{"owner second", func(c *fakeClinic) { c.ownerErrOn = 2 }},
		{"owner third", func(c *fakeClinic) { c.ownerErrOn = 3 }},
		{"admission", func(c *fakeClinic) { c.admErr = errors.New("mssql read failed") }},
		{"snapshot", func(c *fakeClinic) { c.snapErr = errors.New("mssql read failed") }},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e, c, p, _ := testEngine(t)
			cancelOwner(c)
			c.adm = booking.Admission{Found: true, Active: true}
			c.snaps = []booking.Snap{activeSnap()}
			tc.set(c)
			p.record = false
			key := []string{
				"76767676-7676-4676-8676-767676767601", "76767676-7676-4676-8676-767676767602",
				"76767676-7676-4676-8676-767676767603", "76767676-7676-4676-8676-767676767604",
				"76767676-7676-4676-8676-767676767605",
			}[i]
			out := e.Cancel(context.Background(), 1, 5, key)
			if out.Status != http.StatusServiceUnavailable || out.Code != "WRITE_UNAVAILABLE" || p.calls.Load() != 0 {
				t.Fatalf("status=%d code=%s calls=%d", out.Status, out.Code, p.calls.Load())
			}
		})
	}
}

// 4. Same for the booking path reads.
func TestFixes4_BookReadErrorsAre503(t *testing.T) {
	for i, tc := range []struct {
		name string
		set  func(c *fakeClinic)
	}{
		{"future count", func(c *fakeClinic) { c.futureErr = errors.New("mssql read failed") }},
		{"doctor day", func(c *fakeClinic) { c.busyErr = errors.New("mssql read failed") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, c, p, _ := testEngine(t)
			tc.set(c)
			now := e.now()
			key := []string{"77777777-7777-4777-8777-777777777701", "77777777-7777-4777-8777-777777777702"}[i]
			out := e.Book(context.Background(), 1, tok(t, 1, 1119, "2026-11-08T10:00:00", now), key)
			if out.Status != http.StatusServiceUnavailable || out.Code != "WRITE_UNAVAILABLE" || p.calls.Load() != 0 {
				t.Fatalf("status=%d code=%s calls=%d", out.Status, out.Code, p.calls.Load())
			}
		})
	}
}
