package service

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/medkvadrat/medkvadrat-patient-api/internal/confirmations"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/handler"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/repo"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/staff"
)

func TestSnapshot_RegistryPatientMismatchHidden(t *testing.T) {
	page, err := registryDisagree(t, "wl_reg_patient", repo.WaitlistPlanningLive{
		PlanningID: 50, PatientID: 9, BranchID: 3, PatientName: "Чужой", Phone: "79990000050", IsWaitlist: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 0 || page.OpenTotal != 0 || len(page.Items) != 0 {
		t.Fatalf("mismatch leaked %+v total=%d open=%d", page.Items, page.Total, page.OpenTotal)
	}
}

func TestSnapshot_RegistryCancelledHidden(t *testing.T) {
	page, err := registryDisagree(t, "wl_reg_cancel", repo.WaitlistPlanningLive{
		PlanningID: 50, PatientID: 1, BranchID: 106, Cancelled: 1, PatientName: "Отменён", Phone: "79990000051", IsWaitlist: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 0 || page.OpenTotal != 0 || len(page.Items) != 0 {
		t.Fatalf("cancelled leaked %+v total=%d open=%d", page.Items, page.Total, page.OpenTotal)
	}
}

func registryDisagree(t *testing.T, name string, live repo.WaitlistPlanningLive) (confirmations.DashboardPage, error) {
	t.Helper()
	resetSnapStore()
	prevMis, prevEnrich := loadMis, loadPageEnrich
	t.Cleanup(func() {
		loadMis, loadPageEnrich = prevMis, prevEnrich
		resetSnapStore()
	})
	loadMis = func(ctx context.Context, db *sql.DB, now time.Time, back, ahead int, examIDs []int64) ([]repo.WaitlistMisRow, error) {
		return []repo.WaitlistMisRow{{
			PlanningID: 50, PatientID: 1, BranchID: 106, CreatedAt: "2026-11-06 12:00:00",
			Comment: "комментарий окна", CreatorName: "Автор окна",
		}}, nil
	}
	loadPageEnrich = func(ctx context.Context, db *sql.DB, planningIDs, patientIDs, exams []int64, now time.Time, answered map[int64]string) (pageEnrichment, error) {
		return pageEnrichment{live: map[int64]repo.WaitlistPlanningLive{50: live}}, nil
	}
	db := openWaitlistDB(t, name)
	svc := &Services{SQLite: db, MSSQL: db}
	ctx := context.Background()
	kash := staff.WithPrincipal(ctx, staff.Principal{ID: 2, Login: "kash", Role: staff.RoleRegistrar, BranchIDs: []int{106}})
	admin := staff.WithPrincipal(ctx, staff.Principal{ID: 1, Login: "admin", Role: staff.RoleAdmin, All: true})
	filter := confirmations.WaitlistListFilter{OpenOnly: true, Page: 1, PerPage: 50}
	if _, err := svc.PatchRegistryWaitlist(kash, 50, 1, "call_back", "", "kash"); !errors.Is(err, confirmations.ErrWaitlistNotFound) {
		t.Fatalf("registrar patch: %v", err)
	}
	if _, err := svc.PatchRegistryWaitlist(admin, 50, 1, "call_back", "", "admin"); !errors.Is(err, confirmations.ErrWaitlistNotFound) {
		t.Fatalf("admin patch: %v", err)
	}
	return svc.DashboardWaitlist(admin, filter)
}

func TestSnapshot_MaxPatientMismatchKeepsSavedBranch(t *testing.T) {
	resetSnapStore()
	prevMis, prevEnrich := loadMis, loadPageEnrich
	t.Cleanup(func() {
		loadMis, loadPageEnrich = prevMis, prevEnrich
		resetSnapStore()
	})
	loadMis = func(ctx context.Context, db *sql.DB, now time.Time, back, ahead int, examIDs []int64) ([]repo.WaitlistMisRow, error) {
		return nil, nil
	}
	loadPageEnrich = func(ctx context.Context, db *sql.DB, planningIDs, patientIDs, exams []int64, now time.Time, answered map[int64]string) (pageEnrichment, error) {
		return pageEnrichment{live: map[int64]repo.WaitlistPlanningLive{
			77: {PlanningID: 77, PatientID: 88, BranchID: 3, PatientName: "Чужой", Phone: "79990000077"},
		}}, nil
	}
	db := openWaitlistDB(t, "wl_max_patient")
	insertOpen(t, db, 77, 106, "new")
	svc := &Services{SQLite: db, MSSQL: db}
	ctx := staff.WithPrincipal(context.Background(), staff.Principal{ID: 2, Login: "kash", Role: staff.RoleRegistrar, BranchIDs: []int{106}})
	page, err := svc.DashboardWaitlist(ctx, confirmations.WaitlistListFilter{BranchIDs: []int{106}, OpenOnly: true, Page: 1, PerPage: 50, Source: "max"})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 1 || len(page.Items) != 1 || page.Items[0].PatientName != "" || page.Items[0].PatientPhone != "" || page.Items[0].Enriched || page.Items[0].MedialogState != "запись не найдена в Медиалоге" {
		t.Fatalf("%+v", page.Items)
	}
	var branch int
	if err := db.QueryRowContext(context.Background(), `SELECT branch_id FROM waitlist`).Scan(&branch); err != nil {
		t.Fatal(err)
	}
	if branch != 106 {
		t.Fatalf("branch=%d", branch)
	}
}

func TestSnapshot_ExpiredBeforeServeDropsPhone(t *testing.T) {
	resetSnapStore()
	prevMis, prevEnrich, prevHook := loadMis, loadPageEnrich, beforeDashboardServe
	t.Cleanup(func() {
		loadMis, loadPageEnrich, beforeDashboardServe = prevMis, prevEnrich, prevHook
		resetSnapStore()
	})
	clock := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	setMisNow(t, func() time.Time { return clock })
	loadMis = func(ctx context.Context, db *sql.DB, now time.Time, back, ahead int, examIDs []int64) ([]repo.WaitlistMisRow, error) {
		return nil, nil
	}
	loadPageEnrich = func(ctx context.Context, db *sql.DB, planningIDs, patientIDs, exams []int64, now time.Time, answered map[int64]string) (pageEnrichment, error) {
		return pageEnrichment{live: map[int64]repo.WaitlistPlanningLive{
			77: {PlanningID: 77, PatientID: 77, BranchID: 106, PatientName: "Макс", Phone: "79990000001"},
		}}, nil
	}
	beforeDashboardServe = func() { clock = clock.Add(30100 * time.Millisecond) }
	db := openWaitlistDB(t, "wl_expire_serve")
	insertOpen(t, db, 77, 106, "new")
	svc := &Services{SQLite: db, MSSQL: db}
	page, err := svc.DashboardWaitlist(context.Background(), confirmations.WaitlistListFilter{Source: "max", OpenOnly: true, Page: 1, PerPage: 50})
	if err != nil {
		t.Fatal(err)
	}
	if page.Banner != "Медиалог недоступен" || len(page.Items) != 1 || page.Items[0].PatientPhone != "" {
		t.Fatalf("banner=%q %+v", page.Banner, page.Items)
	}
}

func TestSnapshot_ExpiredRegistryPatchDoesNotWrite(t *testing.T) {
	resetSnapStore()
	prevMis, prevEnrich, prevHook := loadMis, loadPageEnrich, afterWriteLock
	t.Cleanup(func() {
		loadMis, loadPageEnrich, afterWriteLock = prevMis, prevEnrich, prevHook
		resetSnapStore()
	})
	clock := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	setMisNow(t, func() time.Time { return clock })
	loadMis = func(ctx context.Context, db *sql.DB, now time.Time, back, ahead int, examIDs []int64) ([]repo.WaitlistMisRow, error) {
		return []repo.WaitlistMisRow{{
			PlanningID: 50, PatientID: 1, BranchID: 106, CreatedAt: "2026-11-06 12:00:00",
		}}, nil
	}
	loadPageEnrich = func(ctx context.Context, db *sql.DB, planningIDs, patientIDs, exams []int64, now time.Time, answered map[int64]string) (pageEnrichment, error) {
		return pageEnrichment{live: map[int64]repo.WaitlistPlanningLive{
			50: {PlanningID: 50, PatientID: 1, BranchID: 106, PatientName: "Реестр", Phone: "79990000050", IsWaitlist: true},
		}}, nil
	}
	afterWriteLock = func() { clock = clock.Add(31 * time.Second) }
	db := openWaitlistDB(t, "wl_expire_patch")
	svc := &Services{SQLite: db, MSSQL: db}
	_, err := svc.PatchRegistryWaitlist(context.Background(), 50, 1, "call_back", "", "admin")
	if !errors.Is(err, confirmations.ErrWaitlistNotFound) {
		t.Fatalf("patch: %v", err)
	}
	var n int
	if err := db.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM waitlist_mis_work`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("mis work rows=%d", n)
	}
}

func TestSnapshot_SweepBeforePersistDoesNotWriteBranch(t *testing.T) {
	resetSnapStore()
	prevMis, prevEnrich, prevHook := loadMis, loadPageEnrich, beforePersistBranches
	t.Cleanup(func() {
		loadMis, loadPageEnrich, beforePersistBranches = prevMis, prevEnrich, prevHook
		resetSnapStore()
	})
	clock := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	setMisNow(t, func() time.Time { return clock })
	loadMis = func(ctx context.Context, db *sql.DB, now time.Time, back, ahead int, examIDs []int64) ([]repo.WaitlistMisRow, error) {
		return nil, nil
	}
	loadPageEnrich = func(ctx context.Context, db *sql.DB, planningIDs, patientIDs, exams []int64, now time.Time, answered map[int64]string) (pageEnrichment, error) {
		return pageEnrichment{live: map[int64]repo.WaitlistPlanningLive{
			77: {PlanningID: 77, PatientID: 77, BranchID: 3, PatientName: "Макс", Phone: "79990000001"},
		}}, nil
	}
	beforePersistBranches = func() { sweepDashboardCaches(clock.Add(31 * time.Second)) }
	db := openWaitlistDB(t, "wl_sweep_persist")
	insertOpen(t, db, 77, 106, "new")
	svc := &Services{SQLite: db, MSSQL: db}
	if _, err := svc.DashboardWaitlist(context.Background(), confirmations.WaitlistListFilter{Source: "max", OpenOnly: true, Page: 1, PerPage: 10}); err != nil {
		t.Fatal(err)
	}
	var branch int
	if err := db.QueryRowContext(context.Background(), `SELECT branch_id FROM waitlist`).Scan(&branch); err != nil {
		t.Fatal(err)
	}
	if branch != 106 {
		t.Fatalf("swept snapshot wrote branch=%d", branch)
	}
}

func TestSnapshot_OverCapBeforeMedialog(t *testing.T) {
	resetSnapStore()
	prevMis, prevEnrich := loadMis, loadPageEnrich
	t.Cleanup(func() {
		loadMis, loadPageEnrich = prevMis, prevEnrich
		resetSnapStore()
	})
	var misCalls, enrichCalls atomic.Int32
	loadMis = func(ctx context.Context, db *sql.DB, now time.Time, back, ahead int, examIDs []int64) ([]repo.WaitlistMisRow, error) {
		misCalls.Add(1)
		return nil, nil
	}
	loadPageEnrich = func(ctx context.Context, db *sql.DB, planningIDs, patientIDs, exams []int64, now time.Time, answered map[int64]string) (pageEnrichment, error) {
		enrichCalls.Add(1)
		return pageEnrichment{}, nil
	}
	db := openWaitlistDB(t, "wl_over_cap")
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= dashboardRowCap+1; i++ {
		if _, err := tx.Exec(`
INSERT INTO waitlist(planning_id,patient_id,appt_version,appt_start,reason,source,status,created_at,updated_at,created_at_msk,branch_id)
VALUES(?,?, 'v', '2026-11-06 10:00:00', 'declined', 'max', 'new', '2026-11-06T08:00:00Z', '2026-11-06T08:00:00Z', '2026-11-06 11:00:00', 106)`, i, i); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	var id int64
	if err := db.QueryRowContext(context.Background(), `SELECT id FROM waitlist WHERE planning_id = 1`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	svc := &Services{SQLite: db, MSSQL: db}
	ctx := context.Background()
	if _, err := svc.DashboardWaitlist(ctx, confirmations.WaitlistListFilter{OpenOnly: true, Page: 1, PerPage: 50}); !errors.Is(err, confirmations.ErrWaitlistOverloaded) {
		t.Fatalf("get: %v", err)
	}
	if _, err := svc.PatchWaitlist(ctx, id, 1, "call_back", "", "admin"); !errors.Is(err, confirmations.ErrWaitlistOverloaded) {
		t.Fatalf("patch max: %v", err)
	}
	if _, err := svc.PatchRegistryWaitlist(ctx, 50, 1, "call_back", "", "admin"); !errors.Is(err, confirmations.ErrWaitlistOverloaded) {
		t.Fatalf("patch registry: %v", err)
	}
	if misCalls.Load() != 0 || enrichCalls.Load() != 0 {
		t.Fatalf("mis=%d enrich=%d", misCalls.Load(), enrichCalls.Load())
	}
	var status string
	var branch int
	if err := db.QueryRowContext(ctx, `SELECT status, branch_id FROM waitlist WHERE id = ?`, id).Scan(&status, &branch); err != nil {
		t.Fatal(err)
	}
	if status != "new" || branch != 106 {
		t.Fatalf("status=%s branch=%d", status, branch)
	}
	h := handler.WaitlistHandler{Svc: svc}
	get := httptest.NewRequest(http.MethodGet, "/dashboard/waitlist?status=open", nil)
	gw := httptest.NewRecorder()
	h.List(gw, get)
	if gw.Code != http.StatusServiceUnavailable {
		t.Fatalf("list status=%d", gw.Code)
	}
	patch := httptest.NewRequest(http.MethodPatch, "/dashboard/waitlist/max/"+strconv.FormatInt(id, 10), strings.NewReader(`{"outcome":"call_back","version":1}`))
	patch.SetPathValue("source", "max")
	patch.SetPathValue("id", strconv.FormatInt(id, 10))
	pw := httptest.NewRecorder()
	h.Patch(pw, patch.WithContext(ctx))
	if pw.Code != http.StatusServiceUnavailable {
		t.Fatalf("patch status=%d body=%s", pw.Code, pw.Body.String())
	}
}

func TestSnapshot_DownReadIsCached(t *testing.T) {
	resetSnapStore()
	prevMis := loadMis
	t.Cleanup(func() {
		loadMis = prevMis
		resetSnapStore()
	})
	clock := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	setMisNow(t, func() time.Time { return clock })
	var calls atomic.Int32
	loadMis = func(ctx context.Context, db *sql.DB, now time.Time, back, ahead int, examIDs []int64) ([]repo.WaitlistMisRow, error) {
		calls.Add(1)
		return nil, errors.New("medialog down")
	}
	db := openWaitlistDB(t, "wl_down_cache")
	svc := &Services{SQLite: db, MSSQL: db}
	filter := confirmations.WaitlistListFilter{Source: "max", OpenOnly: true, Page: 1, PerPage: 10}
	for i := 0; i < 3; i++ {
		page, err := svc.DashboardWaitlist(context.Background(), filter)
		if err != nil {
			t.Fatal(err)
		}
		if page.Banner != "Медиалог недоступен" {
			t.Fatalf("banner=%q", page.Banner)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("calls=%d", calls.Load())
	}
	clock = clock.Add(11 * time.Second)
	if _, err := svc.DashboardWaitlist(context.Background(), filter); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 {
		t.Fatalf("after 10s calls=%d", calls.Load())
	}
}

func TestSnapshot_LeaderCancelStillServesWaiter(t *testing.T) {
	resetSnapStore()
	prevMis, prevEnrich := loadMis, loadPageEnrich
	t.Cleanup(func() {
		loadMis, loadPageEnrich = prevMis, prevEnrich
		resetSnapStore()
	})
	started := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	var calls atomic.Int32
	loadMis = func(ctx context.Context, db *sql.DB, now time.Time, back, ahead int, examIDs []int64) ([]repo.WaitlistMisRow, error) {
		calls.Add(1)
		once.Do(func() { close(started) })
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-release:
		}
		return nil, nil
	}
	loadPageEnrich = func(ctx context.Context, db *sql.DB, planningIDs, patientIDs, exams []int64, now time.Time, answered map[int64]string) (pageEnrichment, error) {
		return pageEnrichment{live: map[int64]repo.WaitlistPlanningLive{
			77: {PlanningID: 77, PatientID: 77, BranchID: 106, PatientName: "Ждёт", Phone: "79990000077"},
		}}, nil
	}
	db := openWaitlistDB(t, "wl_cancel_leader")
	insertOpen(t, db, 77, 106, "new")
	svc := &Services{SQLite: db, MSSQL: db}
	filter := confirmations.WaitlistListFilter{Source: "max", OpenOnly: true, Page: 1, PerPage: 10}
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		_, _ = svc.DashboardWaitlist(ctx, filter)
	}()
	<-started
	cancel()
	type result struct {
		page confirmations.DashboardPage
		err  error
	}
	waiter := make(chan result, 1)
	go func() {
		page, err := svc.DashboardWaitlist(context.Background(), filter)
		waiter <- result{page, err}
	}()
	time.Sleep(50 * time.Millisecond)
	close(release)
	select {
	case got := <-waiter:
		if got.err != nil {
			t.Fatal(got.err)
		}
		if calls.Load() != 1 || len(got.page.Items) != 1 || got.page.Items[0].PatientPhone != "79990000077" {
			t.Fatalf("calls=%d %+v", calls.Load(), got.page.Items)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("waiter did not receive the snapshot")
	}
}

// TestSnapshot_FoundZeroHiddenFromRegistrar fails if found(0) becomes visible
// by the saved branch: registrar 106 would then see both rows. Branch 999 does
// not catch that substitution, because 999 is written back into SQLite.
func TestSnapshot_FoundZeroHiddenFromRegistrar(t *testing.T) {
	resetSnapStore()
	prevMis, prevEnrich := loadMis, loadPageEnrich
	t.Cleanup(func() {
		loadMis, loadPageEnrich = prevMis, prevEnrich
		resetSnapStore()
	})
	loadMis = func(ctx context.Context, db *sql.DB, now time.Time, back, ahead int, examIDs []int64) ([]repo.WaitlistMisRow, error) {
		return []repo.WaitlistMisRow{{
			PlanningID: 50, PatientID: 1, BranchID: 106, CreatedAt: "2026-11-06 12:00:00",
			Comment: "комментарий окна", Phone: "79990000999",
		}}, nil
	}
	loadPageEnrich = func(ctx context.Context, db *sql.DB, planningIDs, patientIDs, exams []int64, now time.Time, answered map[int64]string) (pageEnrichment, error) {
		return pageEnrichment{live: map[int64]repo.WaitlistPlanningLive{
			77: {PlanningID: 77, PatientID: 77, BranchID: 0, PatientName: "Макс", Phone: "79990000077"},
			50: {PlanningID: 50, PatientID: 1, BranchID: 0, PatientName: "Реестр", Phone: "79990000050", IsWaitlist: true},
		}}, nil
	}
	db := openWaitlistDB(t, "wl_found_zero")
	insertOpen(t, db, 77, 106, "new")
	var id int64
	if err := db.QueryRowContext(context.Background(), `SELECT id FROM waitlist`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	svc := &Services{SQLite: db, MSSQL: db}
	base := context.Background()
	kash := staff.WithPrincipal(base, staff.Principal{ID: 2, Login: "kash", Role: staff.RoleRegistrar, BranchIDs: []int{106}})
	mgr := staff.WithPrincipal(base, staff.Principal{ID: 4, Login: "mgr", Role: staff.RoleManager})
	kashFilter := confirmations.WaitlistListFilter{BranchIDs: []int{106}, OpenOnly: true, Page: 1, PerPage: 50}
	hidden, err := svc.DashboardWaitlist(kash, kashFilter)
	if err != nil {
		t.Fatal(err)
	}
	if hidden.Total != 0 || hidden.OpenTotal != 0 || len(hidden.Items) != 0 {
		t.Fatalf("registrar %+v total=%d open=%d", hidden.Items, hidden.Total, hidden.OpenTotal)
	}
	if _, err := svc.PatchWaitlist(kash, id, 1, "call_back", "", "kash"); !errors.Is(err, confirmations.ErrWaitlistMoved) {
		t.Fatalf("max patch: %v", err)
	}
	if _, err := svc.PatchRegistryWaitlist(kash, 50, 1, "call_back", "", "kash"); !errors.Is(err, confirmations.ErrWaitlistMoved) {
		t.Fatalf("registry patch: %v", err)
	}
	seen, err := svc.DashboardWaitlist(mgr, confirmations.WaitlistListFilter{BranchIDs: []int{106, 3, 496}, IncludeOtherBranches: true, OpenOnly: true, Page: 1, PerPage: 50})
	if err != nil {
		t.Fatal(err)
	}
	if seen.Total != 2 || seen.OpenTotal != 2 {
		t.Fatalf("manager total=%d open=%d %+v", seen.Total, seen.OpenTotal, seen.Items)
	}
	var maxBranch, regBranch int
	var maxPhone, regPhone string
	for _, it := range seen.Items {
		if it.Source == "max" {
			maxBranch, maxPhone = it.BranchID, it.PatientPhone
		}
		if it.Source == "registry" {
			regBranch, regPhone = it.BranchID, it.PatientPhone
		}
	}
	if maxBranch != 0 || regBranch != 0 || maxPhone != "79990000077" || regPhone != "79990000050" {
		t.Fatalf("manager %+v", seen.Items)
	}
	var branch int
	if err := db.QueryRowContext(base, `SELECT branch_id FROM waitlist`).Scan(&branch); err != nil {
		t.Fatal(err)
	}
	if branch != 106 {
		t.Fatalf("zero branch was stored as %d", branch)
	}
}
