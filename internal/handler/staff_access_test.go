package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/medkvadrat/medkvadrat-patient-api/internal/staff"
)

func registrarKashirka() staff.Principal {
	return staff.Principal{ID: 4, Login: "kash", Role: staff.RoleRegistrar, BranchIDs: []int{106}}
}

func managerAll() staff.Principal {
	return staff.Principal{ID: 5, Login: "dir", Role: staff.RoleManager, All: true, BranchIDs: staff.DashboardBranchIDs}
}

func withPrincipal(r *http.Request, p staff.Principal) *http.Request {
	return r.WithContext(staff.WithPrincipal(r.Context(), p))
}

func TestSchedule_RegistrarSeesOnlyKashirka(t *testing.T) {
	h := DashboardHandler{Svc: &fakeDashboardSvc{}, Now: dashboardNow}
	r := withPrincipal(httptest.NewRequest(http.MethodGet, "/dashboard/data?date=2026-06-26", nil), registrarKashirka())
	w := httptest.NewRecorder()
	h.Schedule(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	ids := scheduleBranchIDs(t, w.Body.Bytes())
	if len(ids) != 1 || ids[0] != 106 {
		t.Fatalf("branches=%v body=%s", ids, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "Мартынов") {
		t.Fatal("kurkino appointment leaked")
	}

	for _, branch := range []string{"3", "496"} {
		r = withPrincipal(httptest.NewRequest(http.MethodGet, "/dashboard/data?date=2026-06-26&branch="+branch, nil), registrarKashirka())
		w = httptest.NewRecorder()
		h.Schedule(w, r)
		if w.Code != http.StatusForbidden {
			t.Fatalf("branch %s: %d %s", branch, w.Code, w.Body.String())
		}
	}

	r = withPrincipal(httptest.NewRequest(http.MethodGet, "/dashboard/data?date=2026-06-26", nil), managerAll())
	w = httptest.NewRecorder()
	h.Schedule(w, r)
	ids = scheduleBranchIDs(t, w.Body.Bytes())
	if len(ids) != 3 {
		t.Fatalf("manager branches=%v", ids)
	}

	r = withPrincipal(httptest.NewRequest(http.MethodGet, "/dashboard/data?date=2026-06-26&branch=106,3", nil), managerAll())
	w = httptest.NewRecorder()
	h.Schedule(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("two branches: %d %s", w.Code, w.Body.String())
	}
	ids = scheduleBranchIDs(t, w.Body.Bytes())
	if len(ids) != 2 || ids[0] != 106 || ids[1] != 3 {
		t.Fatalf("filtered branches=%v", ids)
	}

	r = withPrincipal(httptest.NewRequest(http.MethodGet, "/dashboard/data?date=2026-06-26&branch=106,3", nil), registrarKashirka())
	w = httptest.NewRecorder()
	h.Schedule(w, r)
	if w.Code != http.StatusForbidden {
		t.Fatalf("registrar list: %d %s", w.Code, w.Body.String())
	}

	r = withPrincipal(httptest.NewRequest(http.MethodGet, "/dashboard/data?date=2026-06-26&branch=106,999", nil), managerAll())
	w = httptest.NewRecorder()
	h.Schedule(w, r)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("unknown branch: %d %s", w.Code, w.Body.String())
	}
}

func TestSchedule_NoPrincipalKeepsEveryBranch(t *testing.T) {
	h := DashboardHandler{Svc: &fakeDashboardSvc{}, Now: dashboardNow}
	r := httptest.NewRequest(http.MethodGet, "/dashboard/data?date=2026-06-26", nil)
	w := httptest.NewRecorder()
	h.Schedule(w, r)
	ids := scheduleBranchIDs(t, w.Body.Bytes())
	if len(ids) != 3 {
		t.Fatalf("branches=%v", ids)
	}
}

func TestWaitlist_BranchFilterAndStatusValidation(t *testing.T) {
	svc := &fakeWaitlistSvc{}
	h := WaitlistHandler{Svc: svc}

	r := httptest.NewRequest(http.MethodGet, "/dashboard/waitlist?status=bogus", nil)
	w := httptest.NewRecorder()
	h.List(w, r)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status: %d %s", w.Code, w.Body.String())
	}

	r = httptest.NewRequest(http.MethodGet, "/dashboard/waitlist?source=nope", nil)
	w = httptest.NewRecorder()
	h.List(w, r)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "source") {
		t.Fatalf("source: %d %s", w.Code, w.Body.String())
	}

	r = withPrincipal(httptest.NewRequest(http.MethodGet, "/dashboard/waitlist?status=open", nil), registrarKashirka())
	w = httptest.NewRecorder()
	h.List(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("list: %d %s", w.Code, w.Body.String())
	}
	if len(svc.last.BranchIDs) != 1 || svc.last.BranchIDs[0] != 106 || svc.last.ExplicitBranches {
		t.Fatalf("branch ids: %v explicit=%v", svc.last.BranchIDs, svc.last.ExplicitBranches)
	}

	before := svc.last
	r = withPrincipal(httptest.NewRequest(http.MethodGet, "/dashboard/waitlist?branch=3", nil), registrarKashirka())
	w = httptest.NewRecorder()
	h.List(w, r)
	if w.Code != http.StatusForbidden {
		t.Fatalf("kurkino: %d %s", w.Code, w.Body.String())
	}
	if svc.last.BranchID != before.BranchID || len(svc.last.BranchIDs) != len(before.BranchIDs) {
		t.Fatal("forbidden branch must not be queried")
	}

	r = withPrincipal(httptest.NewRequest(http.MethodGet, "/dashboard/waitlist?branch=496", nil), registrarKashirka())
	w = httptest.NewRecorder()
	h.List(w, r)
	if w.Code != http.StatusForbidden {
		t.Fatalf("kurkino 2: %d", w.Code)
	}

	r = withPrincipal(httptest.NewRequest(http.MethodGet, "/dashboard/waitlist?status=open", nil), managerAll())
	w = httptest.NewRecorder()
	h.List(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("manager: %d", w.Code)
	}
	if len(svc.last.BranchIDs) != 3 || svc.last.BranchIDs[0] != 106 || svc.last.BranchIDs[1] != 3 || svc.last.BranchIDs[2] != 496 || svc.last.ExplicitBranches {
		t.Fatalf("manager branches: %v explicit=%v", svc.last.BranchIDs, svc.last.ExplicitBranches)
	}

	r = withPrincipal(httptest.NewRequest(http.MethodGet, "/dashboard/waitlist?branch=106,3", nil), registrarKashirka())
	w = httptest.NewRecorder()
	h.List(w, r)
	if w.Code != http.StatusForbidden {
		t.Fatalf("registrar pair: %d %s", w.Code, w.Body.String())
	}

	r = withPrincipal(httptest.NewRequest(http.MethodGet, "/dashboard/waitlist?branch=106,3", nil), managerAll())
	w = httptest.NewRecorder()
	h.List(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("manager pair: %d %s", w.Code, w.Body.String())
	}
	if svc.last.BranchID != nil || len(svc.last.BranchIDs) != 2 || svc.last.BranchIDs[0] != 106 || svc.last.BranchIDs[1] != 3 || !svc.last.ExplicitBranches {
		t.Fatalf("manager filter id=%v ids=%v explicit=%v", svc.last.BranchID, svc.last.BranchIDs, svc.last.ExplicitBranches)
	}

	r = withPrincipal(httptest.NewRequest(http.MethodGet, "/dashboard/waitlist?branch=106,999", nil), managerAll())
	w = httptest.NewRecorder()
	h.List(w, r)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("unknown: %d %s", w.Code, w.Body.String())
	}

	r = withPrincipal(httptest.NewRequest(http.MethodGet, "/dashboard/waitlist?q=И", nil), managerAll())
	w = httptest.NewRecorder()
	h.List(w, r)
	if w.Code != http.StatusOK || svc.last.Query != "" {
		t.Fatalf("one letter query=%q status=%d", svc.last.Query, w.Code)
	}
}

func scheduleBranchIDs(t *testing.T, raw []byte) []int {
	t.Helper()
	var body struct {
		Data struct {
			Branches []struct {
				BranchID int `json:"branch_id"`
			} `json:"branches"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	ids := make([]int, 0, len(body.Data.Branches))
	for _, b := range body.Data.Branches {
		ids = append(ids, b.BranchID)
	}
	return ids
}
