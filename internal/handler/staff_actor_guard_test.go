package handler

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/medkvadrat/medkvadrat-patient-api/internal/db"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/staff"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/store"
	_ "modernc.org/sqlite"
)

type staffFixture struct {
	ctx    context.Context
	db     *sql.DB
	store  *staff.Store
	handler StaffHandler
	actor  staff.User
	other  staff.User
	target staff.User
}

func newStaffFixture(t *testing.T) *staffFixture {
	t.Helper()
	ctx := context.Background()
	sqlite, err := db.OpenSQLite(filepath.Join(t.TempDir(), "staff.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlite.Close() })
	if err := store.Migrate(ctx, sqlite); err != nil {
		t.Fatal(err)
	}
	s := staff.New(sqlite)
	actor, _, err := s.Create(ctx, staff.CreateUser{Login: "admin", FullName: "Админ", Role: staff.RoleAdmin, Password: "password123"})
	if err != nil {
		t.Fatal(err)
	}
	other, _, err := s.Create(ctx, staff.CreateUser{Login: "admin2", FullName: "Второй", Role: staff.RoleAdmin, Password: "password123"})
	if err != nil {
		t.Fatal(err)
	}
	target, _, err := s.Create(ctx, staff.CreateUser{Login: "reg", FullName: "Регистратор", Role: staff.RoleRegistrar, Branches: "106", Password: "password123"})
	if err != nil {
		t.Fatal(err)
	}
	return &staffFixture{
		ctx: ctx, db: sqlite, store: s, handler: StaffHandler{Store: s},
		actor: actor, other: other, target: target,
	}
}

func (f *staffFixture) principal() staff.Principal {
	return staff.Principal{
		ID: f.actor.ID, Login: f.actor.Login, Role: staff.RoleAdmin, All: true,
		SessionVersion: f.actor.SessionVersion,
	}
}

func (f *staffFixture) pause() (release chan struct{}, ready *sync.WaitGroup) {
	gate := make(chan struct{})
	var entered sync.WaitGroup
	entered.Add(1)
	f.handler.AfterAuthz = func() {
		entered.Done()
		<-gate
	}
	return gate, &entered
}

func (f *staffFixture) journalCount(t *testing.T) int {
	t.Helper()
	var n int
	if err := f.db.QueryRowContext(f.ctx, `SELECT COUNT(*) FROM staff_admin_log`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func (f *staffFixture) assertTargetIntact(t *testing.T, journalBefore int) {
	t.Helper()
	if _, err := f.store.ByID(f.ctx, f.target.ID); err != nil {
		t.Fatalf("target missing: %v", err)
	}
	var deleted sql.NullString
	if err := f.db.QueryRowContext(f.ctx, `SELECT deleted_at FROM staff_users WHERE id = ?`, f.target.ID).Scan(&deleted); err != nil {
		t.Fatal(err)
	}
	if deleted.Valid {
		t.Fatal("target was deleted")
	}
	var aboutTarget int
	if err := f.db.QueryRowContext(f.ctx, `
SELECT COUNT(*) FROM staff_admin_log
WHERE target_id = ? AND (change LIKE 'delete;%' OR change LIKE 'role=%')`, f.target.ID).Scan(&aboutTarget); err != nil {
		t.Fatal(err)
	}
	if aboutTarget != 0 {
		t.Fatalf("mutating audit rows for target=%d", aboutTarget)
	}
	_ = journalBefore
}

func (f *staffFixture) runDelete() *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodDelete, "/dashboard/staff/"+strconv.FormatInt(f.target.ID, 10), nil)
	r.SetPathValue("id", strconv.FormatInt(f.target.ID, 10))
	r = withPrincipal(r, f.principal())
	w := httptest.NewRecorder()
	f.handler.Delete(w, r)
	return w
}

func TestStaffDelete_AuthorDeletedAfterAuthzRefuses(t *testing.T) {
	f := newStaffFixture(t)
	before := f.journalCount(t)
	release, ready := f.pause()
	var wg sync.WaitGroup
	wg.Add(1)
	var w *httptest.ResponseRecorder
	go func() {
		defer wg.Done()
		w = f.runDelete()
	}()
	ready.Wait()
	if err := f.store.Delete(f.ctx, f.other.ID, f.other.SessionVersion, f.actor.ID); err != nil {
		t.Fatal(err)
	}
	close(release)
	wg.Wait()
	if w.Code != http.StatusUnauthorized || !strings.Contains(w.Body.String(), "UNAUTHORIZED") {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	f.assertTargetIntact(t, before)
}

func TestStaffDelete_AuthorDisabledAfterAuthzRefuses(t *testing.T) {
	f := newStaffFixture(t)
	before := f.journalCount(t)
	release, ready := f.pause()
	var wg sync.WaitGroup
	wg.Add(1)
	var w *httptest.ResponseRecorder
	go func() {
		defer wg.Done()
		w = f.runDelete()
	}()
	ready.Wait()
	active := false
	if _, _, err := f.store.Update(f.ctx, f.other.ID, f.actor.ID, staff.UpdateUser{Active: &active, ActorSessionVersion: f.other.SessionVersion}); err != nil {
		t.Fatal(err)
	}
	close(release)
	wg.Wait()
	if w.Code != http.StatusUnauthorized || !strings.Contains(w.Body.String(), "UNAUTHORIZED") {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	f.assertTargetIntact(t, before)
}

func TestStaffDelete_AuthorDemotedAfterAuthzRefuses(t *testing.T) {
	f := newStaffFixture(t)
	before := f.journalCount(t)
	release, ready := f.pause()
	var wg sync.WaitGroup
	wg.Add(1)
	var w *httptest.ResponseRecorder
	go func() {
		defer wg.Done()
		w = f.runDelete()
	}()
	ready.Wait()
	// Role change without bumping session_version: cookie still matches, role does not.
	if _, err := f.db.ExecContext(f.ctx, `UPDATE staff_users SET role = 'manager' WHERE id = ?`, f.actor.ID); err != nil {
		t.Fatal(err)
	}
	close(release)
	wg.Wait()
	if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "FORBIDDEN") {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	f.assertTargetIntact(t, before)
}

func TestStaffDelete_AuthorPasswordResetAfterAuthzRefuses(t *testing.T) {
	f := newStaffFixture(t)
	before := f.journalCount(t)
	release, ready := f.pause()
	var wg sync.WaitGroup
	wg.Add(1)
	var w *httptest.ResponseRecorder
	go func() {
		defer wg.Done()
		w = f.runDelete()
	}()
	ready.Wait()
	if _, _, err := f.store.Update(f.ctx, f.other.ID, f.actor.ID, staff.UpdateUser{ResetPassword: true, ActorSessionVersion: f.other.SessionVersion}); err != nil {
		t.Fatal(err)
	}
	close(release)
	wg.Wait()
	if w.Code != http.StatusUnauthorized || !strings.Contains(w.Body.String(), "UNAUTHORIZED") {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	f.assertTargetIntact(t, before)
}

func TestStaffCreate_AuthorDeletedAfterAuthzRefuses(t *testing.T) {
	f := newStaffFixture(t)
	release, ready := f.pause()
	var wg sync.WaitGroup
	wg.Add(1)
	var w *httptest.ResponseRecorder
	go func() {
		defer wg.Done()
		r := httptest.NewRequest(http.MethodPost, "/dashboard/staff", strings.NewReader(`{"login":"newreg","full_name":"Новый","role":"registrar","branches":"106","password":"password123"}`))
		r.Header.Set("Content-Type", "application/json")
		r = withPrincipal(r, f.principal())
		w = httptest.NewRecorder()
		f.handler.Create(w, r)
	}()
	ready.Wait()
	if err := f.store.Delete(f.ctx, f.other.ID, f.other.SessionVersion, f.actor.ID); err != nil {
		t.Fatal(err)
	}
	close(release)
	wg.Wait()
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var n int
	if err := f.db.QueryRowContext(f.ctx, `SELECT COUNT(*) FROM staff_users WHERE login = 'newreg' AND deleted_at IS NULL`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("created=%d err=%v", n, err)
	}
	var journal int
	if err := f.db.QueryRowContext(f.ctx, `SELECT COUNT(*) FROM staff_admin_log WHERE change LIKE 'create;%' AND change LIKE '%newreg%'`).Scan(&journal); err != nil {
		t.Fatal(err)
	}
	// create;role=... does not include login — count rows for the new login via staff_users instead.
	_ = journal
	var createdLog int
	if err := f.db.QueryRowContext(f.ctx, `
SELECT COUNT(*) FROM staff_admin_log
WHERE change LIKE 'create;%' AND target_id = (SELECT id FROM staff_users WHERE login = 'newreg')`).Scan(&createdLog); err != nil {
		t.Fatal(err)
	}
	if createdLog != 0 {
		t.Fatalf("create journal=%d", createdLog)
	}
}

func TestStaffUpdate_AuthorDeletedAfterAuthzRefuses(t *testing.T) {
	f := newStaffFixture(t)
	before := f.journalCount(t)
	release, ready := f.pause()
	var wg sync.WaitGroup
	wg.Add(1)
	var w *httptest.ResponseRecorder
	go func() {
		defer wg.Done()
		r := httptest.NewRequest(http.MethodPost, "/dashboard/staff/"+strconv.FormatInt(f.target.ID, 10), strings.NewReader(`{"active":false}`))
		r.SetPathValue("id", strconv.FormatInt(f.target.ID, 10))
		r.Header.Set("Content-Type", "application/json")
		r = withPrincipal(r, f.principal())
		w = httptest.NewRecorder()
		f.handler.Update(w, r)
	}()
	ready.Wait()
	if err := f.store.Delete(f.ctx, f.other.ID, f.other.SessionVersion, f.actor.ID); err != nil {
		t.Fatal(err)
	}
	close(release)
	wg.Wait()
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	users, err := f.store.List(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, u := range users {
		if u.ID == f.target.ID && !u.Active {
			t.Fatal("target was deactivated")
		}
	}
	f.assertTargetIntact(t, before)
}

func TestStaffResetPassword_AuthorDeletedAfterAuthzRefuses(t *testing.T) {
	f := newStaffFixture(t)
	var beforeHash string
	if err := f.db.QueryRowContext(f.ctx, `SELECT password_hash FROM staff_users WHERE id = ?`, f.target.ID).Scan(&beforeHash); err != nil {
		t.Fatal(err)
	}
	before := f.journalCount(t)
	release, ready := f.pause()
	var wg sync.WaitGroup
	wg.Add(1)
	var w *httptest.ResponseRecorder
	go func() {
		defer wg.Done()
		r := httptest.NewRequest(http.MethodPost, "/dashboard/staff/"+strconv.FormatInt(f.target.ID, 10), strings.NewReader(`{"reset_password":true}`))
		r.SetPathValue("id", strconv.FormatInt(f.target.ID, 10))
		r.Header.Set("Content-Type", "application/json")
		r = withPrincipal(r, f.principal())
		w = httptest.NewRecorder()
		f.handler.Update(w, r)
	}()
	ready.Wait()
	if err := f.store.Delete(f.ctx, f.other.ID, f.other.SessionVersion, f.actor.ID); err != nil {
		t.Fatal(err)
	}
	close(release)
	wg.Wait()
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var after string
	if err := f.db.QueryRowContext(f.ctx, `SELECT password_hash FROM staff_users WHERE id = ?`, f.target.ID).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if beforeHash != after {
		t.Fatal("password hash changed")
	}
	f.assertTargetIntact(t, before)
}
