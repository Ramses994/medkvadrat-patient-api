package service

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/medkvadrat/medkvadrat-patient-api/internal/confirmations"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/repo"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/staff"
)

func TestSnapshot_MoveBothSources(t *testing.T) {
	resetMisCache()
	resetEnrichCache()
	prevMis, prevEnrich := loadMis, loadPageEnrich
	t.Cleanup(func() {
		loadMis, loadPageEnrich = prevMis, prevEnrich
		resetMisCache()
		resetEnrichCache()
	})
	loadMis = func(ctx context.Context, db *sql.DB, now time.Time, back, ahead int, examIDs []int64) ([]repo.WaitlistMisRow, error) {
		return []repo.WaitlistMisRow{{
			PlanningID: 880, PatientID: 5, CreatedAt: "2026-11-06 12:00:00", BranchID: 106,
			PatientName: "Реестр", Phone: "79990000002",
		}}, nil
	}
	loadPageEnrich = func(ctx context.Context, db *sql.DB, planningIDs, patientIDs, exams []int64, now time.Time, answered map[int64]string) (pageEnrichment, error) {
		live := map[int64]repo.WaitlistPlanningLive{
			77:  {PlanningID: 77, BranchID: 3, PatientName: "Макс", Phone: "79990000001", DateStart: "2026-11-06 10:00:00"},
			880: {PlanningID: 880, BranchID: 3, PatientName: "Реестр", Phone: "79990000002"},
		}
		return pageEnrichment{live: live, byOrigin: map[int64]repo.WaitlistFutureVisit{}}, nil
	}
	ctx := context.Background()
	db := openWaitlistDB(t, "wl_snap_move")
	insertOpen(t, db, 77, 106, "new")
	var maxID int64
	if err := db.QueryRowContext(ctx, `SELECT id FROM waitlist WHERE planning_id = 77`).Scan(&maxID); err != nil {
		t.Fatal(err)
	}
	svc := &Services{SQLite: db, MSSQL: db}
	kash := staff.WithPrincipal(ctx, staff.Principal{ID: 2, Login: "kash", Role: staff.RoleRegistrar, BranchIDs: []int{106}})
	kurk := staff.WithPrincipal(ctx, staff.Principal{ID: 3, Login: "kurk", Role: staff.RoleRegistrar, BranchIDs: []int{3}})
	first, err := svc.DashboardWaitlist(kurk, confirmations.WaitlistListFilter{BranchIDs: []int{3}, OpenOnly: true, Page: 1, PerPage: 50})
	if err != nil {
		t.Fatal(err)
	}
	if first.Total != 2 || !pageHas(first, "max", 77) || !pageHas(first, "registry", 880) {
		t.Fatalf("kurkino first %+v total=%d", first.Items, first.Total)
	}
	hidden, err := svc.DashboardWaitlist(kash, confirmations.WaitlistListFilter{BranchIDs: []int{106}, OpenOnly: true, Page: 1, PerPage: 50})
	if err != nil {
		t.Fatal(err)
	}
	if hidden.Total != 0 || len(hidden.Items) != 0 {
		t.Fatalf("kashirka still sees %+v", hidden.Items)
	}
	if _, err := svc.PatchWaitlist(kash, maxID, 1, "call_back", "", "kash"); !errors.Is(err, confirmations.ErrWaitlistMoved) {
		t.Fatalf("max patch: %v", err)
	}
	if _, err := svc.PatchRegistryWaitlist(kash, 880, 1, "call_back", "", "kash"); !errors.Is(err, confirmations.ErrWaitlistMoved) {
		t.Fatalf("registry patch: %v", err)
	}
	again, err := svc.DashboardWaitlist(kash, confirmations.WaitlistListFilter{BranchIDs: []int{106}, OpenOnly: true, Page: 1, PerPage: 50})
	if err != nil {
		t.Fatal(err)
	}
	if again.Total != 0 || len(again.Items) != 0 {
		t.Fatalf("next poll %+v", again.Items)
	}
}

func TestSnapshot_ReadFailureAfterTTL(t *testing.T) {
	resetMisCache()
	resetEnrichCache()
	prevMis, prevEnrich, prevNow := loadMis, loadPageEnrich, misNow
	t.Cleanup(func() {
		loadMis, loadPageEnrich, misNow = prevMis, prevEnrich, prevNow
		resetMisCache()
		resetEnrichCache()
	})
	clock := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	misNow = func() time.Time { return clock }
	fail := false
	loadMis = func(ctx context.Context, db *sql.DB, now time.Time, back, ahead int, examIDs []int64) ([]repo.WaitlistMisRow, error) {
		if fail {
			return nil, errors.New("medialog down")
		}
		return []repo.WaitlistMisRow{{
			PlanningID: 880, PatientID: 5, CreatedAt: "2026-11-06 12:00:00", BranchID: 106,
			PatientName: "СекретРеестра", Phone: "79991112233",
		}}, nil
	}
	loadPageEnrich = func(ctx context.Context, db *sql.DB, planningIDs, patientIDs, exams []int64, now time.Time, answered map[int64]string) (pageEnrichment, error) {
		if fail {
			return pageEnrichment{}, errors.New("medialog down")
		}
		return pageEnrichment{live: map[int64]repo.WaitlistPlanningLive{
			4:   {PlanningID: 4, BranchID: 106, PatientName: "СекретМакс", Phone: "79990001122", DateStart: "2026-11-06 10:00:00"},
			880: {PlanningID: 880, BranchID: 106, PatientName: "СекретРеестра", Phone: "79991112233"},
		}}, nil
	}
	ctx := context.Background()
	db := openWaitlistDB(t, "wl_snap_down")
	insertAt(t, db, 4, "2026-11-06T08:00:00Z")
	svc := &Services{SQLite: db, MSSQL: db}
	kash := staff.WithPrincipal(ctx, staff.Principal{ID: 2, Login: "kash", Role: staff.RoleRegistrar, BranchIDs: []int{106}})
	filter := confirmations.WaitlistListFilter{BranchIDs: []int{106}, OpenOnly: true, Page: 1, PerPage: 50}
	first, err := svc.DashboardWaitlist(kash, filter)
	if err != nil {
		t.Fatal(err)
	}
	if first.Total != 2 || !pageHasPhone(first, "79990001122") || !pageHasPhone(first, "79991112233") {
		t.Fatalf("first %+v total=%d", first.Items, first.Total)
	}
	fail = true
	clock = clock.Add(31 * time.Second)
	second, err := svc.DashboardWaitlist(kash, filter)
	if err != nil {
		t.Fatal(err)
	}
	if second.Banner != "Медиалог недоступен" || second.Total != 1 || len(second.Items) != 1 || second.Items[0].Source != "max" || second.Items[0].PatientPhone != "" {
		t.Fatalf("after ttl %+v total=%d banner=%q", second.Items, second.Total, second.Banner)
	}
	if misCacheHoldsName("СекретРеестра") || enrichHoldsName("СекретМакс") {
		t.Fatal("failed refresh kept a name")
	}
}

func TestSnapshot_UnknownBranch(t *testing.T) {
	resetEnrichCache()
	prev, prevMis := loadPageEnrich, loadMis
	t.Cleanup(func() {
		loadPageEnrich, loadMis = prev, prevMis
		resetEnrichCache()
	})
	loadMis = func(ctx context.Context, db *sql.DB, now time.Time, back, ahead int, examIDs []int64) ([]repo.WaitlistMisRow, error) {
		return nil, nil
	}
	loadPageEnrich = func(ctx context.Context, db *sql.DB, planningIDs, patientIDs, exams []int64, now time.Time, answered map[int64]string) (pageEnrichment, error) {
		return pageEnrichment{live: map[int64]repo.WaitlistPlanningLive{
			82: {PlanningID: 82, BranchID: 999, PatientName: "ЧужойФилиал", Phone: "79990000999"},
		}}, nil
	}
	ctx := context.Background()
	db := openWaitlistDB(t, "wl_snap_unknown")
	insertOpen(t, db, 82, 106, "new")
	var id int64
	if err := db.QueryRowContext(ctx, `SELECT id FROM waitlist`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	svc := &Services{SQLite: db, MSSQL: db}
	kash := staff.WithPrincipal(ctx, staff.Principal{ID: 2, Login: "kash", Role: staff.RoleRegistrar, BranchIDs: []int{106}})
	admin := staff.WithPrincipal(ctx, staff.Principal{ID: 1, Login: "admin", Role: staff.RoleAdmin, All: true})
	seen, err := svc.DashboardWaitlist(admin, confirmations.WaitlistListFilter{BranchIDs: []int{106, 3, 496}, OpenOnly: true, Page: 1, PerPage: 50, Source: "max"})
	if err != nil {
		t.Fatal(err)
	}
	if seen.Total != 1 || seen.Items[0].BranchID != 999 || seen.Items[0].PatientPhone != "79990000999" {
		t.Fatalf("admin %+v", seen.Items)
	}
	hidden, err := svc.DashboardWaitlist(kash, confirmations.WaitlistListFilter{BranchIDs: []int{106}, OpenOnly: true, Page: 1, PerPage: 50, Source: "max"})
	if err != nil {
		t.Fatal(err)
	}
	if hidden.Total != 0 {
		t.Fatalf("registrar total=%d", hidden.Total)
	}
	if _, err := svc.PatchWaitlist(kash, id, 1, "call_back", "", "kash"); !errors.Is(err, confirmations.ErrWaitlistMoved) {
		t.Fatalf("patch: %v", err)
	}
}

func TestSnapshot_MissingOriginal(t *testing.T) {
	resetMisCache()
	resetEnrichCache()
	prevMis, prevEnrich := loadMis, loadPageEnrich
	t.Cleanup(func() {
		loadMis, loadPageEnrich = prevMis, prevEnrich
		resetMisCache()
		resetEnrichCache()
	})
	loadMis = func(ctx context.Context, db *sql.DB, now time.Time, back, ahead int, examIDs []int64) ([]repo.WaitlistMisRow, error) {
		return nil, nil
	}
	loadPageEnrich = func(ctx context.Context, db *sql.DB, planningIDs, patientIDs, exams []int64, now time.Time, answered map[int64]string) (pageEnrichment, error) {
		return pageEnrichment{live: map[int64]repo.WaitlistPlanningLive{}}, nil
	}
	db := openWaitlistDB(t, "wl_snap_missing")
	insertAt(t, db, 15, "2026-11-06T08:00:00Z")
	svc := &Services{SQLite: db, MSSQL: db}
	page, err := svc.DashboardWaitlist(context.Background(), confirmations.WaitlistListFilter{OpenOnly: true, Page: 1, PerPage: 10})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 1 || len(page.Items) != 1 || page.Items[0].Source != "max" || page.Items[0].PatientPhone != "" || page.Items[0].MedialogState != "запись не найдена в Медиалоге" || page.Items[0].Enriched {
		t.Fatalf("%+v total=%d", page.Items, page.Total)
	}
}

func TestSnapshot_FuturesByOrigin(t *testing.T) {
	resetEnrichCache()
	prev, prevMis := loadPageEnrich, loadMis
	t.Cleanup(func() {
		loadPageEnrich, loadMis = prev, prevMis
		resetEnrichCache()
	})
	loadMis = func(ctx context.Context, db *sql.DB, now time.Time, back, ahead int, examIDs []int64) ([]repo.WaitlistMisRow, error) {
		return nil, nil
	}
	if !repo.VisitCreatedAfter("2026-11-10T09:00:00", "2026-11-10 18:00:00") {
		t.Fatal("same-day later visit must stay")
	}
	if confirmations.MedialogCreateIsNewer("2026-11-10T09:00:00", "2026-11-10 18:00:00") {
		t.Fatal("string compare hides the later same-day visit")
	}
	loadPageEnrich = func(ctx context.Context, db *sql.DB, planningIDs, patientIDs, exams []int64, now time.Time, answered map[int64]string) (pageEnrichment, error) {
		futures := []repo.WaitlistFutureVisit{
			{OriginID: 10, PlanningID: 100, PatientID: 1, CreatedAt: "2026-11-07 10:00:00", DateStart: "2026-11-10 09:00:00", BranchID: 106},
			{OriginID: 11, PlanningID: 101, PatientID: 1, CreatedAt: "2026-11-07 11:00:00", DateStart: "2026-11-11 09:00:00", BranchID: 106},
		}
		return pageEnrichment{
			live: map[int64]repo.WaitlistPlanningLive{
				10: {PlanningID: 10, BranchID: 106, PatientName: "Один", DateStart: "2026-11-06 10:00:00"},
				11: {PlanningID: 11, BranchID: 106, PatientName: "Один", DateStart: "2026-11-06 10:00:00"},
			},
			byOrigin: futuresByOrigin(futures),
		}, nil
	}
	ctx := context.Background()
	db := openWaitlistDB(t, "wl_snap_future")
	insertOpen(t, db, 10, 106, "new")
	insertOpen(t, db, 11, 106, "new")
	if _, err := db.ExecContext(ctx, `UPDATE waitlist SET patient_id = 1`); err != nil {
		t.Fatal(err)
	}
	svc := &Services{SQLite: db, MSSQL: db}
	page, err := svc.DashboardWaitlist(ctx, confirmations.WaitlistListFilter{Source: "max", OpenOnly: true, Page: 1, PerPage: 10})
	if err != nil {
		t.Fatal(err)
	}
	got := map[int64]string{}
	for _, it := range page.Items {
		got[it.PlanningID] = it.FutureDateStart
	}
	if got[10] != "2026-11-10 09:00:00" || got[11] != "2026-11-11 09:00:00" {
		t.Fatalf("%+v", page.Items)
	}
}

func TestSnapshot_RegistrySameDayFuture(t *testing.T) {
	resetMisCache()
	resetEnrichCache()
	prevMis, prevEnrich := loadMis, loadPageEnrich
	t.Cleanup(func() {
		loadMis, loadPageEnrich = prevMis, prevEnrich
		resetMisCache()
		resetEnrichCache()
	})
	loadMis = func(ctx context.Context, db *sql.DB, now time.Time, back, ahead int, examIDs []int64) ([]repo.WaitlistMisRow, error) {
		return []repo.WaitlistMisRow{{
			PlanningID: 50, PatientID: 1, CreatedAt: "2026-11-10T09:00:00", BranchID: 106, PatientName: "Реестр",
		}}, nil
	}
	loadPageEnrich = func(ctx context.Context, db *sql.DB, planningIDs, patientIDs, exams []int64, now time.Time, answered map[int64]string) (pageEnrichment, error) {
		fut := repo.WaitlistFutureVisit{
			OriginID: 50, PlanningID: 51, PatientID: 1, CreatedAt: "2026-11-10 18:00:00",
			DateStart: "2026-11-10 18:30:00", BranchID: 106,
		}
		return pageEnrichment{
			live:     map[int64]repo.WaitlistPlanningLive{50: {PlanningID: 50, BranchID: 106, PatientName: "Реестр"}},
			byOrigin: futuresByOrigin([]repo.WaitlistFutureVisit{fut}),
		}, nil
	}
	db := openWaitlistDB(t, "wl_snap_reg_future")
	svc := &Services{SQLite: db, MSSQL: db}
	page, err := svc.DashboardWaitlist(context.Background(), confirmations.WaitlistListFilter{Source: "registry", Page: 1, PerPage: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.Items[0].FutureDateStart != "2026-11-10 18:30:00" {
		t.Fatalf("%+v", page.Items)
	}
}

func TestSnapshot_IdleHourDropsNames(t *testing.T) {
	resetMisCache()
	resetEnrichCache()
	prevMis, prevEnrich, prevNow := loadMis, loadPageEnrich, misNow
	t.Cleanup(func() {
		loadMis, loadPageEnrich, misNow = prevMis, prevEnrich, prevNow
		resetMisCache()
		resetEnrichCache()
	})
	clock := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	misNow = func() time.Time { return clock }
	loadMis = func(ctx context.Context, db *sql.DB, now time.Time, back, ahead int, examIDs []int64) ([]repo.WaitlistMisRow, error) {
		return []repo.WaitlistMisRow{{
			PlanningID: 70, PatientID: 1, CreatedAt: "2026-11-06 12:00:00", BranchID: 106, PatientName: "СекретЧас",
		}}, nil
	}
	loadPageEnrich = func(ctx context.Context, db *sql.DB, planningIDs, patientIDs, exams []int64, now time.Time, answered map[int64]string) (pageEnrichment, error) {
		return pageEnrichment{live: map[int64]repo.WaitlistPlanningLive{
			4: {PlanningID: 4, BranchID: 106, PatientName: "СекретМаксЧас"},
		}}, nil
	}
	db := openWaitlistDB(t, "wl_snap_idle")
	insertAt(t, db, 4, "2026-11-06T08:00:00Z")
	svc := &Services{SQLite: db, MSSQL: db}
	if _, err := svc.DashboardWaitlist(context.Background(), confirmations.WaitlistListFilter{OpenOnly: true, Page: 1, PerPage: 10}); err != nil {
		t.Fatal(err)
	}
	if !misCacheHoldsName("СекретЧас") || !enrichHoldsName("СекретМаксЧас") {
		t.Fatal("snapshot did not keep the names")
	}
	clock = clock.Add(time.Hour)
	sweepDashboardCaches(clock)
	if misCacheHoldsName("СекретЧас") || enrichHoldsName("СекретМаксЧас") {
		t.Fatal("idle hour left personal data in memory")
	}
}

func TestSnapshot_OpenSetReloadsTogether(t *testing.T) {
	resetMisCache()
	resetEnrichCache()
	prevMis, prevEnrich, prevNow := loadMis, loadPageEnrich, misNow
	t.Cleanup(func() {
		loadMis, loadPageEnrich, misNow = prevMis, prevEnrich, prevNow
		resetMisCache()
		resetEnrichCache()
	})
	clock := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	misNow = func() time.Time { return clock }
	var misCalls, enrichCalls int
	loadMis = func(ctx context.Context, db *sql.DB, now time.Time, back, ahead int, examIDs []int64) ([]repo.WaitlistMisRow, error) {
		misCalls++
		return []repo.WaitlistMisRow{{
			PlanningID: 880, PatientID: 5, CreatedAt: "2026-11-06 12:00:00", BranchID: 106, PatientName: "Реестр",
		}}, nil
	}
	loadPageEnrich = func(ctx context.Context, db *sql.DB, planningIDs, patientIDs, exams []int64, now time.Time, answered map[int64]string) (pageEnrichment, error) {
		enrichCalls++
		return pageEnrichment{live: map[int64]repo.WaitlistPlanningLive{
			77: {PlanningID: 77, BranchID: 106, PatientName: "Макс"},
		}}, nil
	}
	db := openWaitlistDB(t, "wl_snap_generation")
	insertOpen(t, db, 77, 106, "new")
	svc := &Services{SQLite: db, MSSQL: db}
	ctx := context.Background()
	filter := confirmations.WaitlistListFilter{OpenOnly: true, Page: 1, PerPage: 50}
	if _, err := svc.DashboardWaitlist(ctx, filter); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.DashboardWaitlist(ctx, filter); err != nil {
		t.Fatal(err)
	}
	if misCalls != 1 || enrichCalls != 1 {
		t.Fatalf("within ttl mis=%d enrich=%d", misCalls, enrichCalls)
	}
	insertOpen(t, db, 78, 106, "new")
	again, err := svc.DashboardWaitlist(ctx, filter)
	if err != nil {
		t.Fatal(err)
	}
	if misCalls != 1 || enrichCalls != 1 {
		t.Fatalf("new row before ttl mis=%d enrich=%d", misCalls, enrichCalls)
	}
	var fresh confirmations.DashboardItem
	for _, it := range again.Items {
		if it.PlanningID == 78 {
			fresh = it
		}
	}
	if fresh.PlanningID != 78 || fresh.PatientName != "" || fresh.MedialogState != "запись не найдена в Медиалоге" {
		t.Fatalf("row created after the snapshot %+v", fresh)
	}
}

func pageHas(page confirmations.DashboardPage, source string, planning int64) bool {
	for _, it := range page.Items {
		if it.Source == source && it.PlanningID == planning {
			return true
		}
	}
	return false
}

func pageHasPhone(page confirmations.DashboardPage, phone string) bool {
	for _, it := range page.Items {
		if it.PatientPhone == phone {
			return true
		}
	}
	return false
}
