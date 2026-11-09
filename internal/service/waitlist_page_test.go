package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/medkvadrat/medkvadrat-patient-api/internal/confirmations"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/repo"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/staff"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/store"
	_ "modernc.org/sqlite"
)

func TestMovedAppointment_ListAndPatchUseCurrentBranch(t *testing.T) {
	ctx := context.Background()
	db := openWaitlistDB(t, "wl_move_branch")
	if _, err := db.ExecContext(ctx, `
INSERT INTO waitlist(planning_id,patient_id,appt_version,appt_start,reason,source,status,created_at,updated_at,created_at_msk,branch_id)
VALUES(77,66,'v','2026-11-06 10:00:00','declined','max','new','2026-11-06T06:00:00Z','2026-11-06T06:00:00Z','2026-11-06 09:00:00',106)`); err != nil {
		t.Fatal(err)
	}
	var id int64
	if err := db.QueryRowContext(ctx, `SELECT id FROM waitlist`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	prev := loadPageEnrich
	t.Cleanup(func() { loadPageEnrich = prev })
	loadPageEnrich = func(ctx context.Context, db *sql.DB, planningIDs, patientIDs, exams []int64, now time.Time, answered map[int64]string) (pageEnrichment, error) {
		return pageEnrichment{live: map[int64]repo.WaitlistPlanningLive{
			77: {PlanningID: 77, BranchID: 3, DateStart: "2026-11-06 10:00:00", PatientName: "Иванов"},
		}}, nil
	}
	svc := &Services{SQLite: db, MSSQL: db}
	kash := staff.WithPrincipal(ctx, staff.Principal{ID: 2, Login: "kash", Role: staff.RoleRegistrar, BranchIDs: []int{106}})
	if _, err := svc.PatchWaitlist(kash, id, 1, "call_back", "", "kash"); !errors.Is(err, confirmations.ErrWaitlistMoved) {
		t.Fatalf("registrar 106 patch: %v", err)
	}
	var branch int
	if err := db.QueryRowContext(ctx, `SELECT branch_id FROM waitlist WHERE id = ?`, id).Scan(&branch); err != nil {
		t.Fatal(err)
	}
	if branch != 3 {
		t.Fatalf("stored branch=%d", branch)
	}
	hidden, err := svc.DashboardWaitlist(kash, confirmations.WaitlistListFilter{BranchIDs: []int{106}, OpenOnly: true, Page: 1, PerPage: 50, Source: "max"})
	if err != nil {
		t.Fatal(err)
	}
	if len(hidden.Items) != 0 || hidden.Total != 0 || hidden.OpenTotal != 0 {
		t.Fatalf("registrar 106 still sees total=%d open=%d %+v", hidden.Total, hidden.OpenTotal, hidden.Items)
	}
	kurk := staff.WithPrincipal(ctx, staff.Principal{ID: 3, Login: "kurk", Role: staff.RoleRegistrar, BranchIDs: []int{3}})
	visible, err := svc.DashboardWaitlist(kurk, confirmations.WaitlistListFilter{BranchIDs: []int{3}, OpenOnly: true, Page: 1, PerPage: 50, Source: "max"})
	if err != nil {
		t.Fatal(err)
	}
	if len(visible.Items) != 1 || visible.Items[0].BranchID != 3 || visible.Total != 1 || visible.OpenTotal != 1 {
		t.Fatalf("total=%d open=%d %+v", visible.Total, visible.OpenTotal, visible.Items)
	}
	saved, err := svc.PatchWaitlist(kurk, id, 1, "call_back", "", "kurk")
	if err != nil {
		t.Fatal(err)
	}
	if saved.MedialogState != "запись действует" || saved.BranchID != 3 || saved.Status != "contacted" {
		t.Fatalf("%+v", saved)
	}
}

func TestMisCache_NewKeyAfterMoscowMidnight(t *testing.T) {
	resetMisCache()
	prevLoad, prevNow := loadMis, misNow
	t.Cleanup(func() {
		loadMis = prevLoad
		misNow = prevNow
		resetMisCache()
	})
	var calls atomic.Int32
	loadMis = func(ctx context.Context, db *sql.DB, now time.Time, back, ahead int, examIDs []int64) ([]repo.WaitlistMisRow, error) {
		calls.Add(1)
		return nil, nil
	}
	clock := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	misNow = func() time.Time { return clock }
	db := openWaitlistDB(t, "wl_mis_day")
	svc := &Services{SQLite: db, MSSQL: db}
	ctx := context.Background()
	filter := confirmations.WaitlistListFilter{Source: "registry", Page: 1, PerPage: 50}
	if _, err := svc.DashboardWaitlist(ctx, filter); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.DashboardWaitlist(ctx, filter); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatalf("same day calls=%d", calls.Load())
	}
	clock = time.Date(2026, 11, 6, 21, 30, 0, 0, time.UTC)
	if _, err := svc.DashboardWaitlist(ctx, filter); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 {
		t.Fatalf("after midnight calls=%d", calls.Load())
	}
}

func TestMaxPage_OneMedialogQueryPerTick(t *testing.T) {
	resetEnrichCache()
	prev := loadPageEnrich
	prevNow := misNow
	t.Cleanup(func() {
		loadPageEnrich = prev
		misNow = prevNow
		resetEnrichCache()
	})
	var calls atomic.Int32
	loadPageEnrich = func(ctx context.Context, db *sql.DB, planningIDs, patientIDs, exams []int64, now time.Time, answered map[int64]string) (pageEnrichment, error) {
		calls.Add(1)
		if len(planningIDs) != 25 {
			return pageEnrichment{}, fmt.Errorf("planning ids=%d", len(planningIDs))
		}
		return pageEnrichment{live: map[int64]repo.WaitlistPlanningLive{
			planningIDs[0]: {PlanningID: planningIDs[0], PatientName: "Секрет"},
		}}, nil
	}
	clock := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	misNow = func() time.Time { return clock }
	db := openWaitlistDB(t, "wl_page_enrich")
	ctx := context.Background()
	for i := 0; i < 25; i++ {
		if _, err := db.ExecContext(ctx, `
INSERT INTO waitlist(planning_id,patient_id,appt_version,appt_start,reason,source,status,created_at,updated_at,created_at_msk,branch_id)
VALUES(?,?, 'v', '2026-11-06 10:00:00', 'declined', 'max', 'new', ?, ?, '2026-11-06 13:00:00', 106)`,
			100+i, 200+i, fmt.Sprintf("2026-11-06T12:%02d:00Z", i), fmt.Sprintf("2026-11-06T12:%02d:00Z", i)); err != nil {
			t.Fatal(err)
		}
	}
	svc := &Services{SQLite: db, MSSQL: db}
	page, err := svc.DashboardWaitlist(ctx, confirmations.WaitlistListFilter{Source: "max", OpenOnly: true, Page: 1, PerPage: 50})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 25 || calls.Load() != 1 {
		t.Fatalf("items=%d queries=%d", len(page.Items), calls.Load())
	}
	if _, err := svc.DashboardWaitlist(ctx, confirmations.WaitlistListFilter{Source: "max", OpenOnly: true, Page: 1, PerPage: 50}); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatalf("within ttl queries=%d", calls.Load())
	}
	if !enrichHoldsName("Секрет") {
		t.Fatal("enrichment cache missed the page")
	}
	clock = clock.Add(31 * time.Second)
	sweepDashboardCaches(clock)
	if enrichHoldsName("Секрет") {
		t.Fatal("expired enrichment still holds a name")
	}
	if _, err := svc.DashboardWaitlist(ctx, confirmations.WaitlistListFilter{Source: "max", OpenOnly: true, Page: 1, PerPage: 50}); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 {
		t.Fatalf("after ttl queries=%d", calls.Load())
	}
}

func TestWaitlistPage_SQLLimitAndMergedHead(t *testing.T) {
	db := openWaitlistDB(t, "wl_sql_page")
	ctx := context.Background()
	for i := 0; i < 30; i++ {
		created := time.Date(2026, 11, 1, 0, 0, i, 0, time.UTC).Format(time.RFC3339)
		if _, err := db.ExecContext(ctx, `
INSERT INTO waitlist(planning_id,patient_id,appt_version,appt_start,reason,source,status,created_at,updated_at,created_at_msk,branch_id)
VALUES(?,?, 'v', '2026-11-06 10:00:00', 'declined', 'max', 'new', ?, ?, '2026-11-06 13:00:00', 106)`,
			i+1, i+1, created, created); err != nil {
			t.Fatal(err)
		}
	}
	svc := &Services{SQLite: db}
	page, err := svc.DashboardWaitlist(ctx, confirmations.WaitlistListFilter{Source: "max", Page: 2, PerPage: 10})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 30 || len(page.Items) != 10 || page.Items[0].PlanningID != 20 || page.Items[9].PlanningID != 11 {
		t.Fatalf("total=%d first=%d last=%d len=%d", page.Total, page.Items[0].PlanningID, page.Items[len(page.Items)-1].PlanningID, len(page.Items))
	}

	prevMis, prevEnrich := loadMis, loadPageEnrich
	t.Cleanup(func() {
		loadMis = prevMis
		loadPageEnrich = prevEnrich
	})
	loadMis = func(ctx context.Context, db *sql.DB, now time.Time, back, ahead int, examIDs []int64) ([]repo.WaitlistMisRow, error) {
		return []repo.WaitlistMisRow{
			{PlanningID: 501, PatientID: 1, CreatedAt: "2026-11-02T00:00:02Z", BranchID: 106, PatientName: "А"},
			{PlanningID: 502, PatientID: 2, CreatedAt: "2026-11-02T00:00:04Z", BranchID: 106, PatientName: "Б"},
			{PlanningID: 503, PatientID: 3, CreatedAt: "2026-11-02T00:00:06Z", BranchID: 106, PatientName: "В"},
		}, nil
	}
	loadPageEnrich = func(ctx context.Context, db *sql.DB, planningIDs, patientIDs, exams []int64, now time.Time, answered map[int64]string) (pageEnrichment, error) {
		return pageEnrichment{}, nil
	}
	resetMisCache()
	t.Cleanup(resetMisCache)
	mergeDB := openWaitlistDB(t, "wl_merge_page")
	for _, sec := range []int{1, 3, 5} {
		created := fmt.Sprintf("2026-11-02T00:00:%02dZ", sec)
		if _, err := mergeDB.ExecContext(ctx, `
INSERT INTO waitlist(planning_id,patient_id,appt_version,appt_start,reason,source,status,created_at,updated_at,created_at_msk,branch_id)
VALUES(?,?, 'v', '2026-11-06 10:00:00', 'declined', 'max', 'new', ?, ?, '2026-11-06 13:00:00', 106)`,
			sec, sec, created, created); err != nil {
			t.Fatal(err)
		}
	}
	merged := &Services{SQLite: mergeDB, MSSQL: mergeDB}
	got, err := merged.DashboardWaitlist(ctx, confirmations.WaitlistListFilter{OpenOnly: true, Page: 1, PerPage: 2})
	if err != nil {
		t.Fatal(err)
	}
	if got.Total != 6 || len(got.Items) != 2 || got.Items[0].ID != 503 || got.Items[0].Source != "registry" || got.Items[1].Source != "max" || got.Items[1].PlanningID != 5 {
		t.Fatalf("total=%d items=%+v", got.Total, got.Items)
	}
}

func openWaitlistDB(t *testing.T, name string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+name+"?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := store.Migrate(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	return db
}
