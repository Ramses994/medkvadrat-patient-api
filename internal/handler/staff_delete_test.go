package handler

import (
	"context"
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

func TestStaffDelete_TwoAdminsDeletingEachOtherOneIsLastAdmin(t *testing.T) {
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
	first, _, err := s.Create(ctx, staff.CreateUser{Login: "admin", FullName: "One", Role: staff.RoleAdmin, Password: "password123"})
	if err != nil {
		t.Fatal(err)
	}
	second, _, err := s.Create(ctx, staff.CreateUser{Login: "admin2", FullName: "Two", Role: staff.RoleAdmin, Password: "password123"})
	if err != nil {
		t.Fatal(err)
	}
	h := StaffHandler{Store: s}
	pairs := [][2]int64{{first.ID, second.ID}, {second.ID, first.ID}}
	codes := make([]int, 2)
	bodies := make([]string, 2)
	var wg sync.WaitGroup
	for i, pair := range pairs {
		wg.Add(1)
		go func(i int, actor, target int64) {
			defer wg.Done()
			r := httptest.NewRequest(http.MethodDelete, "/dashboard/staff/"+strconv.FormatInt(target, 10), nil)
			r.SetPathValue("id", strconv.FormatInt(target, 10))
			r = r.WithContext(staff.WithPrincipal(r.Context(), staff.Principal{ID: actor, Role: staff.RoleAdmin, All: true}))
			w := httptest.NewRecorder()
			h.Delete(w, r)
			codes[i] = w.Code
			bodies[i] = w.Body.String()
		}(i, pair[0], pair[1])
	}
	wg.Wait()
	ok, last := 0, 0
	for i, code := range codes {
		switch {
		case code == http.StatusOK:
			ok++
		case code == http.StatusConflict && strings.Contains(bodies[i], "LAST_ADMIN"):
			last++
		default:
			t.Fatalf("codes=%v bodies=%v", codes, bodies)
		}
	}
	if ok != 1 || last != 1 {
		t.Fatalf("ok=%d last=%d", ok, last)
	}
	users, err := s.List(ctx)
	if err != nil || len(users) != 1 || users[0].Role != staff.RoleAdmin || !users[0].Active {
		t.Fatalf("users=%+v err=%v", users, err)
	}
}

func TestStaffDelete_ManagerAndRegistrarForbidden(t *testing.T) {
	h := StaffHandler{}
	for _, p := range []staff.Principal{
		{ID: 2, Login: "mgr", Role: staff.RoleManager, All: true},
		registrarKashirka(),
	} {
		r := withPrincipal(httptest.NewRequest(http.MethodDelete, "/dashboard/staff/3", nil), p)
		r.SetPathValue("id", "3")
		w := httptest.NewRecorder()
		h.Delete(w, r)
		if w.Code != http.StatusForbidden {
			t.Fatalf("%s status=%d body=%s", p.Role, w.Code, w.Body.String())
		}
	}
}
