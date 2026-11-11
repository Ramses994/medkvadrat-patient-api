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
	prevEnrich, prevMis, prevNow := loadPageEnrich, loadMis, misNow
	t.Cleanup(func() {
		resetSnapStore()
		loadPageEnrich, loadMis, misNow = prevEnrich, prevMis, prevNow
	})
	loadMis = func(ctx context.Context, db *sql.DB, now time.Time, back, ahead int, examIDs []int64) ([]repo.WaitlistMisRow, error) {
		return nil, nil
	}
	loadPageEnrich = func(ctx context.Context, db *sql.DB, planningIDs, patientIDs, exams []int64, now time.Time, answered map[int64]string) (pageEnrichment, error) {
		return pageEnrichment{live: map[int64]repo.WaitlistPlanningLive{
			77:  {PlanningID: 77, PatientID: 77, BranchID: 106, PatientName: "Ёлкин Иван", Phone: "79161234567"},
			88:  {PlanningID: 88, PatientID: 88, BranchID: 3, PatientName: "Кузнецов", Phone: "79990000088"},
			100: {PlanningID: 100, PatientID: 100, BranchID: 496, PatientName: "Петров", Phone: "79990000100"},
		}}, nil
	}
	db := openWaitlistDB(t, "wl_search_ux")
	insertNamed(t, db, 77, 106, "", "")
	insertNamed(t, db, 88, 3, "", "")
	insertNamed(t, db, 99, 106, "Сидоров Пётр", "79001112233")
	insertNamed(t, db, 100, 496, "", "")
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
	for _, q := range []string{"89161234567", "+7 916 123-45-67", "9161234567"} {
		got, err := svc.DashboardWaitlist(kash, withQuery(base, q))
		if err != nil {
			t.Fatal(err)
		}
		if got.Total != 1 || len(got.Items) != 1 || got.Items[0].PlanningID != 77 {
			t.Fatalf("phone %q total=%d %+v", q, got.Total, got.Items)
		}
	}

	sqliteHit, err := svc.DashboardWaitlist(kash, withQuery(base, "сидоров"))
	if err != nil {
		t.Fatal(err)
	}
	if sqliteHit.Total != 1 || len(sqliteHit.Items) != 1 || sqliteHit.Items[0].PlanningID != 99 || sqliteHit.Items[0].PatientName != "Сидоров Пётр" {
		t.Fatalf("sqlite %+v total=%d", sqliteHit.Items, sqliteHit.Total)
	}

	one, err := svc.DashboardWaitlist(kash, withQuery(base, "я"))
	if err != nil {
		t.Fatal(err)
	}
	if one.Total != 2 || one.OpenTotal != 2 {
		t.Fatalf("one letter total=%d open=%d", one.Total, one.OpenTotal)
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

func withQuery(f confirmations.WaitlistListFilter, q string) confirmations.WaitlistListFilter {
	f.Query = q
	return f
}

func insertNamed(t *testing.T, db *sql.DB, planning int64, branch int, name, phone string) {
	t.Helper()
	if _, err := db.ExecContext(context.Background(), `
INSERT INTO waitlist(planning_id,patient_id,appt_version,appt_start,reason,source,status,created_at,updated_at,created_at_msk,branch_id,patient_name,patient_phone)
VALUES(?,?, 'v', '2026-11-06 10:00:00', 'declined', 'max', 'new', '2026-11-06T06:00:00Z', '2026-11-06T06:00:00Z', '2026-11-06 09:00:00', ?, ?, ?)`,
		planning, planning, branch, name, phone); err != nil {
		t.Fatal(err)
	}
}
