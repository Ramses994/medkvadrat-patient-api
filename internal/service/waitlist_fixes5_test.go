package service

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/medkvadrat/medkvadrat-patient-api/internal/confirmations"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/repo"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/staff"
)

func TestSnapshot_OneReadForParallelTabs(t *testing.T) {
	resetSnapStore()
	prevMis, prevEnrich := loadMis, loadPageEnrich
	t.Cleanup(func() {
		loadMis, loadPageEnrich = prevMis, prevEnrich
		resetSnapStore()
	})
	var misCalls, enrichCalls atomic.Int32
	loadMis = func(ctx context.Context, db *sql.DB, now time.Time, back, ahead int, examIDs []int64) ([]repo.WaitlistMisRow, error) {
		misCalls.Add(1)
		return []repo.WaitlistMisRow{{
			PlanningID: 880, PatientID: 5, CreatedAt: "2026-11-06 12:00:00", BranchID: 106, PatientName: "Реестр", Phone: "79990000002",
		}}, nil
	}
	loadPageEnrich = func(ctx context.Context, db *sql.DB, planningIDs, patientIDs, exams []int64, now time.Time, answered map[int64]string) (pageEnrichment, error) {
		enrichCalls.Add(1)
		return pageEnrichment{live: map[int64]repo.WaitlistPlanningLive{
			77:  {PlanningID: 77, PatientID: 77, BranchID: 106, PatientName: "Макс", Phone: "79990000001"},
			880: {PlanningID: 880, PatientID: 5, BranchID: 106, PatientName: "Реестр", Phone: "79990000002", IsWaitlist: true},
		}}, nil
	}
	db := openWaitlistDB(t, "wl_one_read")
	db.SetMaxOpenConns(1)
	insertOpen(t, db, 77, 106, "new")
	var id int64
	if err := db.QueryRowContext(context.Background(), `SELECT id FROM waitlist`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	svc := &Services{SQLite: db, MSSQL: db}
	ctx := staff.WithPrincipal(context.Background(), staff.Principal{ID: 1, Login: "admin", Role: staff.RoleAdmin, All: true})
	sources := []string{"all", "max", "registry"}
	var wg sync.WaitGroup
	errCh := make(chan error, 12)
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := svc.DashboardWaitlist(ctx, confirmations.WaitlistListFilter{
				Source: sources[i%3], OpenOnly: true, Page: 1, PerPage: 50,
			})
			errCh <- err
		}(i)
	}
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := svc.PatchWaitlist(ctx, id, 1, "call_back", "", "admin")
			errCh <- err
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil && !errors.Is(err, confirmations.ErrWaitlistChanged) {
			t.Fatal(err)
		}
	}
	if misCalls.Load() != 1 || enrichCalls.Load() != 1 {
		t.Fatalf("mis=%d enrich=%d", misCalls.Load(), enrichCalls.Load())
	}
}

func TestSnapshot_WriteFailureDoesNotRestoreOldBranch(t *testing.T) {
	resetSnapStore()
	prevMis, prevEnrich, prevNow, prevStore := loadMis, loadPageEnrich, misNow, storeWaitlistBranch
	t.Cleanup(func() {
		loadMis, loadPageEnrich, misNow, storeWaitlistBranch = prevMis, prevEnrich, prevNow, prevStore
		resetSnapStore()
	})
	clock := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	misNow = func() time.Time { return clock }
	phone := "79990000001"
	loadMis = func(ctx context.Context, db *sql.DB, now time.Time, back, ahead int, examIDs []int64) ([]repo.WaitlistMisRow, error) {
		return nil, nil
	}
	loadPageEnrich = func(ctx context.Context, db *sql.DB, planningIDs, patientIDs, exams []int64, now time.Time, answered map[int64]string) (pageEnrichment, error) {
		return pageEnrichment{live: map[int64]repo.WaitlistPlanningLive{
			77: {PlanningID: 77, PatientID: 77, BranchID: 3, PatientName: "Макс", Phone: phone},
		}}, nil
	}
	storeWaitlistBranch = func(ctx context.Context, conn *sql.Conn, id int64, branch int) error {
		return errors.New("disk")
	}
	ctx := context.Background()
	db := openWaitlistDB(t, "wl_write_fail")
	insertOpen(t, db, 77, 106, "new")
	var id int64
	if err := db.QueryRowContext(ctx, `SELECT id FROM waitlist`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	svc := &Services{SQLite: db, MSSQL: db}
	kash := staff.WithPrincipal(ctx, staff.Principal{ID: 2, Login: "kash", Role: staff.RoleRegistrar, BranchIDs: []int{106}})
	kurk := staff.WithPrincipal(ctx, staff.Principal{ID: 3, Login: "kurk", Role: staff.RoleRegistrar, BranchIDs: []int{3}})
	if _, err := svc.DashboardWaitlist(kurk, confirmations.WaitlistListFilter{BranchIDs: []int{3}, OpenOnly: true, Page: 1, PerPage: 50, Source: "max"}); err != nil {
		t.Fatal(err)
	}
	phone = "79991110000"
	clock = clock.Add(31 * time.Second)
	hidden, err := svc.DashboardWaitlist(kash, confirmations.WaitlistListFilter{BranchIDs: []int{106}, OpenOnly: true, Page: 1, PerPage: 50, Source: "max"})
	if err != nil {
		t.Fatal(err)
	}
	if hidden.Total != 0 || hidden.OpenTotal != 0 || len(hidden.Items) != 0 {
		t.Fatalf("old branch %+v total=%d open=%d", hidden.Items, hidden.Total, hidden.OpenTotal)
	}
	if _, err := svc.PatchWaitlist(kash, id, 1, "call_back", "", "kash"); !errors.Is(err, confirmations.ErrWaitlistMoved) {
		t.Fatalf("patch: %v", err)
	}
	visible, err := svc.DashboardWaitlist(kurk, confirmations.WaitlistListFilter{BranchIDs: []int{3}, OpenOnly: true, Page: 1, PerPage: 50, Source: "max"})
	if err != nil {
		t.Fatal(err)
	}
	if visible.Total != 1 || visible.OpenTotal != 1 || len(visible.Items) != 1 || visible.Items[0].PatientPhone != "79991110000" {
		t.Fatalf("%+v total=%d open=%d", visible.Items, visible.Total, visible.OpenTotal)
	}
	var branch int
	if err := db.QueryRowContext(ctx, `SELECT branch_id FROM waitlist WHERE id = ?`, id).Scan(&branch); err != nil {
		t.Fatal(err)
	}
	if branch != 106 {
		t.Fatalf("failed reserve wrote branch=%d", branch)
	}
}

func TestSnapshot_StaleGenerationDoesNotWriteBranch(t *testing.T) {
	resetSnapStore()
	prevMis, prevEnrich, prevHook := loadMis, loadPageEnrich, beforePersistBranches
	t.Cleanup(func() {
		loadMis, loadPageEnrich, beforePersistBranches = prevMis, prevEnrich, prevHook
		resetSnapStore()
	})
	loadMis = func(ctx context.Context, db *sql.DB, now time.Time, back, ahead int, examIDs []int64) ([]repo.WaitlistMisRow, error) {
		return nil, nil
	}
	loadPageEnrich = func(ctx context.Context, db *sql.DB, planningIDs, patientIDs, exams []int64, now time.Time, answered map[int64]string) (pageEnrichment, error) {
		return pageEnrichment{live: map[int64]repo.WaitlistPlanningLive{
			77: {PlanningID: 77, PatientID: 77, BranchID: 3, PatientName: "Макс", Phone: "79990000001"},
		}}, nil
	}
	beforePersistBranches = func() {
		snapStore.mu.Lock()
		snapStore.gen++
		snapStore.mu.Unlock()
	}
	ctx := context.Background()
	db := openWaitlistDB(t, "wl_stale_gen")
	insertOpen(t, db, 77, 106, "new")
	svc := &Services{SQLite: db, MSSQL: db}
	if _, err := svc.DashboardWaitlist(ctx, confirmations.WaitlistListFilter{Source: "max", OpenOnly: true, Page: 1, PerPage: 10}); err != nil {
		t.Fatal(err)
	}
	var branch int
	if err := db.QueryRowContext(ctx, `SELECT branch_id FROM waitlist`).Scan(&branch); err != nil {
		t.Fatal(err)
	}
	if branch != 106 {
		t.Fatalf("stale generation wrote branch=%d", branch)
	}
}

func TestSnapshot_BlankNameIsNotEnriched(t *testing.T) {
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
			77: {PlanningID: 77, PatientID: 77, BranchID: 106, PatientName: " ", Phone: ""},
		}}, nil
	}
	db := openWaitlistDB(t, "wl_blank_name")
	insertOpen(t, db, 77, 106, "new")
	var id int64
	if err := db.QueryRowContext(context.Background(), `SELECT id FROM waitlist`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	svc := &Services{SQLite: db, MSSQL: db}
	ctx := staff.WithPrincipal(context.Background(), staff.Principal{ID: 2, Login: "kash", Role: staff.RoleRegistrar, BranchIDs: []int{106}})
	page, err := svc.DashboardWaitlist(ctx, confirmations.WaitlistListFilter{Source: "max", OpenOnly: true, Page: 1, PerPage: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.Items[0].Enriched || page.Items[0].PatientName != "" || page.Items[0].PatientPhone != "" {
		t.Fatalf("%+v", page.Items)
	}
	item, err := svc.PatchWaitlist(ctx, id, 1, "call_back", "", "kash")
	if err != nil {
		t.Fatal(err)
	}
	if item.Enriched {
		t.Fatal("blank name was enriched")
	}
}

func TestSnapshot_RegistryWithoutDetailsIsHidden(t *testing.T) {
	resetSnapStore()
	prevMis, prevEnrich := loadMis, loadPageEnrich
	t.Cleanup(func() {
		loadMis, loadPageEnrich = prevMis, prevEnrich
		resetSnapStore()
	})
	loadMis = func(ctx context.Context, db *sql.DB, now time.Time, back, ahead int, examIDs []int64) ([]repo.WaitlistMisRow, error) {
		return []repo.WaitlistMisRow{{
			PlanningID: 50, PatientID: 1, CreatedAt: "2026-11-06 12:00:00", BranchID: 106,
			PatientName: "Окно", Phone: "79990000050",
		}}, nil
	}
	loadPageEnrich = func(ctx context.Context, db *sql.DB, planningIDs, patientIDs, exams []int64, now time.Time, answered map[int64]string) (pageEnrichment, error) {
		return pageEnrichment{live: map[int64]repo.WaitlistPlanningLive{}}, nil
	}
	db := openWaitlistDB(t, "wl_reg_missing")
	svc := &Services{SQLite: db, MSSQL: db}
	ctx := staff.WithPrincipal(context.Background(), staff.Principal{ID: 1, Login: "admin", Role: staff.RoleAdmin, All: true})
	page, err := svc.DashboardWaitlist(ctx, confirmations.WaitlistListFilter{Source: "registry", Page: 1, PerPage: 10})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 0 || len(page.Items) != 0 {
		t.Fatalf("window-only row %+v", page.Items)
	}
	if _, err := svc.PatchRegistryWaitlist(ctx, 50, 1, "call_back", "", "admin"); !errors.Is(err, confirmations.ErrWaitlistNotFound) {
		t.Fatalf("patch: %v", err)
	}
}

func TestDashboard_OverCap(t *testing.T) {
	prev := dashboardRowCap
	dashboardRowCap = 1
	t.Cleanup(func() { dashboardRowCap = prev })
	db := openWaitlistDB(t, "wl_cap")
	insertOpen(t, db, 1, 106, "new")
	insertOpen(t, db, 2, 106, "new")
	svc := &Services{SQLite: db}
	_, err := svc.DashboardWaitlist(context.Background(), confirmations.WaitlistListFilter{Source: "max", OpenOnly: true, Page: 1, PerPage: 10})
	if !errors.Is(err, confirmations.ErrWaitlistOverloaded) {
		t.Fatalf("%v", err)
	}
}
