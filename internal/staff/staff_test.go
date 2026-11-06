package staff

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/medkvadrat/medkvadrat-patient-api/internal/store"
	_ "modernc.org/sqlite"
)

func openStaffDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file:staff_"+t.Name()+"?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Migrate(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestEnsureFirstAdmin_OnceAndShortBootstrapPassword(t *testing.T) {
	db := openStaffDB(t)
	ctx := context.Background()
	created, err := EnsureFirstAdmin(ctx, db, "admin", "secret")
	if err != nil || !created {
		t.Fatalf("created=%v err=%v", created, err)
	}
	created, err = EnsureFirstAdmin(ctx, db, "admin", "another-password")
	if err != nil || created {
		t.Fatalf("second created=%v err=%v", created, err)
	}
	s := New(db)
	p, err := s.Authenticate(ctx, "admin", "secret", "127.0.0.1", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if p.Role != RoleAdmin || !p.All || !p.MustChangePassword {
		t.Fatalf("%+v", p)
	}
	if _, err := s.Authenticate(ctx, "admin", "another-password", "127.0.0.1", time.Now()); !errors.Is(err, ErrBadCredentials) {
		t.Fatalf("second password must not replace the first: %v", err)
	}
	var n int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM staff_users`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("n=%d err=%v", n, err)
	}
}

func TestEnsureFirstAdmin_EmptyPasswordSkips(t *testing.T) {
	db := openStaffDB(t)
	created, err := EnsureFirstAdmin(context.Background(), db, "admin", "  ")
	if err != nil || created {
		t.Fatalf("created=%v err=%v", created, err)
	}
}

func TestAuthenticate_LockoutAfterFiveFailures(t *testing.T) {
	db := openStaffDB(t)
	ctx := context.Background()
	s := New(db)
	if _, _, err := s.Create(ctx, CreateUser{
		Login: "reg", FullName: "Регистратор", Role: RoleRegistrar, Branches: "106", Password: "password10",
	}); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	for i := 0; i < LockFailures; i++ {
		_, err := s.Authenticate(ctx, "reg", "wrong-pass", "10.0.0.8", now)
		if !errors.Is(err, ErrBadCredentials) {
			t.Fatalf("attempt %d: %v", i+1, err)
		}
	}
	lockedAt := now.Add(time.Minute)
	_, err := s.Authenticate(ctx, "reg", "password10", "10.0.0.8", lockedAt)
	if !errors.Is(err, ErrLocked) {
		t.Fatalf("after five failures: %v", err)
	}
	p, err := s.Authenticate(ctx, "reg", "password10", "10.0.0.8", lockedAt.Add(LockWindow+time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if p.Login != "reg" {
		t.Fatalf("%+v", p)
	}
	var leaked int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM staff_login_log WHERE login LIKE '%password%'`).Scan(&leaked); err != nil {
		t.Fatal(err)
	}
	if leaked != 0 {
		t.Fatal("journal must not store the password")
	}
}

func TestChangePassword_MinLengthAndClearsMustChange(t *testing.T) {
	db := openStaffDB(t)
	ctx := context.Background()
	if _, err := EnsureFirstAdmin(ctx, db, "admin", "secret"); err != nil {
		t.Fatal(err)
	}
	s := New(db)
	p, err := s.Authenticate(ctx, "admin", "secret", "127.0.0.1", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ChangePassword(ctx, p.ID, "secret", "short"); !errors.Is(err, ErrShortPassword) {
		t.Fatalf("short: %v", err)
	}
	if err := s.ChangePassword(ctx, p.ID, "secret", "new-secret"); err != nil {
		t.Fatal(err)
	}
	next, err := s.ByID(ctx, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if next.MustChangePassword {
		t.Fatal("password change must clear the flag")
	}
	if _, err := s.Authenticate(ctx, "admin", "new-secret", "127.0.0.1", time.Now()); err != nil {
		t.Fatal(err)
	}
}

func TestAuthenticate_UnknownRoleRejected(t *testing.T) {
	db := openStaffDB(t)
	ctx := context.Background()
	s := New(db)
	u, _, err := s.Create(ctx, CreateUser{
		Login: "x", FullName: "X", Role: RoleRegistrar, Branches: "106", Password: "password10",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE staff_users SET role = 'boss' WHERE id = ?`, u.ID); err != nil {
		t.Fatal(err)
	}
	_, err = s.Authenticate(ctx, "x", "password10", "127.0.0.1", time.Now())
	if !errors.Is(err, ErrBadRole) {
		t.Fatalf("%v", err)
	}
}

func TestRegistrarKashirkaDoesNotSeeKurkino(t *testing.T) {
	all, ids, err := ParseBranches("106")
	if err != nil || all || len(ids) != 1 || ids[0] != 106 {
		t.Fatalf("all=%v ids=%v err=%v", all, ids, err)
	}
	p := Principal{Role: RoleRegistrar, BranchIDs: ids}
	if !p.Allows(106) || p.Allows(3) || p.Allows(496) {
		t.Fatalf("kashirka registrar branches: %+v", p.VisibleBranches())
	}
	mgr := Principal{Role: RoleManager, All: true}
	got := mgr.VisibleBranches()
	if len(got) != 3 || got[0] != 106 || got[1] != 3 || got[2] != 496 {
		t.Fatalf("manager order: %v", got)
	}
	for _, id := range DashboardBranchIDs {
		if !mgr.Allows(id) {
			t.Fatalf("manager must see %d", id)
		}
	}
}
