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

func TestRepo_UpsertLastWriteWins(t *testing.T) {
	db := openTestDB(t)
	r := NewRepo(db)
	ctx := context.Background()

	rec, err := r.Upsert(ctx, 100, 200, "confirmed", "max")
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != "confirmed" || rec.PlanningID != 100 {
		t.Fatalf("first upsert: %+v", rec)
	}

	rec2, err := r.Upsert(ctx, 100, 200, "declined", "max")
	if err != nil {
		t.Fatal(err)
	}
	if rec2.Status != "declined" {
		t.Fatalf("expected declined, got %q", rec2.Status)
	}
	if !rec2.UpdatedAt.Equal(rec.UpdatedAt) && !rec2.UpdatedAt.After(rec.UpdatedAt) {
		t.Fatalf("updated_at should advance: %v vs %v", rec.UpdatedAt, rec2.UpdatedAt)
	}
}
