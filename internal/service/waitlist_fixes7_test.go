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

func TestSnapshot_SQLiteWaitExpiresBeforeServe(t *testing.T) {
	resetSnapStore()
	prevMis, prevEnrich, prevHook := loadMis, loadPageEnrich, duringLastDashboardRead
	t.Cleanup(func() {
		loadMis, loadPageEnrich, duringLastDashboardRead = prevMis, prevEnrich, prevHook
		resetSnapStore()
	})
	clock := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	setMisNow(t, func() time.Time { return clock })
	const phone = "79990000050"
	loadMis = func(ctx context.Context, db *sql.DB, now time.Time, back, ahead int, examIDs []int64) ([]repo.WaitlistMisRow, error) {
		return []repo.WaitlistMisRow{{
			PlanningID: 50, PatientID: 1, BranchID: 106, CreatedAt: "2026-11-06 12:00:00",
			PatientName: "Реестр", Phone: phone,
		}}, nil
	}
	loadPageEnrich = func(ctx context.Context, db *sql.DB, planningIDs, patientIDs, exams []int64, now time.Time, answered map[int64]string) (pageEnrichment, error) {
		return pageEnrichment{live: map[int64]repo.WaitlistPlanningLive{
			50: {PlanningID: 50, PatientID: 1, BranchID: 106, PatientName: "Реестр", Phone: phone, IsWaitlist: true},
		}}, nil
	}
	duringLastDashboardRead = func() { clock = clock.Add(31 * time.Second) }
	db := openWaitlistDB(t, "wl_sqlite_wait")
	svc := &Services{SQLite: db, MSSQL: db}
	page, err := svc.DashboardWaitlist(context.Background(), confirmations.WaitlistListFilter{Source: "registry", OpenOnly: true, Page: 1, PerPage: 50})
	if err != nil {
		t.Fatal(err)
	}
	if page.Banner != "Медиалог недоступен" || page.Total != 0 || len(page.Items) != 0 {
		t.Fatalf("banner=%q total=%d %+v", page.Banner, page.Total, page.Items)
	}
	for _, it := range page.Items {
		if it.PatientPhone == phone || it.PatientName == "Реестр" {
			t.Fatalf("expired snapshot published %+v", it)
		}
	}
}

func TestSnapshot_RegistryLockAfterTTLDoesNotWrite(t *testing.T) {
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
	db := openWaitlistDB(t, "wl_reg_lock")
	svc := &Services{SQLite: db, MSSQL: db}
	ctx := context.Background()
	if _, err := svc.DashboardWaitlist(ctx, confirmations.WaitlistListFilter{Source: "registry", OpenOnly: true, Page: 1, PerPage: 10}); err != nil {
		t.Fatal(err)
	}
	clock = clock.Add(29 * time.Second)
	afterWriteLock = func() { clock = clock.Add(2 * time.Second) }
	_, err := svc.PatchRegistryWaitlist(ctx, 50, 1, "call_back", "", "admin")
	if !errors.Is(err, confirmations.ErrWaitlistNotFound) {
		t.Fatalf("patch: %v", err)
	}
	var n int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM waitlist_mis_work`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("mis work rows=%d", n)
	}
}

func TestSnapshot_ExpiredMaxPatchKeepsSavedBranchWithoutPhone(t *testing.T) {
	resetSnapStore()
	prevMis, prevEnrich, prevHook := loadMis, loadPageEnrich, afterWriteLock
	t.Cleanup(func() {
		loadMis, loadPageEnrich, afterWriteLock = prevMis, prevEnrich, prevHook
		resetSnapStore()
	})
	clock := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	setMisNow(t, func() time.Time { return clock })
	const phone = "79990000077"
	loadMis = func(ctx context.Context, db *sql.DB, now time.Time, back, ahead int, examIDs []int64) ([]repo.WaitlistMisRow, error) {
		return nil, nil
	}
	loadPageEnrich = func(ctx context.Context, db *sql.DB, planningIDs, patientIDs, exams []int64, now time.Time, answered map[int64]string) (pageEnrichment, error) {
		return pageEnrichment{live: map[int64]repo.WaitlistPlanningLive{
			77: {PlanningID: 77, PatientID: 77, BranchID: 106, PatientName: "Макс", Phone: phone},
		}}, nil
	}
	db := openWaitlistDB(t, "wl_max_lock")
	insertOpen(t, db, 77, 106, "new")
	var id int64
	if err := db.QueryRowContext(context.Background(), `SELECT id FROM waitlist`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	svc := &Services{SQLite: db, MSSQL: db}
	kash := staff.WithPrincipal(context.Background(), staff.Principal{ID: 2, Login: "kash", Role: staff.RoleRegistrar, BranchIDs: []int{106}})
	if _, err := svc.DashboardWaitlist(kash, confirmations.WaitlistListFilter{Source: "max", OpenOnly: true, Page: 1, PerPage: 10}); err != nil {
		t.Fatal(err)
	}
	clock = clock.Add(29 * time.Second)
	afterWriteLock = func() { clock = clock.Add(2 * time.Second) }
	item, err := svc.PatchWaitlist(kash, id, 1, "call_back", "", "kash")
	if err != nil {
		t.Fatal(err)
	}
	if item.PatientPhone != "" || item.PatientName != "" || item.Outcome != "call_back" {
		t.Fatalf("%+v", item)
	}
	var status string
	var version int
	if err := db.QueryRowContext(context.Background(), `SELECT status, IFNULL(version,1) FROM waitlist WHERE id = ?`, id).Scan(&status, &version); err != nil {
		t.Fatal(err)
	}
	if status != confirmations.WaitlistStatusContacted || version != 2 {
		t.Fatalf("status=%s version=%d", status, version)
	}
}

func TestSnapshot_LockWaitOutlivesSnapshot(t *testing.T) {
	resetSnapStore()
	prevMis, prevEnrich := loadMis, loadPageEnrich
	t.Cleanup(func() {
		loadMis, loadPageEnrich = prevMis, prevEnrich
		resetSnapStore()
	})
	clock := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	setMisNow(t, func() time.Time { return clock })
	loadMis = func(ctx context.Context, db *sql.DB, now time.Time, back, ahead int, examIDs []int64) ([]repo.WaitlistMisRow, error) {
		return nil, nil
	}
	loadPageEnrich = func(ctx context.Context, db *sql.DB, planningIDs, patientIDs, exams []int64, now time.Time, answered map[int64]string) (pageEnrichment, error) {
		return pageEnrichment{live: map[int64]repo.WaitlistPlanningLive{
			77: {PlanningID: 77, PatientID: 77, BranchID: 106, PatientName: "Макс", Phone: "79990000077"},
		}}, nil
	}
	db := openWaitlistDB(t, "wl_lock_wait")
	insertOpen(t, db, 77, 106, "new")
	var id int64
	ctx := context.Background()
	if err := db.QueryRowContext(ctx, `SELECT id FROM waitlist`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	svc := &Services{SQLite: db, MSSQL: db}
	if _, err := svc.DashboardWaitlist(ctx, confirmations.WaitlistListFilter{Source: "max", OpenOnly: true, Page: 1, PerPage: 10}); err != nil {
		t.Fatal(err)
	}
	clock = clock.Add(29500 * time.Millisecond)
	hold, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = hold.Close() })
	if _, err := hold.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = hold.ExecContext(context.Background(), "ROLLBACK") })
	done := make(chan error, 1)
	go func() {
		_, err := svc.PatchWaitlist(ctx, id, 1, "call_back", "", "admin")
		done <- err
	}()
	select {
	case err := <-done:
		if !errors.Is(err, confirmations.ErrWaitlistNotFound) {
			t.Fatalf("patch: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("patch waited past the snapshot lifetime")
	}
	var status, outcome string
	var version int
	if err := db.QueryRowContext(ctx, `SELECT status, IFNULL(outcome,''), IFNULL(version,1) FROM waitlist WHERE id = ?`, id).Scan(&status, &outcome, &version); err != nil {
		t.Fatal(err)
	}
	if status != "new" || outcome != "" || version != 1 {
		t.Fatalf("status=%s outcome=%s version=%d", status, outcome, version)
	}
}

func TestSnapshot_BranchLockAfterTTLWritesNothing(t *testing.T) {
	resetSnapStore()
	prevMis, prevEnrich, prevHook := loadMis, loadPageEnrich, afterBranchLock
	t.Cleanup(func() {
		loadMis, loadPageEnrich, afterBranchLock = prevMis, prevEnrich, prevHook
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
			78: {PlanningID: 78, PatientID: 78, BranchID: 496, PatientName: "Второй", Phone: "79990000002"},
		}}, nil
	}
	afterBranchLock = func() { clock = clock.Add(31 * time.Second) }
	db := openWaitlistDB(t, "wl_branch_lock")
	insertOpen(t, db, 77, 106, "new")
	insertOpen(t, db, 78, 106, "new")
	svc := &Services{SQLite: db, MSSQL: db}
	if _, err := svc.DashboardWaitlist(context.Background(), confirmations.WaitlistListFilter{Source: "max", OpenOnly: true, Page: 1, PerPage: 10}); err != nil {
		t.Fatal(err)
	}
	rows, err := db.QueryContext(context.Background(), `SELECT branch_id FROM waitlist ORDER BY planning_id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var branches []int
	for rows.Next() {
		var branch int
		if err := rows.Scan(&branch); err != nil {
			t.Fatal(err)
		}
		branches = append(branches, branch)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(branches) != 2 || branches[0] != 106 || branches[1] != 106 {
		t.Fatalf("branches=%v", branches)
	}
}

func TestSnapshot_CapOnLiveSnapshot(t *testing.T) {
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
		return pageEnrichment{live: map[int64]repo.WaitlistPlanningLive{
			1: {PlanningID: 1, PatientID: 1, BranchID: 106, PatientName: "Макс", Phone: "79990000001"},
		}}, nil
	}
	db := openWaitlistDB(t, "wl_cap_live")
	insertOpen(t, db, 1, 106, "new")
	svc := &Services{SQLite: db, MSSQL: db}
	ctx := context.Background()
	if _, err := svc.DashboardWaitlist(ctx, confirmations.WaitlistListFilter{Source: "max", OpenOnly: true, Page: 1, PerPage: 50}); err != nil {
		t.Fatal(err)
	}
	if misCalls.Load() != 1 || enrichCalls.Load() != 1 {
		t.Fatalf("warm mis=%d enrich=%d", misCalls.Load(), enrichCalls.Load())
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for i := 2; i <= dashboardRowCap+1; i++ {
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
	if err := db.QueryRowContext(ctx, `SELECT id FROM waitlist WHERE planning_id = 1`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.DashboardWaitlist(ctx, confirmations.WaitlistListFilter{Source: "max", OpenOnly: true, Page: 1, PerPage: 50}); !errors.Is(err, confirmations.ErrWaitlistOverloaded) {
		t.Fatalf("get max: %v", err)
	}
	if _, err := svc.DashboardWaitlist(ctx, confirmations.WaitlistListFilter{Source: "registry", OpenOnly: true, Page: 1, PerPage: 50}); !errors.Is(err, confirmations.ErrWaitlistOverloaded) {
		t.Fatalf("get registry: %v", err)
	}
	if _, err := svc.PatchWaitlist(ctx, id, 1, "call_back", "", "admin"); !errors.Is(err, confirmations.ErrWaitlistOverloaded) {
		t.Fatalf("patch max: %v", err)
	}
	if _, err := svc.PatchRegistryWaitlist(ctx, 50, 1, "call_back", "", "admin"); !errors.Is(err, confirmations.ErrWaitlistOverloaded) {
		t.Fatalf("patch registry: %v", err)
	}
	if misCalls.Load() != 1 || enrichCalls.Load() != 1 {
		t.Fatalf("mis=%d enrich=%d", misCalls.Load(), enrichCalls.Load())
	}
	var status, outcome string
	if err := db.QueryRowContext(ctx, `SELECT status, IFNULL(outcome,'') FROM waitlist WHERE id = ?`, id).Scan(&status, &outcome); err != nil {
		t.Fatal(err)
	}
	if status != "new" || outcome != "" {
		t.Fatalf("status=%s outcome=%s", status, outcome)
	}
}
