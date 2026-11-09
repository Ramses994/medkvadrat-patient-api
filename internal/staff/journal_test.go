package staff

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestChangePassword_CompareDoesNotHoldSQLite(t *testing.T) {
	db := openStaffDB(t)
	ctx := context.Background()
	s := New(db)
	if _, _, err := s.Create(ctx, CreateUser{Login: "reg", FullName: "R", Role: RoleRegistrar, Branches: "106", Password: "password123"}); err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	release := make(chan struct{})
	s.Compare = func(hash, password []byte) error {
		close(started)
		<-release
		return nil
	}
	done := make(chan error, 1)
	go func() {
		done <- s.ChangePassword(ctx, 1, "password123", "new-password-1")
	}()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("password compare did not start")
	}
	qctx, cancel := context.WithTimeout(ctx, time.Second)
	var n int
	err := db.QueryRowContext(qctx, `SELECT COUNT(*) FROM staff_users`).Scan(&n)
	cancel()
	if err != nil {
		t.Fatalf("sqlite stayed busy during password check: %v", err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	var actor int64
	var login, change string
	if err := db.QueryRowContext(ctx, `SELECT actor_id, actor_login, change FROM staff_admin_log`).Scan(&actor, &login, &change); err != nil {
		t.Fatal(err)
	}
	if actor != 1 || login != "reg" || change != "password" || strings.Contains(change, "new-password") {
		t.Fatalf("journal actor=%d login=%s change=%s", actor, login, change)
	}
}

func TestChangePassword_StaleVersionDoesNotUpdate(t *testing.T) {
	db := openStaffDB(t)
	ctx := context.Background()
	s := New(db)
	if _, _, err := s.Create(ctx, CreateUser{Login: "reg", FullName: "R", Role: RoleRegistrar, Branches: "106", Password: "password123"}); err != nil {
		t.Fatal(err)
	}
	var before string
	if err := db.QueryRowContext(ctx, `SELECT password_hash FROM staff_users WHERE id = 1`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	s.Compare = func(hash, password []byte) error {
		_, err := db.ExecContext(ctx, `UPDATE staff_users SET session_version = session_version + 1 WHERE id = 1`)
		return err
	}
	err := s.ChangePassword(ctx, 1, "password123", "new-password-1")
	if !errors.Is(err, ErrStale) {
		t.Fatal(err)
	}
	var after string
	if err := db.QueryRowContext(ctx, `SELECT password_hash FROM staff_users WHERE id = 1`).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Fatal("stale password change wrote a hash")
	}
}

func TestAdminJournal_SameTransactionAsUpdate(t *testing.T) {
	db := openStaffDB(t)
	ctx := context.Background()
	if _, err := EnsureFirstAdmin(ctx, db, "admin", "password123"); err != nil {
		t.Fatal(err)
	}
	s := New(db)
	active := false
	if _, _, err := s.Update(ctx, 1, 1, UpdateUser{Active: &active}); !errors.Is(err, ErrLastAdmin) {
		t.Fatal(err)
	}
	var n int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM staff_admin_log`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("rolled-back admin change left %d journal rows", n)
	}
	if _, _, err := s.Create(ctx, CreateUser{Login: "reg", FullName: "R", Role: RoleRegistrar, Branches: "106", Password: "password123"}); err != nil {
		t.Fatal(err)
	}
	_, temp, err := s.Update(ctx, 1, 2, UpdateUser{ResetPassword: true, Branches: "106"})
	if err != nil {
		t.Fatal(err)
	}
	var actor int64
	var login, change string
	var target int64
	if err := db.QueryRowContext(ctx, `SELECT actor_id, actor_login, target_id, change FROM staff_admin_log`).Scan(&actor, &login, &target, &change); err != nil {
		t.Fatal(err)
	}
	if actor != 1 || login != "admin" || target != 2 || !strings.Contains(change, "reset_password=1") || strings.Contains(change, temp) {
		t.Fatalf("actor=%d login=%s target=%d change=%s", actor, login, target, change)
	}
	rows, err := db.QueryContext(ctx, `PRAGMA table_info(staff_admin_log)`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, decl string
		var notnull, pk int
		var dflt any
		if err := rows.Scan(&cid, &name, &decl, &notnull, &dflt, &pk); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(strings.ToLower(name), "password") {
			t.Fatalf("journal column %s", name)
		}
	}
}
