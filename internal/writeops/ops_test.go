package writeops

import (
	"context"
	"database/sql"
	"testing"

	"github.com/medkvadrat/medkvadrat-patient-api/internal/store"
	_ "modernc.org/sqlite"
)

func openOps(t *testing.T) *Repo {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+t.Name()+"?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := store.Migrate(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	return New(db)
}

func TestInsert_UniqueKey(t *testing.T) {
	r := openOps(t)
	ctx := context.Background()
	op := Op{PatientID: 1, Action: ActionBook, RequestID: "k", IntentJSON: `{}`, ContextJSON: `{}`, StartedAt: "t", StartedAtMSK: "t"}
	if err := r.InsertInFlight(ctx, op); err != nil {
		t.Fatal(err)
	}
	if err := r.InsertInFlight(ctx, op); err != ErrConflict {
		t.Fatalf("err %v", err)
	}
}

func TestTransition_LostCAS(t *testing.T) {
	r := openOps(t)
	ctx := context.Background()
	op := Op{OpID: "op-1", PatientID: 1, Action: ActionBook, RequestID: "k", IntentJSON: `{}`, ContextJSON: `{"a":1}`, StartedAt: "t", StartedAtMSK: "t"}
	if err := r.InsertInFlight(ctx, op); err != nil {
		t.Fatal(err)
	}
	ok, err := r.Transition(ctx, "op-1", StateOK, Patch{State: StateUnknown, ContextJSON: `{}`})
	if err != nil || ok {
		t.Fatalf("lost ok=%v err=%v", ok, err)
	}
	got, err := r.Get(ctx, "op-1")
	if err != nil || got == nil || got.State != StateInFlight || got.ContextJSON != `{"a":1}` {
		t.Fatalf("%+v err=%v", got, err)
	}
}

func TestMarkUnknown_LeavesContext(t *testing.T) {
	r := openOps(t)
	ctx := context.Background()
	op := Op{
		OpID: "op-2", PatientID: 1, Action: ActionBook, RequestID: "k2", IntentJSON: `{}`, ContextJSON: `{}`,
		StartedAt: "t", StartedAtMSK: "t", PlanningID: sql.NullInt64{},
	}
	if err := r.InsertInFlight(ctx, op); err != nil {
		t.Fatal(err)
	}
	ok, err := r.SaveContext(ctx, "op-2", `{"org_id":496}`, sql.NullInt64{Int64: 77, Valid: true})
	if err != nil || !ok {
		t.Fatalf("save ok=%v err=%v", ok, err)
	}
	ok, err = r.MarkUnknown(ctx, "op-2", "BOOKING_UNKNOWN", "2026-11-06T09:00:00Z")
	if err != nil || !ok {
		t.Fatalf("mark ok=%v err=%v", ok, err)
	}
	got, err := r.Get(ctx, "op-2")
	if err != nil || got == nil {
		t.Fatal(err)
	}
	if got.State != StateUnknown || got.ContextJSON != `{"org_id":496}` || !got.PlanningID.Valid || got.PlanningID.Int64 != 77 {
		t.Fatalf("state=%s context=%s planning=%+v", got.State, got.ContextJSON, got.PlanningID)
	}
}

func TestListStates_Filters(t *testing.T) {
	r := openOps(t)
	ctx := context.Background()
	a := Op{OpID: "a", PatientID: 1, Action: ActionBook, RequestID: "a", IntentJSON: `{}`, ContextJSON: `{}`, StartedAt: "t1", StartedAtMSK: "t1"}
	b := Op{OpID: "b", PatientID: 2, Action: ActionBook, RequestID: "b", IntentJSON: `{}`, ContextJSON: `{}`, StartedAt: "t2", StartedAtMSK: "t2"}
	if err := r.InsertInFlight(ctx, a); err != nil {
		t.Fatal(err)
	}
	if err := r.InsertInFlight(ctx, b); err != nil {
		t.Fatal(err)
	}
	if _, err := r.MarkUnknown(ctx, "b", "BOOKING_UNKNOWN", "2026-11-06T09:00:00Z"); err != nil {
		t.Fatal(err)
	}
	rows, err := r.ListStates(ctx, []string{StateUnknown})
	if err != nil || len(rows) != 1 || rows[0].OpID != "b" {
		t.Fatalf("%+v err=%v", rows, err)
	}
	flight, err := r.ListStates(ctx, []string{StateInFlight})
	if err != nil || len(flight) != 1 || flight[0].OpID != "a" {
		t.Fatalf("%+v err=%v", flight, err)
	}
}
