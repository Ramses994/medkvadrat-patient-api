package confirmations

import (
	"context"
	"database/sql"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/medkvadrat/medkvadrat-patient-api/internal/store"
)

func TestWaitlist_InsertOnDeclinedSameTx(t *testing.T) {
	db := openTestDB(t)
	r := NewRepo(db)
	ctx := context.Background()
	now := time.Date(2026, 10, 21, 23, 30, 0, 0, time.UTC)
	r.Now = func() time.Time { return now }

	if _, err := r.Upsert(ctx, 10, 20, "declined", "max", "2026-10-22 10:00:00", "v1", 106); err != nil {
		t.Fatal(err)
	}
	n, err := CountWaitlist(ctx, db, 10, "v1")
	if err != nil || n != 1 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	rows, total, err := r.ListWaitlist(ctx, WaitlistListFilter{OpenOnly: true, Page: 1, PerPage: 50})
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(rows) != 1 {
		t.Fatalf("total=%d rows=%d", total, len(rows))
	}
	if rows[0].CreatedAtMSK != "2026-10-22 02:30:00" {
		t.Fatalf("msk=%q", rows[0].CreatedAtMSK)
	}
	if rows[0].CreatedAt != "2026-10-21T23:30:00Z" {
		t.Fatalf("utc=%q", rows[0].CreatedAt)
	}
	if rows[0].BranchID != 106 || rows[0].Reason != "declined" {
		t.Fatalf("%+v", rows[0])
	}
}

func TestWaitlist_ConfirmedNoRow(t *testing.T) {
	db := openTestDB(t)
	r := NewRepo(db)
	ctx := context.Background()
	if _, err := r.Upsert(ctx, 10, 20, "confirmed", "max", "2026-10-22 10:00:00", "v1", 106); err != nil {
		t.Fatal(err)
	}
	n, err := CountWaitlistByPlanning(ctx, db, 10)
	if err != nil || n != 0 {
		t.Fatalf("n=%d err=%v", n, err)
	}
}

func TestWaitlist_AlreadyAnsweredNoRow(t *testing.T) {
	db := openTestDB(t)
	r := NewRepo(db)
	ctx := context.Background()
	if _, err := r.Upsert(ctx, 10, 20, "confirmed", "max", "2026-10-22 10:00:00", "v1", 106); err != nil {
		t.Fatal(err)
	}
	_, err := r.Upsert(ctx, 10, 20, "declined", "max", "2026-10-22 10:00:00", "v1", 106)
	var already *AlreadyAnsweredError
	if !asAlready(err, &already) {
		t.Fatalf("err=%v", err)
	}
	n, err := CountWaitlistByPlanning(ctx, db, 10)
	if err != nil || n != 0 {
		t.Fatalf("n=%d err=%v", n, err)
	}
}

func TestWaitlist_StaleVersionNoInsert(t *testing.T) {
	// STALE_REMINDER is enforced in service before Upsert; Upsert with a new version
	// is a legitimate reschedule answer and must insert a new waitlist row.
	db := openTestDB(t)
	r := NewRepo(db)
	ctx := context.Background()
	if _, err := r.Upsert(ctx, 10, 20, "declined", "max", "a", "v1", 3); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Upsert(ctx, 10, 20, "reschedule", "max", "b", "v2", 3); err != nil {
		t.Fatal(err)
	}
	n, err := CountWaitlistByPlanning(ctx, db, 10)
	if err != nil || n != 2 {
		t.Fatalf("n=%d err=%v", n, err)
	}
}

func TestWaitlist_RaceOneRow(t *testing.T) {
	db := openNamedDB(t, "waitlist_race")
	r := NewRepo(db)
	ctx := context.Background()
	var okN atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := r.Upsert(ctx, 1, 2, "declined", "max", "2026-06-26 19:30:00", "abc", 496)
			if err == nil {
				okN.Add(1)
			}
		}()
	}
	wg.Wait()
	if okN.Load() != 1 {
		t.Fatalf("winners=%d", okN.Load())
	}
	n, err := CountWaitlist(ctx, db, 1, "abc")
	if err != nil || n != 1 {
		t.Fatalf("waitlist n=%d err=%v", n, err)
	}
}

func TestWaitlist_InsertFailureRollsBackConfirmation(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	if _, err := db.ExecContext(ctx, `
CREATE TRIGGER waitlist_fail BEFORE INSERT ON waitlist
BEGIN
  SELECT RAISE(ABORT, 'injected waitlist fail');
END`); err != nil {
		t.Fatal(err)
	}
	r := NewRepo(db)
	_, err := r.Upsert(ctx, 10, 20, "declined", "max", "2026-10-22 10:00:00", "v1", 106)
	if err == nil {
		t.Fatal("expected waitlist insert error")
	}
	var status sql.NullString
	err = db.QueryRowContext(ctx, `SELECT status FROM appointment_confirmations WHERE planning_id = 10`).Scan(&status)
	if err != sql.ErrNoRows {
		t.Fatalf("confirmation should roll back, status=%v err=%v", status, err)
	}
}

func TestWaitlist_Pagination230(t *testing.T) {
	db := openNamedDB(t, "waitlist_page")
	r := NewRepo(db)
	ctx := context.Background()
	for i := 1; i <= 230; i++ {
		branch := 106
		if i > 100 {
			branch = 3
		}
		if _, err := r.Upsert(ctx, int64(i), 20, "declined", "max", "2026-10-22 10:00:00", "v1", branch); err != nil {
			t.Fatal(err)
		}
	}
	seen := map[int64]bool{}
	for page := 1; page <= 5; page++ {
		rows, total, err := r.ListWaitlist(ctx, WaitlistListFilter{OpenOnly: true, Page: page, PerPage: 50})
		if err != nil {
			t.Fatal(err)
		}
		if total != 230 {
			t.Fatalf("total=%d", total)
		}
		if page < 5 && len(rows) != 50 {
			t.Fatalf("page %d len=%d", page, len(rows))
		}
		if page == 5 && len(rows) != 30 {
			t.Fatalf("last page len=%d", len(rows))
		}
		for _, row := range rows {
			if seen[row.PlanningID] {
				t.Fatalf("dup %d", row.PlanningID)
			}
			seen[row.PlanningID] = true
		}
	}
	if len(seen) != 230 {
		t.Fatalf("seen=%d", len(seen))
	}
	branch := 106
	rows, total, err := r.ListWaitlist(ctx, WaitlistListFilter{OpenOnly: true, BranchID: &branch, Page: 1, PerPage: 100})
	if err != nil {
		t.Fatal(err)
	}
	if total != 100 || len(rows) != 100 {
		t.Fatalf("branch total=%d len=%d", total, len(rows))
	}
	for _, row := range rows {
		if row.BranchID != 106 {
			t.Fatalf("branch leak %+v", row)
		}
	}
}

func TestWaitlist_TransitionsAndPurge(t *testing.T) {
	db := openTestDB(t)
	r := NewRepo(db)
	ctx := context.Background()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	r.Now = func() time.Time { return now }
	if _, err := r.Upsert(ctx, 10, 20, "declined", "max", "a", "v1", 106); err != nil {
		t.Fatal(err)
	}
	rows, _, err := r.ListWaitlist(ctx, WaitlistListFilter{Page: 1, PerPage: 10})
	if err != nil {
		t.Fatal(err)
	}
	id := rows[0].ID
	if _, err := r.UpdateWaitlist(ctx, id, "rebooked", "rebooked", "staffabcd1234", now); err != nil {
		t.Fatal(err)
	}
	if _, err := r.UpdateWaitlist(ctx, id, "new", "other", "staff", now); err != ErrInvalidWaitlistTransition {
		t.Fatalf("back: %v", err)
	}
	if _, err := r.UpdateWaitlist(ctx, id, "closed", "nope", "staff", now); err != ErrUnknownWaitlistOutcome {
		t.Fatalf("outcome: %v", err)
	}

	if _, err := db.ExecContext(ctx, `UPDATE waitlist SET updated_at = ? WHERE id = ?`, "2025-01-01T00:00:00Z", id); err != nil {
		t.Fatal(err)
	}
	n, err := store.PurgeClosedWaitlist(ctx, db, time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC))
	if err != nil || n != 1 {
		t.Fatalf("purged=%d err=%v", n, err)
	}
}

func TestMedialogCreateIsNewer_MSKWallClock(t *testing.T) {
	createdMSK := FormatWaitlistMSK(time.Date(2026, 10, 21, 23, 30, 0, 0, time.UTC))
	if createdMSK != "2026-10-22 02:30:00" {
		t.Fatalf("created=%q", createdMSK)
	}
	if MedialogCreateIsNewer(createdMSK, "2026-10-22 01:00:00") {
		t.Fatal("01:00 MSK must not count as newer than 02:30")
	}
	if !MedialogCreateIsNewer(createdMSK, "2026-10-22 03:00:00") {
		t.Fatal("03:00 MSK must count as newer")
	}
}
