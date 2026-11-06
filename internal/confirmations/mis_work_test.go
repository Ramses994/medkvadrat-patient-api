package confirmations

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestMisWork_PersistsAndReopenClosed(t *testing.T) {
	db := openTestDB(t)
	r := NewRepo(db)
	ctx := context.Background()
	now := time.Date(2026, 11, 6, 9, 0, 0, 0, time.UTC)
	const planningID int64 = 3118001

	row, err := r.UpdateMisWork(ctx, planningID, WaitlistStatusContacted, "no_answer", "reg", now)
	if err != nil {
		t.Fatal(err)
	}
	if row.Status != WaitlistStatusContacted || row.PlanningID != planningID {
		t.Fatalf("%+v", row)
	}
	got, err := r.MisWorkMap(ctx, []int64{planningID})
	if err != nil {
		t.Fatal(err)
	}
	if got[planningID].Status != WaitlistStatusContacted || got[planningID].UpdatedBy != "reg" {
		t.Fatalf("%+v", got)
	}
	if _, err := r.UpdateMisWork(ctx, planningID, WaitlistStatusClosed, "refused", "reg", now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	reopened, err := r.UpdateMisWork(ctx, planningID, WaitlistStatusContacted, "refused", "reg", now.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if reopened.Status != WaitlistStatusContacted {
		t.Fatalf("%+v", reopened)
	}
	again, err := r.MisWorkMap(ctx, []int64{planningID})
	if err != nil {
		t.Fatal(err)
	}
	if again[planningID].Status != WaitlistStatusContacted {
		t.Fatalf("refresh: %+v", again[planningID])
	}
}

func TestMisWork_StaleStatusIsConflict(t *testing.T) {
	db := openTestDB(t)
	r := NewRepo(db)
	ctx := context.Background()
	now := time.Date(2026, 11, 6, 9, 0, 0, 0, time.UTC)
	const planningID int64 = 42
	if _, err := r.UpdateMisWork(ctx, planningID, WaitlistStatusContacted, "call_back", "reg", now); err != nil {
		t.Fatal(err)
	}
	r.AfterMisWorkRead = func() {
		r.AfterMisWorkRead = nil
		if _, err := db.ExecContext(ctx, `UPDATE waitlist_mis_work SET status = ? WHERE planning_id = ?`, WaitlistStatusClosed, planningID); err != nil {
			t.Error(err)
		}
	}
	_, err := r.UpdateMisWork(ctx, planningID, WaitlistStatusClosed, "refused", "reg", now.Add(time.Minute))
	if !errors.Is(err, ErrWaitlistChanged) {
		t.Fatalf("%v", err)
	}
	got, err := r.MisWorkMap(ctx, []int64{planningID})
	if err != nil {
		t.Fatal(err)
	}
	if got[planningID].Status != WaitlistStatusClosed {
		t.Fatalf("stale write must not land: %+v", got[planningID])
	}
}

func TestWaitlist_ClosedReturnsToContacted(t *testing.T) {
	db := openTestDB(t)
	r := NewRepo(db)
	ctx := context.Background()
	now := time.Date(2026, 11, 6, 9, 0, 0, 0, time.UTC)
	if _, err := db.ExecContext(ctx, `
INSERT INTO waitlist(planning_id,patient_id,appt_version,appt_start,reason,source,status,outcome,created_at,updated_at,created_at_msk,branch_id)
VALUES(9,8,'v1','2026-11-06 10:00:00','declined','max','closed','refused','2026-11-06T06:00:00Z','2026-11-06T06:00:00Z','2026-11-06 09:00:00',106)`); err != nil {
		t.Fatal(err)
	}
	row, err := r.UpdateWaitlist(ctx, 1, WaitlistStatusContacted, "refused", "reg", now)
	if err != nil {
		t.Fatal(err)
	}
	if row.Status != WaitlistStatusContacted {
		t.Fatalf("%+v", row)
	}
}
