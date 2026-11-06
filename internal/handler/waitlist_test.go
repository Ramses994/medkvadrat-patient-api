package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/medkvadrat/medkvadrat-patient-api/internal/confirmations"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/middleware"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/staffsession"
)

type fakeWaitlistSvc struct {
	page confirmations.DashboardPage
	err  error
	last confirmations.WaitlistListFilter
}

func (f *fakeWaitlistSvc) DashboardWaitlist(ctx context.Context, filter confirmations.WaitlistListFilter) (confirmations.DashboardPage, error) {
	f.last = filter
	if f.err != nil {
		return confirmations.DashboardPage{}, f.err
	}
	return f.page, nil
}

func (f *fakeWaitlistSvc) UpdateWaitlist(ctx context.Context, id int64, status, outcome, updatedBy string) (confirmations.WaitlistRow, error) {
	if f.err != nil {
		return confirmations.WaitlistRow{}, f.err
	}
	return confirmations.WaitlistRow{
		ID: id, PlanningID: 10, PatientID: 20, Reason: "declined",
		Status: status, Outcome: outcome, CreatedAt: "2026-10-22T00:00:00Z",
		CreatedAtMSK: "2026-10-22 03:00:00", BranchID: 106, UpdatedBy: updatedBy,
	}, nil
}

func TestGolden_Waitlist_List_OK(t *testing.T) {
	h := WaitlistHandler{Svc: &fakeWaitlistSvc{page: confirmations.DashboardPage{
		Items: []confirmations.DashboardItem{{
			ID: 1, PlanningID: 10, PatientID: 20, Reason: "declined", Status: "new",
			CreatedAt: "2026-10-21T23:30:00Z", CreatedAtMSK: "2026-10-22 02:30:00", BranchID: 106,
			PatientName: "Иванов И.", PatientPhone: "79001234567", PatientBirth: "1980-01-01",
			OrigDateStart: "2026-10-22 10:00:00", OrigDoctor: "Петров П.", OrigService: "Терапевт",
			MedialogState: "запись действует",
		}},
		Total: 1, OpenTotal: 1, Page: 1, PerPage: 50,
	}}}
	r := httptest.NewRequest(http.MethodGet, "/dashboard/waitlist?status=open&page=1&per_page=50", nil)
	w := httptest.NewRecorder()
	h.List(w, r)
	assertGolden(t, "waitlist_ok", w.Body.Bytes())
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("cache-control=%q", w.Header().Get("Cache-Control"))
	}
}

func TestWaitlist_Update_409Changed(t *testing.T) {
	h := WaitlistHandler{Svc: &fakeWaitlistSvc{err: confirmations.ErrWaitlistChanged}}
	body := `{"status":"closed","outcome":"refused"}`
	r := httptest.NewRequest(http.MethodPost, "/dashboard/waitlist/1", bytes.NewBufferString(body))
	r.SetPathValue("id", "1")
	w := httptest.NewRecorder()
	h.Update(w, r)
	if w.Code != http.StatusConflict {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "WAITLIST_CHANGED") {
		t.Fatalf("body=%s", w.Body.String())
	}
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("no-store")
	}
}

func TestWaitlist_Update_422(t *testing.T) {
	h := WaitlistHandler{Svc: &fakeWaitlistSvc{err: confirmations.ErrInvalidWaitlistTransition}}
	body := `{"status":"new","outcome":"other"}`
	r := httptest.NewRequest(http.MethodPost, "/dashboard/waitlist/1", bytes.NewBufferString(body))
	r.SetPathValue("id", "1")
	w := httptest.NewRecorder()
	h.Update(w, r)
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("no-store")
	}
}

func TestWaitlist_Router_AuthAndOrigin(t *testing.T) {
	secret := []byte("0123456789abcdef0123456789abcdef")
	staff := middleware.RequireStaff{Secret: secret, Now: func() time.Time { return dashboardNow() }}
	h := staff.Wrap(http.HandlerFunc(WaitlistHandler{Svc: &fakeWaitlistSvc{page: confirmations.DashboardPage{
		Items: []confirmations.DashboardItem{}, Total: 0, OpenTotal: 0, Page: 1, PerPage: 50,
	}}}.List))

	r := httptest.NewRequest(http.MethodGet, "/dashboard/waitlist", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("no cookie: %d", w.Code)
	}

	val, err := staffsession.Sign(secret, dashboardNow().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	r = httptest.NewRequest(http.MethodGet, "/dashboard/waitlist", nil)
	r.AddCookie(&http.Cookie{Name: staffsession.CookieName, Value: val})
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("with cookie: %d %s", w.Code, w.Body.String())
	}
}

func TestWaitlist_MutationForbiddenWithoutOrigin(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("must not run")
	})
	h := middleware.RequireDashboardMutation(inner)
	r := httptest.NewRequest(http.MethodPost, "/dashboard/waitlist/1", bytes.NewBufferString(`{}`))
	r.Host = "example.com"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusForbidden {
		t.Fatalf("status=%d", w.Code)
	}

	r = httptest.NewRequest(http.MethodPost, "/dashboard/waitlist/1", bytes.NewBufferString(`{}`))
	r.Host = "example.com"
	r.Header.Set("X-Dashboard-Request", "1")
	r.Header.Set("Origin", "https://evil.example")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusForbidden {
		t.Fatalf("evil origin: %d", w.Code)
	}
}

func TestWaitlist_JSONRoundTripEscapesHTML(t *testing.T) {
	h := WaitlistHandler{Svc: &fakeWaitlistSvc{page: confirmations.DashboardPage{
		Items: []confirmations.DashboardItem{{
			ID: 1, PatientName: "<b>XSS</b>", MedialogState: "<img>",
		}},
		Total: 1, OpenTotal: 1, Page: 1, PerPage: 50,
	}}}
	r := httptest.NewRequest(http.MethodGet, "/dashboard/waitlist", nil)
	w := httptest.NewRecorder()
	h.List(w, r)
	var body struct {
		Data waitlistPageDTO `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Data.Items[0].PatientName != "<b>XSS</b>" {
		t.Fatalf("%q", body.Data.Items[0].PatientName)
	}
}
