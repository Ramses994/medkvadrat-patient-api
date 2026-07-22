package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/medkvadrat/medkvadrat-patient-api/internal/repo"
)

type fakeDashboardSvc struct {
	lastDate time.Time
}

func (f *fakeDashboardSvc) DashboardSchedule(ctx context.Context, date time.Time) (repo.DashboardSchedule, error) {
	f.lastDate = date
	confirmed := "confirmed"
	return repo.DashboardSchedule{
		Date: "2026-06-26",
		Branches: []repo.DashboardBranchSchedule{
			{
				BranchID:   106,
				BranchCode: "Каширка",
				Name:       "Каширка",
				Appointments: []repo.DashboardAppointment{
					{PlanningID: 9001, Time: "09:00", PatientName: "Иванов И.", DoctorName: "Петров Пётр", Status: 0},
				},
			},
			{
				BranchID:   3,
				BranchCode: "Куркино",
				Name:       "Куркино",
				Appointments: []repo.DashboardAppointment{
					{PlanningID: 11737097, Time: "19:30", PatientName: "Мартынов Дмитрий", DoctorName: "Гаевой Эдуард", Status: 0, Confirmation: &confirmed},
				},
			},
			{
				BranchID:     496,
				BranchCode:   "Куркино 2 (взр.)",
				Name:         "Куркино 2",
				Appointments: []repo.DashboardAppointment{},
			},
		},
	}, nil
}

type fakeDashboardSvcEmpty struct {
	lastDate time.Time
}

func (f *fakeDashboardSvcEmpty) DashboardSchedule(ctx context.Context, date time.Time) (repo.DashboardSchedule, error) {
	f.lastDate = date
	return repo.BuildDashboardSchedule(date, nil, nil), nil
}

func dashboardNow() time.Time {
	return time.Date(2026, 6, 26, 12, 0, 0, 0, time.FixedZone("MSK", 3*3600))
}

func TestGolden_Dashboard_Schedule_OK(t *testing.T) {
	svc := &fakeDashboardSvc{}
	h := DashboardHandler{Svc: svc, Now: dashboardNow}
	r := httptest.NewRequest(http.MethodGet, "/api/dashboard/schedule?date=2026-06-26", nil)
	w := httptest.NewRecorder()
	h.Schedule(w, r)
	assertGolden(t, "dashboard_schedule_ok", w.Body.Bytes())
	if svc.lastDate.Format("2006-01-02") != "2026-06-26" {
		t.Fatalf("date=%v", svc.lastDate)
	}
}

func TestGolden_Dashboard_Data_SameShapeAsSchedule(t *testing.T) {
	// GET /dashboard/data reuses DashboardHandler.Schedule (staff cookie in router).
	svc := &fakeDashboardSvc{}
	h := DashboardHandler{Svc: svc, Now: dashboardNow}
	r := httptest.NewRequest(http.MethodGet, "/dashboard/data?date=2026-06-26", nil)
	w := httptest.NewRecorder()
	h.Schedule(w, r)
	assertGolden(t, "dashboard_schedule_ok", w.Body.Bytes())
}

func TestGolden_Dashboard_Schedule_EmptyDay(t *testing.T) {
	h := DashboardHandler{Svc: &fakeDashboardSvcEmpty{}, Now: dashboardNow}
	r := httptest.NewRequest(http.MethodGet, "/api/dashboard/schedule?date=2026-06-27", nil)
	w := httptest.NewRecorder()
	h.Schedule(w, r)
	assertGolden(t, "dashboard_schedule_empty", w.Body.Bytes())
}

func TestGolden_Dashboard_Schedule_ValidationError(t *testing.T) {
	h := DashboardHandler{Svc: &fakeDashboardSvc{}, Now: dashboardNow}
	r := httptest.NewRequest(http.MethodGet, "/api/dashboard/schedule?date=bad", nil)
	w := httptest.NewRecorder()
	h.Schedule(w, r)
	assertGolden(t, "dashboard_schedule_validation_error", w.Body.Bytes())
}

func TestGolden_Dashboard_Schedule_DefaultToday(t *testing.T) {
	svc := &fakeDashboardSvcEmpty{}
	h := DashboardHandler{Svc: svc, Now: dashboardNow}
	r := httptest.NewRequest(http.MethodGet, "/api/dashboard/schedule", nil)
	w := httptest.NewRecorder()
	h.Schedule(w, r)
	if svc.lastDate.Format("2006-01-02") != "2026-06-26" {
		t.Fatalf("default date=%v", svc.lastDate)
	}
}

func Test__GenerateDashboardGolden(t *testing.T) {
	if os.Getenv("UPDATE_GOLDEN") != "1" {
		t.Skip("set UPDATE_GOLDEN=1 to (re)generate golden files")
	}
	now := dashboardNow
	{
		h := DashboardHandler{Svc: &fakeDashboardSvc{}, Now: now}
		r := httptest.NewRequest(http.MethodGet, "/api/dashboard/schedule?date=2026-06-26", nil)
		w := httptest.NewRecorder()
		h.Schedule(w, r)
		writeGolden(t, "dashboard_schedule_ok", w.Body.Bytes())
	}
	{
		h := DashboardHandler{Svc: &fakeDashboardSvcEmpty{}, Now: now}
		r := httptest.NewRequest(http.MethodGet, "/api/dashboard/schedule?date=2026-06-27", nil)
		w := httptest.NewRecorder()
		h.Schedule(w, r)
		writeGolden(t, "dashboard_schedule_empty", w.Body.Bytes())
	}
	{
		h := DashboardHandler{Svc: &fakeDashboardSvc{}, Now: now}
		r := httptest.NewRequest(http.MethodGet, "/api/dashboard/schedule?date=bad", nil)
		w := httptest.NewRecorder()
		h.Schedule(w, r)
		writeGolden(t, "dashboard_schedule_validation_error", w.Body.Bytes())
	}
}
