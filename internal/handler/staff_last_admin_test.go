package handler

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/medkvadrat/medkvadrat-patient-api/internal/staff"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/store"
	_ "modernc.org/sqlite"
)

func TestStaffUpdate_TwoAdminsOneConflict(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open("sqlite", "file:staff_last_admin?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := store.Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	s := staff.New(db)
	if _, err := staff.EnsureFirstAdmin(ctx, db, "admin", "password123"); err != nil {
		t.Fatal(err)
	}
	second, _, err := s.Create(ctx, staff.CreateUser{Login: "admin2", FullName: "Two", Role: staff.RoleAdmin, Password: "password123"})
	if err != nil {
		t.Fatal(err)
	}
	first, err := s.ByID(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	h := StaffHandler{Store: s}
	ids := []int64{first.ID, second.ID}
	codes := make([]int, 2)
	var wg sync.WaitGroup
	for i, id := range ids {
		wg.Add(1)
		go func(i int, id int64) {
			defer wg.Done()
			r := httptest.NewRequest(http.MethodPost, "/dashboard/staff/"+strconv.FormatInt(id, 10), strings.NewReader(`{"active":false}`))
			r.SetPathValue("id", strconv.FormatInt(id, 10))
			r = r.WithContext(staff.WithPrincipal(r.Context(), staff.Principal{ID: id, Role: staff.RoleAdmin, All: true}))
			w := httptest.NewRecorder()
			h.Update(w, r)
			codes[i] = w.Code
		}(i, id)
	}
	wg.Wait()
	ok, conflict := 0, 0
	for _, code := range codes {
		if code == http.StatusOK {
			ok++
		} else if code == http.StatusConflict {
			conflict++
		} else {
			t.Fatalf("codes=%v", codes)
		}
	}
	if ok != 1 || conflict != 1 {
		t.Fatalf("ok=%d conflict=%d", ok, conflict)
	}
}
