package staff

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
)

func TestAuthenticate_ParallelWrongPasswordsCheckBcryptAtMostFive(t *testing.T) {
	db := openStaffDB(t)
	ctx := context.Background()
	if _, err := EnsureFirstAdmin(ctx, db, "admin", "correct-password"); err != nil {
		t.Fatal(err)
	}
	s := New(db)
	var compares atomic.Int32
	s.Compare = func(hash, password []byte) error {
		compares.Add(1)
		return bcrypt.CompareHashAndPassword(hash, password)
	}
	now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, _ = s.Authenticate(ctx, "admin", "wrong-password", "10.0.0."+string(rune('a'+i)), now)
		}(i)
	}
	wg.Wait()
	if compares.Load() > int32(LockFailures) {
		t.Fatalf("bcrypt calls=%d", compares.Load())
	}
}

func TestAuthenticate_UnknownAndDisabledStillCompare(t *testing.T) {
	db := openStaffDB(t)
	ctx := context.Background()
	s := New(db)
	var compares atomic.Int32
	s.Compare = func(hash, password []byte) error {
		compares.Add(1)
		return errors.New("mismatch")
	}
	now := time.Now()
	if _, err := s.Authenticate(ctx, "nobody", "password123", "127.0.0.1", now); !errors.Is(err, ErrBadCredentials) {
		t.Fatal(err)
	}
	_, _, err := s.Create(ctx, CreateUser{Login: "off", FullName: "Off", Role: RoleRegistrar, Branches: "106", Password: "password123"})
	if err != nil {
		t.Fatal(err)
	}
	active := false
	if _, _, err := s.Update(ctx, 0, 1, UpdateUser{Active: &active}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Authenticate(ctx, "off", "password123", "127.0.0.1", now); !errors.Is(err, ErrBadCredentials) {
		t.Fatal(err)
	}
	if compares.Load() != 2 {
		t.Fatalf("compares=%d", compares.Load())
	}
}

func TestPassword_FiveCyrillicRunesRejected(t *testing.T) {
	db := openStaffDB(t)
	ctx := context.Background()
	s := New(db)
	_, _, err := s.Create(ctx, CreateUser{Login: "reg", FullName: "R", Role: RoleRegistrar, Branches: "106", Password: "абвгд"})
	if !errors.Is(err, ErrShortPassword) {
		t.Fatalf("%v", err)
	}
}

func TestRegistrarWithoutBranchesCannotLogin(t *testing.T) {
	db := openStaffDB(t)
	ctx := context.Background()
	s := New(db)
	_, _, err := s.Create(ctx, CreateUser{Login: "reg", FullName: "R", Role: RoleRegistrar, Branches: "", Password: "password123"})
	if !errors.Is(err, ErrNoBranches) {
		t.Fatalf("create %v", err)
	}
	_, _, err = s.Create(ctx, CreateUser{Login: "reg", FullName: "R", Role: RoleRegistrar, Branches: "106", Password: "password123"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE staff_users SET branches = '' WHERE login = 'reg'`); err != nil {
		t.Fatal(err)
	}
	_, err = s.Authenticate(ctx, "reg", "password123", "127.0.0.1", time.Now())
	if !errors.Is(err, ErrNoBranches) {
		t.Fatalf("login %v", err)
	}
}

func TestUpdate_TwoAdminsCannotDisableThemselvesTogether(t *testing.T) {
	db := openStaffDB(t)
	ctx := context.Background()
	s := New(db)
	if _, err := EnsureFirstAdmin(ctx, db, "admin", "password123"); err != nil {
		t.Fatal(err)
	}
	second, _, err := s.Create(ctx, CreateUser{Login: "admin2", FullName: "Two", Role: RoleAdmin, Password: "password123"})
	if err != nil {
		t.Fatal(err)
	}
	first, _, err := s.userByLogin(ctx, "admin")
	if err != nil {
		t.Fatal(err)
	}
	ids := []int64{first.ID, second.ID}
	errs := make([]error, 2)
	var wg sync.WaitGroup
	for i, id := range ids {
		wg.Add(1)
		go func(i int, id int64) {
			defer wg.Done()
			active := false
			_, _, errs[i] = s.Update(ctx, id, id, UpdateUser{Active: &active})
		}(i, id)
	}
	wg.Wait()
	wins, blocked := 0, 0
	for _, err := range errs {
		if err == nil {
			wins++
		} else if errors.Is(err, ErrLastAdmin) {
			blocked++
		} else {
			t.Fatal(err)
		}
	}
	if wins != 1 || blocked != 1 {
		t.Fatalf("wins=%d blocked=%d", wins, blocked)
	}
}
