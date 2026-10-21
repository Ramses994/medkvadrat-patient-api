package confirmations

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/medkvadrat/medkvadrat-patient-api/internal/store"
	_ "modernc.org/sqlite"
)

func openNamedDB(t *testing.T, name string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+name+"?mode=memory&cache=shared&_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Migrate(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func openTestDB(t *testing.T) *sql.DB {
	return openNamedDB(t, "confirmations_test_"+t.Name())
}

func TestRepo_FirstAnswerWinsUntilVersionChanges(t *testing.T) {
	r := NewRepo(openTestDB(t))
	ctx := context.Background()

	rec, err := r.Upsert(ctx, 100, 200, "confirmed", "max", "2026-06-26 19:30:00", "ver-a")
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != "confirmed" {
		t.Fatalf("%+v", rec)
	}

	_, err = r.Upsert(ctx, 100, 200, "declined", "max", "2026-06-26 19:30:00", "ver-a")
	var already *AlreadyAnsweredError
	if !asAlready(err, &already) || already.Record.Status != "confirmed" {
		t.Fatalf("repeat: %v", err)
	}

	rec2, err := r.Upsert(ctx, 100, 200, "reschedule", "max", "2026-06-27 10:00:00", "ver-b")
	if err != nil {
		t.Fatal(err)
	}
	if rec2.Status != "reschedule" || rec2.ApptVersion != "ver-b" {
		t.Fatalf("%+v", rec2)
	}
}

func TestRepo_NullVersionBlocksRewrite(t *testing.T) {
	r := NewRepo(openTestDB(t))
	ctx := context.Background()
	if _, err := r.Upsert(ctx, 7, 1, "confirmed", "max", "", ""); err != nil {
		t.Fatal(err)
	}
	_, err := r.Upsert(ctx, 7, 1, "declined", "max", "2026-06-26 19:30:00", "ver-a")
	var already *AlreadyAnsweredError
	if !asAlready(err, &already) || already.Record.Status != "confirmed" {
		t.Fatalf("null version must block: %v", err)
	}
}

func TestRepo_RaceOneWinner(t *testing.T) {
	r := NewRepo(openNamedDB(t, "confirmations_race"))
	ctx := context.Background()
	var okN, alreadyN atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := r.Upsert(ctx, 1, 2, "confirmed", "max", "2026-06-26 19:30:00", "abc")
			if err == nil {
				okN.Add(1)
				return
			}
			var a *AlreadyAnsweredError
			if errors.As(err, &a) {
				alreadyN.Add(1)
				return
			}
			t.Errorf("unexpected: %v", err)
		}()
	}
	wg.Wait()
	if okN.Load() != 1 {
		t.Fatalf("winners=%d already=%d", okN.Load(), alreadyN.Load())
	}
}

func asAlready(err error, dest **AlreadyAnsweredError) bool {
	aa, ok := err.(*AlreadyAnsweredError)
	if ok {
		*dest = aa
	}
	return ok
}
