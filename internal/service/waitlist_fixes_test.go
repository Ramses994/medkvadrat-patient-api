package service

import (
	"context"
	"database/sql"
	"sync/atomic"
	"testing"
	"time"

	"github.com/medkvadrat/medkvadrat-patient-api/internal/confirmations"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/repo"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/store"
	_ "modernc.org/sqlite"
)

func TestApplyFuture_ForeignBranchIsLabelOnly(t *testing.T) {
	it := confirmations.DashboardItem{}
	fut := &repo.WaitlistFutureVisit{
		BranchID: 3, DateStart: "2026-11-07 10:00:00", DoctorName: "Врач", Service: "Осмотр",
	}
	applyFuture(&it, fut, true, []int{106})
	if it.FutureLabel != "есть запись в другом филиале" || it.FutureDateStart != "" || it.FutureDoctor != "" || it.FutureService != "" {
		t.Fatalf("%+v", it)
	}
	applyFuture(&it, fut, true, []int{106, 3})
	if it.FutureDateStart == "" || it.FutureDoctor != "Врач" {
		t.Fatalf("own %+v", it)
	}
}

func TestDashboardWaitlist_OpenRowBehindClosedPage(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open("sqlite", "file:wl_open_behind?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := store.Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 500; i++ {
		if _, err := db.ExecContext(ctx, `
INSERT INTO waitlist(planning_id,patient_id,appt_version,appt_start,reason,source,status,created_at,updated_at,created_at_msk,branch_id)
VALUES(?,?, 'v', '2026-11-06 10:00:00', 'declined', 'max', 'closed', ?, ?, '2026-11-06 13:00:00', 106)`,
			1000+i, 2000+i, time.Date(2026, 11, 6, 12, 0, i, 0, time.UTC).Format(time.RFC3339), time.Date(2026, 11, 6, 12, 0, i, 0, time.UTC).Format(time.RFC3339)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.ExecContext(ctx, `
INSERT INTO waitlist(planning_id,patient_id,appt_version,appt_start,reason,source,status,created_at,updated_at,created_at_msk,branch_id)
VALUES(9, 8, 'v', '2026-11-01 10:00:00', 'declined', 'max', 'new', '2026-11-01T08:00:00Z', '2026-11-01T08:00:00Z', '2026-11-01 11:00:00', 106)`); err != nil {
		t.Fatal(err)
	}
	svc := &Services{SQLite: db}
	page, err := svc.DashboardWaitlist(ctx, confirmations.WaitlistListFilter{OpenOnly: true, Page: 1, PerPage: 50, Source: "max"})
	if err != nil {
		t.Fatal(err)
	}
	if page.OpenTotal != 1 || len(page.Items) != 1 || page.Items[0].PlanningID != 9 {
		t.Fatalf("total=%d items=%+v", page.OpenTotal, page.Items)
	}
}

func TestMisCache_RepeatsOnceAndMaxSkips(t *testing.T) {
	resetMisCache()
	t.Cleanup(func() {
		loadMis = repo.ListWaitlistMis
		resetMisCache()
	})
	var calls atomic.Int32
	loadMis = func(ctx context.Context, db *sql.DB, now time.Time, back, ahead int, examIDs []int64) ([]repo.WaitlistMisRow, error) {
		calls.Add(1)
		return nil, nil
	}
	sqlite, err := sql.Open("sqlite", "file:wl_mis_cache?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlite.Close() })
	if err := store.Migrate(context.Background(), sqlite); err != nil {
		t.Fatal(err)
	}
	hold, err := sql.Open("sqlite", "file:wl_mis_hold?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = hold.Close() })
	svc := &Services{SQLite: sqlite, MSSQL: hold}
	ctx := context.Background()
	for i := 0; i < 15; i++ {
		if _, err := svc.DashboardWaitlist(ctx, confirmations.WaitlistListFilter{Source: "all", Page: 1, PerPage: 50}); err != nil {
			t.Fatal(err)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("calls=%d", calls.Load())
	}
	if _, err := svc.DashboardWaitlist(ctx, confirmations.WaitlistListFilter{Source: "max", Page: 1, PerPage: 50}); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatalf("max calls=%d", calls.Load())
	}
}
