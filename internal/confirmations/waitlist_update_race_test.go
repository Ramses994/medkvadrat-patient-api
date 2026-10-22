package confirmations

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/medkvadrat/medkvadrat-patient-api/internal/store"
	_ "modernc.org/sqlite"
)

// Transferred from the 22.10.2026 auditor repro (waitlist-update-race).
func TestWaitlist_ConcurrentTerminalUpdatesOneWinner(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	db, err := sql.Open("sqlite", "file:waitlist_update_race?mode=memory&cache=shared&_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := store.Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `
INSERT INTO waitlist(planning_id,patient_id,appt_version,appt_start,reason,source,status,created_at,updated_at,created_at_msk,branch_id)
VALUES(1,1,'v1','2026-10-25 12:00:00','declined','max','new','2026-10-22T09:00:00Z','2026-10-22T09:00:00Z','2026-10-22 12:00:00',106)`); err != nil {
		t.Fatal(err)
	}
	r := NewRepo(db)
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)

	for trial := 0; trial < 50; trial++ {
		if _, err := db.ExecContext(ctx, `UPDATE waitlist SET status='new', outcome=NULL, updated_by=NULL WHERE id=1`); err != nil {
			t.Fatal(err)
		}
		var seen atomic.Int32
		r.AfterWaitlistRead = func() {
			seen.Add(1)
			for seen.Load() < 2 {
				if ctx.Err() != nil {
					return
				}
				time.Sleep(time.Millisecond)
			}
		}
		var wg sync.WaitGroup
		errCh := make(chan error, 2)
		wg.Add(2)
		go func() {
			defer wg.Done()
			_, e := r.UpdateWaitlist(ctx, 1, "closed", "refused", "staff_a", now)
			errCh <- e
		}()
		go func() {
			defer wg.Done()
			_, e := r.UpdateWaitlist(ctx, 1, "rebooked", "rebooked", "staff_b", now)
			errCh <- e
		}()
		wg.Wait()
		close(errCh)
		var okN, changedN int
		for e := range errCh {
			switch {
			case e == nil:
				okN++
			case errors.Is(e, ErrWaitlistChanged):
				changedN++
			default:
				t.Fatalf("trial=%d unexpected %v", trial, e)
			}
		}
		if okN != 1 || changedN != 1 {
			t.Fatalf("trial=%d winners=%d changed=%d", trial, okN, changedN)
		}
		var status, outcome string
		if err := db.QueryRowContext(ctx, `SELECT status, IFNULL(outcome,'') FROM waitlist WHERE id=1`).Scan(&status, &outcome); err != nil {
			t.Fatal(err)
		}
		if (status == "closed" && outcome == "refused") || (status == "rebooked" && outcome == "rebooked") {
			continue
		}
		t.Fatalf("trial=%d final status=%s outcome=%s", trial, status, outcome)
	}
}
