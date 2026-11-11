package service

import (
	"context"
	"database/sql"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/medkvadrat/medkvadrat-patient-api/internal/confirmations"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/repo"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/staff"
)

func TestBranchSync_MoveDeletedUnknownAndReadError(t *testing.T) {
	resetEnrichCache()
	prevEnrich, prevMis := loadPageEnrich, loadMis
	t.Cleanup(func() {
		loadPageEnrich, loadMis = prevEnrich, prevMis
		resetEnrichCache()
	})
	loadMis = func(ctx context.Context, db *sql.DB, now time.Time, back, ahead int, examIDs []int64) ([]repo.WaitlistMisRow, error) {
		return nil, nil
	}
	ctx := context.Background()
	kash := staff.WithPrincipal(ctx, staff.Principal{ID: 2, Login: "kash", Role: staff.RoleRegistrar, BranchIDs: []int{106}})
	kurk := staff.WithPrincipal(ctx, staff.Principal{ID: 3, Login: "kurk", Role: staff.RoleRegistrar, BranchIDs: []int{3}})
	admin := staff.WithPrincipal(ctx, staff.Principal{ID: 1, Login: "admin", Role: staff.RoleAdmin, All: true})

	moveDB := openWaitlistDB(t, "wl_sync_move")
	insertOpen(t, moveDB, 77, 106, "new")
	insertOpen(t, moveDB, 78, 106, "closed")
	loadPageEnrich = func(ctx context.Context, db *sql.DB, planningIDs, patientIDs, exams []int64, now time.Time, answered map[int64]string) (pageEnrichment, error) {
		return pageEnrichment{live: map[int64]repo.WaitlistPlanningLive{
			77: {PlanningID: 77, PatientID: 77, BranchID: 3, PatientName: "Перенесён"},
		}}, nil
	}
	svc := &Services{SQLite: moveDB, MSSQL: moveDB}
	visible, err := svc.DashboardWaitlist(kurk, confirmations.WaitlistListFilter{BranchIDs: []int{3}, OpenOnly: true, Page: 1, PerPage: 50, Source: "max"})
	if err != nil {
		t.Fatal(err)
	}
	if visible.Total != 1 || visible.OpenTotal != 1 || len(visible.Items) != 1 || visible.Items[0].PlanningID != 77 {
		t.Fatalf("kurkino total=%d open=%d %+v", visible.Total, visible.OpenTotal, visible.Items)
	}
	hidden, err := svc.DashboardWaitlist(kash, confirmations.WaitlistListFilter{BranchIDs: []int{106}, OpenOnly: true, Page: 1, PerPage: 50, Source: "max"})
	if err != nil {
		t.Fatal(err)
	}
	if hidden.Total != 0 || hidden.OpenTotal != 0 || len(hidden.Items) != 0 {
		t.Fatalf("kashirka total=%d open=%d %+v", hidden.Total, hidden.OpenTotal, hidden.Items)
	}
	var closedBranch int
	if err := moveDB.QueryRowContext(ctx, `SELECT branch_id FROM waitlist WHERE planning_id = 78`).Scan(&closedBranch); err != nil {
		t.Fatal(err)
	}
	if closedBranch != 106 {
		t.Fatalf("closed row branch=%d", closedBranch)
	}

	resetEnrichCache()
	deleted := openWaitlistDB(t, "wl_sync_deleted")
	insertOpen(t, deleted, 81, 106, "new")
	loadPageEnrich = func(ctx context.Context, db *sql.DB, planningIDs, patientIDs, exams []int64, now time.Time, answered map[int64]string) (pageEnrichment, error) {
		return pageEnrichment{live: map[int64]repo.WaitlistPlanningLive{}}, nil
	}
	svc = &Services{SQLite: deleted, MSSQL: deleted}
	kept, err := svc.DashboardWaitlist(kash, confirmations.WaitlistListFilter{BranchIDs: []int{106}, OpenOnly: true, Page: 1, PerPage: 50, Source: "max"})
	if err != nil {
		t.Fatal(err)
	}
	if kept.Total != 1 || len(kept.Items) != 1 || kept.Items[0].BranchID != 106 {
		t.Fatalf("deleted source %+v total=%d", kept.Items, kept.Total)
	}

	resetEnrichCache()
	unknown := openWaitlistDB(t, "wl_sync_unknown")
	insertOpen(t, unknown, 82, 106, "new")
	loadPageEnrich = func(ctx context.Context, db *sql.DB, planningIDs, patientIDs, exams []int64, now time.Time, answered map[int64]string) (pageEnrichment, error) {
		return pageEnrichment{live: map[int64]repo.WaitlistPlanningLive{
			82: {PlanningID: 82, PatientID: 82, BranchID: 999, PatientName: "ЧужойФилиал"},
		}}, nil
	}
	svc = &Services{SQLite: unknown, MSSQL: unknown}
	allBranches := []int{106, 3, 496}
	seen, err := svc.DashboardWaitlist(admin, confirmations.WaitlistListFilter{BranchIDs: allBranches, OpenOnly: true, Page: 1, PerPage: 50, Source: "max"})
	if err != nil {
		t.Fatal(err)
	}
	if seen.Total != 1 || len(seen.Items) != 1 || seen.Items[0].BranchID != 999 {
		t.Fatalf("admin unknown %+v total=%d", seen.Items, seen.Total)
	}
	resetEnrichCache()
	missed, err := svc.DashboardWaitlist(kash, confirmations.WaitlistListFilter{BranchIDs: []int{106}, OpenOnly: true, Page: 1, PerPage: 50, Source: "max"})
	if err != nil {
		t.Fatal(err)
	}
	if missed.Total != 0 || len(missed.Items) != 0 {
		t.Fatalf("registrar saw unknown %+v", missed.Items)
	}

	resetEnrichCache()
	failed := openWaitlistDB(t, "wl_sync_err")
	insertOpen(t, failed, 83, 106, "new")
	loadPageEnrich = func(ctx context.Context, db *sql.DB, planningIDs, patientIDs, exams []int64, now time.Time, answered map[int64]string) (pageEnrichment, error) {
		return pageEnrichment{}, errors.New("medialog down")
	}
	svc = &Services{SQLite: failed, MSSQL: failed}
	still, err := svc.DashboardWaitlist(kash, confirmations.WaitlistListFilter{BranchIDs: []int{106}, OpenOnly: true, Page: 1, PerPage: 50, Source: "max"})
	if err != nil {
		t.Fatal(err)
	}
	if still.Total != 1 || len(still.Items) != 1 || still.Items[0].BranchID != 106 || still.Items[0].PatientPhone != "" {
		t.Fatalf("read error %+v", still.Items)
	}
	if still.Banner != "Медиалог недоступен" {
		t.Fatalf("banner=%q", still.Banner)
	}
}

func TestWaitlistSort_MoscowEqualAndEmptyPage(t *testing.T) {
	resetMisCache()
	resetEnrichCache()
	prevMis, prevEnrich := loadMis, loadPageEnrich
	t.Cleanup(func() {
		loadMis = prevMis
		loadPageEnrich = prevEnrich
		resetMisCache()
		resetEnrichCache()
	})
	loadPageEnrich = func(ctx context.Context, db *sql.DB, planningIDs, patientIDs, exams []int64, now time.Time, answered map[int64]string) (pageEnrichment, error) {
		live := map[int64]repo.WaitlistPlanningLive{}
		for _, id := range planningIDs {
			patient := id
			if id == 900 || id == 901 {
				patient = 1
				if id == 901 {
					patient = 2
				}
			}
			live[id] = repo.WaitlistPlanningLive{PlanningID: id, PatientID: patient, BranchID: 106, PatientName: "Имя", IsWaitlist: true}
		}
		return pageEnrichment{live: live}, nil
	}
	loadMis = func(ctx context.Context, db *sql.DB, now time.Time, back, ahead int, examIDs []int64) ([]repo.WaitlistMisRow, error) {
		return []repo.WaitlistMisRow{{
			PlanningID: 900, PatientID: 1, CreatedAt: "2026-11-06 10:30:00", BranchID: 106, PatientName: "Рано",
		}}, nil
	}
	ctx := context.Background()
	db := openWaitlistDB(t, "wl_sort_msk")
	insertAt(t, db, 5, "2026-11-06T08:00:00Z")
	svc := &Services{SQLite: db, MSSQL: db}
	page, err := svc.DashboardWaitlist(ctx, confirmations.WaitlistListFilter{OpenOnly: true, Page: 1, PerPage: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 2 || page.Items[0].Source != "max" || page.Items[0].PlanningID != 5 || page.Items[1].Source != "registry" {
		t.Fatalf("msk order %+v", page.Items)
	}

	resetMisCache()
	loadMis = func(ctx context.Context, db *sql.DB, now time.Time, back, ahead int, examIDs []int64) ([]repo.WaitlistMisRow, error) {
		return []repo.WaitlistMisRow{{
			PlanningID: 901, PatientID: 2, CreatedAt: "2026-11-06T08:00:00Z", BranchID: 106, PatientName: "Ровно",
		}}, nil
	}
	equalDB := openWaitlistDB(t, "wl_sort_equal")
	insertAt(t, equalDB, 6, "2026-11-06T08:00:00Z")
	equal := &Services{SQLite: equalDB, MSSQL: equalDB}
	got, err := equal.DashboardWaitlist(ctx, confirmations.WaitlistListFilter{OpenOnly: true, Page: 1, PerPage: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Items) != 2 || got.Items[0].Source != "max" || got.Items[1].Source != "registry" {
		t.Fatalf("equal order %+v", got.Items)
	}

	empty, err := svc.DashboardWaitlist(ctx, confirmations.WaitlistListFilter{Source: "max", Page: 3, PerPage: 10})
	if err != nil {
		t.Fatal(err)
	}
	if empty.Total != 1 || len(empty.Items) != 0 {
		t.Fatalf("past end total=%d items=%d", empty.Total, len(empty.Items))
	}
}

func TestRegistryReadError_OmitsRowsAndExpiredNames(t *testing.T) {
	resetMisCache()
	prevMis, prevEnrich := loadMis, loadPageEnrich
	t.Cleanup(func() {
		loadMis = prevMis
		loadPageEnrich = prevEnrich
		resetMisCache()
	})
	loadPageEnrich = func(ctx context.Context, db *sql.DB, planningIDs, patientIDs, exams []int64, now time.Time, answered map[int64]string) (pageEnrichment, error) {
		live := map[int64]repo.WaitlistPlanningLive{}
		for _, id := range planningIDs {
			patient := id
			if id == 700 {
				patient = 9
			}
			live[id] = repo.WaitlistPlanningLive{PlanningID: id, PatientID: patient, BranchID: 106, PatientName: "СекретРеестра", Phone: "79990000000", IsWaitlist: true}
		}
		return pageEnrichment{live: live}, nil
	}
	clock := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	setMisNow(t, func() time.Time { return clock })
	var fail atomic.Bool
	loadMis = func(ctx context.Context, db *sql.DB, now time.Time, back, ahead int, examIDs []int64) ([]repo.WaitlistMisRow, error) {
		if fail.Load() {
			return nil, errors.New("medialog down")
		}
		return []repo.WaitlistMisRow{{
			PlanningID: 700, PatientID: 9, CreatedAt: "2026-11-06T09:00:00Z", BranchID: 106, PatientName: "СекретРеестра",
		}}, nil
	}
	ctx := context.Background()
	db := openWaitlistDB(t, "wl_reg_err")
	insertAt(t, db, 4, "2026-11-06T08:00:00Z")
	svc := &Services{SQLite: db, MSSQL: db}
	first, err := svc.DashboardWaitlist(ctx, confirmations.WaitlistListFilter{OpenOnly: true, Page: 1, PerPage: 10})
	if err != nil {
		t.Fatal(err)
	}
	if first.Total != 2 || !misCacheHoldsName("СекретРеестра") {
		t.Fatalf("total=%d cached=%v", first.Total, misCacheHoldsName("СекретРеестра"))
	}
	fail.Store(true)
	clock = clock.Add(31 * time.Second)
	second, err := svc.DashboardWaitlist(ctx, confirmations.WaitlistListFilter{OpenOnly: true, Page: 1, PerPage: 10})
	if err != nil {
		t.Fatal(err)
	}
	if second.Total != 1 || len(second.Items) != 1 || second.Items[0].Source != "max" || second.Items[0].PatientPhone != "" {
		t.Fatalf("after error %+v total=%d", second.Items, second.Total)
	}
	if second.Banner != "Медиалог недоступен" {
		t.Fatalf("banner=%q", second.Banner)
	}
	if misCacheHoldsName("СекретРеестра") {
		t.Fatal("expired registry name survived a failed read")
	}
}

func TestEnrichCache_MissingOriginalOneReadThenGone(t *testing.T) {
	resetEnrichCache()
	prev := loadPageEnrich
	prevMis := loadMis
	t.Cleanup(func() {
		loadPageEnrich = prev
		loadMis = prevMis
		resetEnrichCache()
	})
	loadMis = func(ctx context.Context, db *sql.DB, now time.Time, back, ahead int, examIDs []int64) ([]repo.WaitlistMisRow, error) {
		return nil, nil
	}
	clock := time.Date(2026, 11, 6, 15, 0, 0, 0, time.UTC)
	setMisNow(t, func() time.Time { return clock })
	var calls atomic.Int32
	loadPageEnrich = func(ctx context.Context, db *sql.DB, planningIDs, patientIDs, exams []int64, now time.Time, answered map[int64]string) (pageEnrichment, error) {
		calls.Add(1)
		return pageEnrichment{live: map[int64]repo.WaitlistPlanningLive{}}, nil
	}
	ctx := context.Background()
	db := openWaitlistDB(t, "wl_enrich_miss")
	insertAt(t, db, 15, "2026-11-06T08:00:00Z")
	svc := &Services{SQLite: db, MSSQL: db}
	filter := confirmations.WaitlistListFilter{Source: "max", OpenOnly: true, Page: 1, PerPage: 10}
	if _, err := svc.DashboardWaitlist(ctx, filter); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.DashboardWaitlist(ctx, filter); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 || enrichCacheLen() != 1 {
		t.Fatalf("calls=%d keys=%d", calls.Load(), enrichCacheLen())
	}
	clock = clock.Add(31 * time.Second)
	sweepDashboardCaches(clock)
	if enrichCacheLen() != 0 {
		t.Fatal("expired enrichment key remains")
	}
}

func insertOpen(t *testing.T, db *sql.DB, planning int64, branch int, status string) {
	t.Helper()
	insertAtStatus(t, db, planning, "2026-11-06T08:00:00Z", branch, status)
}

func insertAt(t *testing.T, db *sql.DB, planning int64, created string) {
	t.Helper()
	insertAtStatus(t, db, planning, created, 106, "new")
}

func insertAtStatus(t *testing.T, db *sql.DB, planning int64, created string, branch int, status string) {
	t.Helper()
	if _, err := db.ExecContext(context.Background(), `
INSERT INTO waitlist(planning_id,patient_id,appt_version,appt_start,reason,source,status,created_at,updated_at,created_at_msk,branch_id)
VALUES(?,?, 'v', '2026-11-06 10:00:00', 'declined', 'max', ?, ?, ?, '2026-11-06 11:00:00', ?)`,
		planning, planning, status, created, created, branch); err != nil {
		t.Fatal(err)
	}
}
