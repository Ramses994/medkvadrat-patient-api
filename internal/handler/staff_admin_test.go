package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/medkvadrat/medkvadrat-patient-api/internal/db"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/staff"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/store"
	_ "modernc.org/sqlite"
)

func TestStaffPage_NonAdminForbidden(t *testing.T) {
	h := StaffHandler{}
	r := withPrincipal(httptest.NewRequest(http.MethodGet, "/dashboard/staff", nil), registrarKashirka())
	w := httptest.NewRecorder()
	h.List(w, r)
	if w.Code != http.StatusForbidden {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "Только для администратора") {
		t.Fatalf("body=%s", w.Body.String())
	}
}

func TestStaffPage_AdminListOmitsPasswordHash(t *testing.T) {
	ctx := context.Background()
	db, err := db.OpenSQLite(filepath.Join(t.TempDir(), "staff.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := store.Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	s := staff.New(db)
	if _, _, err := s.Create(ctx, staff.CreateUser{
		Login: "admin", FullName: "Администратор", Role: staff.RoleAdmin, Branches: "*", Password: "password10",
	}); err != nil {
		t.Fatal(err)
	}
	h := StaffHandler{Store: s}
	r := withPrincipal(httptest.NewRequest(http.MethodGet, "/dashboard/staff", nil), staff.Principal{
		ID: 1, Login: "admin", Role: staff.RoleAdmin, All: true,
	})
	w := httptest.NewRecorder()
	h.List(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "password_hash") || strings.Contains(w.Body.String(), "password10") {
		t.Fatalf("secret leaked: %s", w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "admin") {
		t.Fatalf("body=%s", w.Body.String())
	}
}
