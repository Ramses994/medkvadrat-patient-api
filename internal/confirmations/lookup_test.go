package confirmations

import (
	"context"
	"testing"
)

func TestRepo_StatusByPlanningIDs(t *testing.T) {
	db := openTestDB(t)
	r := NewRepo(db)
	ctx := context.Background()

	if _, err := r.Upsert(ctx, 10, 1, "confirmed", "max", "2026-06-26 19:30:00", "v1", 106); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Upsert(ctx, 20, 2, "declined", "max", "2026-06-26 19:30:00", "v1", 3); err != nil {
		t.Fatal(err)
	}

	got, err := r.StatusByPlanningIDs(ctx, []int64{10, 20, 99})
	if err != nil {
		t.Fatal(err)
	}
	if got[10] != "confirmed" || got[20] != "declined" {
		t.Fatalf("got=%v", got)
	}
	if _, ok := got[99]; ok {
		t.Fatal("missing id should be omitted")
	}

	empty, err := r.StatusByPlanningIDs(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(empty) != 0 {
		t.Fatalf("expected empty map, got %v", empty)
	}
}
