package confirmations

import (
	"context"
	"database/sql"
	"testing"

	"github.com/medkvadrat/medkvadrat-patient-api/internal/store"
	_ "modernc.org/sqlite"
)

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file:confirmations_test?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Migrate(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestRepo_FirstAnswerWinsUntilStartChanges(t *testing.T) {
	db := openTestDB(t)
	r := NewRepo(db)
	ctx := context.Background()

	rec, err := r.Upsert(ctx, 100, 200, "confirmed", "max", "2026-06-26 19:30:00")
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != "confirmed" {
		t.Fatalf("%+v", rec)
	}

	_, err = r.Upsert(ctx, 100, 200, "declined", "max", "2026-06-26 19:30:00")
	var already *AlreadyAnsweredError
	if !asAlready(err, &already) || already.Record.Status != "confirmed" {
		t.Fatalf("repeat: %v", err)
	}

	rec2, err := r.Upsert(ctx, 100, 200, "reschedule", "max", "2026-06-27 10:00:00")
	if err != nil {
		t.Fatal(err)
	}
	if rec2.Status != "reschedule" || rec2.ApptStart != "2026-06-27 10:00:00" {
		t.Fatalf("%+v", rec2)
	}
}

func TestRepo_NullStartBlocksRewrite(t *testing.T) {
	db := openTestDB(t)
	r := NewRepo(db)
	ctx := context.Background()
	if _, err := r.Upsert(ctx, 7, 1, "confirmed", "max", ""); err != nil {
		t.Fatal(err)
	}
	_, err := r.Upsert(ctx, 7, 1, "declined", "max", "2026-06-26 19:30:00")
	var already *AlreadyAnsweredError
	if !asAlready(err, &already) || already.Record.Status != "confirmed" {
		t.Fatalf("null start must block: %v", err)
	}
}

func asAlready(err error, dest **AlreadyAnsweredError) bool {
	aa, ok := err.(*AlreadyAnsweredError)
	if ok {
		*dest = aa
	}
	return ok
}
