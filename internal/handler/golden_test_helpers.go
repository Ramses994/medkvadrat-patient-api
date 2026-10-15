package handler

import (
	"context"
	"database/sql"
	"testing"

	"github.com/medkvadrat/medkvadrat-patient-api/internal/medialogwrite"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/store"
	_ "modernc.org/sqlite"
)

type fakePlanningLookup struct{}

func (fakePlanningLookup) PlSubjIDForPlanning(context.Context, int64) (int64, error) {
	return 1, nil
}

func (fakePlanningLookup) PlanningIDForMotconsu(context.Context, int64, int64) (int64, error) {
	return 100, nil
}

func permissiveWriteGuard(tb testing.TB) medialogwrite.HTTPDeps {
	tb.Helper()
	db, err := sql.Open("sqlite", "file:handler_write_guard?mode=memory&cache=shared")
	if err != nil {
		tb.Fatal(err)
	}
	if err := store.Migrate(context.Background(), db); err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(func() { _ = db.Close() })
	return medialogwrite.HTTPDeps{
		Guard:  medialogwrite.NewGuard(true, true, []int64{1}, []int64{1}),
		Lookup: fakePlanningLookup{},
		Log:    medialogwrite.NewLogRepo(db),
	}
}
