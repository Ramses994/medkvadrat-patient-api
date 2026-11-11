package service

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/medkvadrat/medkvadrat-patient-api/internal/confirmations"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/repo"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/staff"
)

func TestWaitlistSearch_HidesOtherBranchesAndMatchesPhone(t *testing.T) {
	resetSnapStore()
	prevEnrich, prevMis := loadPageEnrich, loadMis
	t.Cleanup(func() {
		resetSnapStore()
		loadPageEnrich, loadMis = prevEnrich, prevMis
	})
	loadMis = func(ctx context.Context, db *sql.DB, now time.Time, back, ahead int, examIDs []int64) ([]repo.WaitlistMisRow, error) {
		return nil, nil
	}
	loadPageEnrich = func(ctx context.Context, db *sql.DB, planningIDs, patientIDs, exams []int64, now time.Time, answered map[int64]string) (pageEnrichment, error) {
		return pageEnrichment{live: map[int64]repo.WaitlistPlanningLive{
			77:  {PlanningID: 77, PatientID: 77, BranchID: 106, PatientName: "Ёлкин Иван", Phone: "79265906994"},
			88:  {PlanningID: 88, PatientID: 88, BranchID: 3, PatientName: "Кузнецов", Phone: "79990000088"},
			100: {PlanningID: 100, PatientID: 100, BranchID: 496, PatientName: "Петров", Phone: "79990000100"},
		}}, nil
	}
	db := openWaitlistDB(t, "wl_search_ux")
	insertWait(t, db, 77, 106)
	insertWait(t, db, 88, 3)
	insertWait(t, db, 99, 106)
	insertWait(t, db, 100, 496)
	svc := &Services{SQLite: db, MSSQL: db}
	kash := staff.WithPrincipal(context.Background(), staff.Principal{ID: 2, Login: "kash", Role: staff.RoleRegistrar, BranchIDs: []int{106}})
	base := confirmations.WaitlistListFilter{BranchIDs: []int{106}, OpenOnly: true, Page: 1, PerPage: 50}

	hidden, err := svc.DashboardWaitlist(kash, withQuery(base, "кузнецов"))
	if err != nil {
		t.Fatal(err)
	}
	if hidden.Total != 0 || len(hidden.Items) != 0 || hidden.OpenTotal != 2 {
		t.Fatalf("hidden total=%d open=%d %+v", hidden.Total, hidden.OpenTotal, hidden.Items)
	}

	byName, err := svc.DashboardWaitlist(kash, withQuery(base, "елкин"))
	if err != nil {
		t.Fatal(err)
	}
	if byName.Total != 1 || len(byName.Items) != 1 || byName.Items[0].PlanningID != 77 || byName.OpenTotal != 2 {
		t.Fatalf("name total=%d open=%d %+v", byName.Total, byName.OpenTotal, byName.Items)
	}
	for _, q := range []string{"6994", "590-69-94", "+7 926 590 69 94", "89265906994"} {
		got, err := svc.DashboardWaitlist(kash, withQuery(base, q))
		if err != nil {
			t.Fatal(err)
		}
		if got.Total != 1 || len(got.Items) != 1 || got.Items[0].PlanningID != 77 {
			t.Fatalf("phone %q total=%d %+v", q, got.Total, got.Items)
		}
	}
	short, err := svc.DashboardWaitlist(kash, withQuery(base, "69"))
	if err != nil {
		t.Fatal(err)
	}
	if short.Total != 0 || len(short.Items) != 0 {
		t.Fatalf("short phone total=%d %+v", short.Total, short.Items)
	}

	missing, err := svc.DashboardWaitlist(kash, withQuery(base, "сидоров"))
	if err != nil {
		t.Fatal(err)
	}
	if missing.Total != 0 || len(missing.Items) != 0 {
		t.Fatalf("missing name total=%d %+v", missing.Total, missing.Items)
	}

	one, err := svc.DashboardWaitlist(kash, withQuery(base, "я"))
	if err != nil {
		t.Fatal(err)
	}
	if one.Total != 2 || one.OpenTotal != 2 {
		t.Fatalf("one letter total=%d open=%d", one.Total, one.OpenTotal)
	}
	var bare *confirmations.DashboardItem
	for i := range one.Items {
		if one.Items[i].PlanningID == 99 {
			bare = &one.Items[i]
		}
	}
	if bare == nil || bare.PatientName != "" || bare.PatientPhone != "" {
		t.Fatalf("missing row %+v", bare)
	}

	mgr := staff.WithPrincipal(context.Background(), staff.Principal{ID: 3, Login: "dir", Role: staff.RoleManager, All: true, BranchIDs: staff.DashboardBranchIDs})
	pair, err := svc.DashboardWaitlist(mgr, confirmations.WaitlistListFilter{BranchIDs: []int{106, 3}, OpenOnly: true, Page: 1, PerPage: 50})
	if err != nil {
		t.Fatal(err)
	}
	if pair.Total != 3 || pair.OpenTotal != 3 {
		t.Fatalf("pair total=%d open=%d", pair.Total, pair.OpenTotal)
	}
	for _, it := range pair.Items {
		if it.BranchID != 106 && it.BranchID != 3 {
			t.Fatalf("branch %d leaked", it.BranchID)
		}
	}
}

func TestExplicitBranches_DropsUnknownBranch(t *testing.T) {
	resetSnapStore()
	prevEnrich, prevMis := loadPageEnrich, loadMis
	t.Cleanup(func() {
		resetSnapStore()
		loadPageEnrich, loadMis = prevEnrich, prevMis
	})
	loadMis = func(ctx context.Context, db *sql.DB, now time.Time, back, ahead int, examIDs []int64) ([]repo.WaitlistMisRow, error) {
		return nil, nil
	}
	loadPageEnrich = func(ctx context.Context, db *sql.DB, planningIDs, patientIDs, exams []int64, now time.Time, answered map[int64]string) (pageEnrichment, error) {
		return pageEnrichment{live: map[int64]repo.WaitlistPlanningLive{
			1: {PlanningID: 1, PatientID: 1, BranchID: 106, PatientName: "Каширка"},
			2: {PlanningID: 2, PatientID: 2, BranchID: 3, PatientName: "Куркино"},
			3: {PlanningID: 3, PatientID: 3, BranchID: 496, PatientName: "Куркино 2"},
			4: {PlanningID: 4, PatientID: 4, BranchID: 0, PatientName: "Без филиала"},
		}}, nil
	}
	sqlite := openWaitlistDB(t, "wl_explicit_branch")
	insertWait(t, sqlite, 1, 106)
	insertWait(t, sqlite, 2, 3)
	insertWait(t, sqlite, 3, 496)
	insertWait(t, sqlite, 4, 0)
	svc := &Services{SQLite: sqlite, MSSQL: sqlite}
	mgr := staff.WithPrincipal(context.Background(), staff.Principal{ID: 3, Login: "dir", Role: staff.RoleManager, All: true, BranchIDs: staff.DashboardBranchIDs})
	pair, err := svc.DashboardWaitlist(mgr, confirmations.WaitlistListFilter{
		BranchIDs: []int{106, 3}, ExplicitBranches: true, OpenOnly: true, Page: 1, PerPage: 50,
	})
	if err != nil {
		t.Fatal(err)
	}
	if pair.Total != 2 || pair.OpenTotal != 2 || len(pair.Items) != 2 {
		t.Fatalf("explicit total=%d open=%d items=%d", pair.Total, pair.OpenTotal, len(pair.Items))
	}
	for _, it := range pair.Items {
		if it.BranchID != 106 && it.BranchID != 3 {
			t.Fatalf("branch %d", it.BranchID)
		}
	}
	all, err := svc.DashboardWaitlist(mgr, confirmations.WaitlistListFilter{
		BranchIDs: staff.DashboardBranchIDs, IncludeOtherBranches: true, OpenOnly: true, Page: 1, PerPage: 50,
	})
	if err != nil {
		t.Fatal(err)
	}
	if all.Total != 4 || all.OpenTotal != 4 || len(all.Items) != 4 {
		t.Fatalf("all total=%d open=%d items=%d", all.Total, all.OpenTotal, len(all.Items))
	}
	kash := staff.WithPrincipal(context.Background(), staff.Principal{ID: 2, Login: "kash", Role: staff.RoleRegistrar, BranchIDs: []int{106}})
	only, err := svc.DashboardWaitlist(kash, confirmations.WaitlistListFilter{
		BranchIDs: []int{106}, OpenOnly: true, Page: 1, PerPage: 50,
	})
	if err != nil {
		t.Fatal(err)
	}
	if only.Total != 1 || only.OpenTotal != 1 || len(only.Items) != 1 || only.Items[0].BranchID != 106 {
		t.Fatalf("registrar %+v total=%d open=%d", only.Items, only.Total, only.OpenTotal)
	}
}

func withQuery(f confirmations.WaitlistListFilter, q string) confirmations.WaitlistListFilter {
	f.Query = q
	return f
}

func insertWait(t *testing.T, db *sql.DB, planning int64, branch int) {
	t.Helper()
	if _, err := db.ExecContext(context.Background(), `
INSERT INTO waitlist(planning_id,patient_id,appt_version,appt_start,reason,source,status,created_at,updated_at,created_at_msk,branch_id)
VALUES(?,?, 'v', '2026-11-06 10:00:00', 'declined', 'max', 'new', '2026-11-06T06:00:00Z', '2026-11-06T06:00:00Z', '2026-11-06 09:00:00', ?)`,
		planning, planning, branch); err != nil {
		t.Fatal(err)
	}
}
