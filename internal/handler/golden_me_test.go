package handler

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/medkvadrat/medkvadrat-patient-api/internal/middleware"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/repo"
)

type fakeMeSvc struct{}

func (fakeMeSvc) MeProfile(ctx context.Context, patientID int64) (repo.Profile, error) {
	return repo.Profile{PatientID: patientID, FullName: "Иван Иванов", Phone: "+79990000000", Email: "ivan@example.com"}, nil
}

func (fakeMeSvc) MeAppointments(ctx context.Context, patientID int64, now time.Time, kind string) ([]repo.PlanningVisit, error) {
	if kind != "" && kind != "upcoming" && kind != "past" {
		return nil, errInvalidKind
	}
	if kind == "past" {
		return pastPlanningVisits(), nil
	}
	if kind == "empty" {
		return []repo.PlanningVisit{}, nil
	}
	return upcomingPlanningVisits(), nil
}

func (fakeMeSvc) MeVisits(ctx context.Context, patientID int64, from, to time.Time) ([]repo.PlanningVisit, error) {
	confirmed := "confirmed"
	return []repo.PlanningVisit{{
		PlanningID: 11737097, StartsAt: "2026-06-26 19:30", StartsAtFull: "2026-06-26 19:30:00", DurationMin: 30,
		Arrived: 1, BranchID: 3, BranchCode: "Куркино",
		MedecinsID: sql.NullInt64{Int64: 1828, Valid: true},
		DoctorName: "Гаевой Эдуард Валерьевич", ExamName: "Приём стоматолога",
		Confirmation: &confirmed,
	}}, nil
}

func upcomingPlanningVisits() []repo.PlanningVisit {
	confirmed := "confirmed"
	return []repo.PlanningVisit{
		{
			PlanningID: 11737097, StartsAt: "2099-06-26 19:30", StartsAtFull: "2099-06-26 19:30:00", DurationMin: 30,
			BranchID: 3, BranchCode: "Куркино",
			MedecinsID: sql.NullInt64{Int64: 1828, Valid: true},
			DoctorName: "Гаевой Эдуард Валерьевич", ExamName: "Приём стоматолога",
			Confirmation: &confirmed,
		},
		{
			PlanningID: 11737098, StartsAt: "2099-06-27 10:00", StartsAtFull: "2099-06-27 10:00:00", DurationMin: 20,
			Cancelled: 1, BranchID: 999, BranchCode: "Склад",
			ExamName: "Консультация",
		},
	}
}

func pastPlanningVisits() []repo.PlanningVisit {
	return []repo.PlanningVisit{
		{
			PlanningID: 100, StartsAt: "2020-01-10 09:00", StartsAtFull: "2020-01-10 09:00:00", DurationMin: 30, Arrived: 1,
			BranchID: 106, BranchCode: "Каширка",
			MedecinsID: sql.NullInt64{Int64: 5, Valid: true}, DoctorName: "Петров Пётр",
			ExamName: "Терапевт",
		},
		{
			PlanningID: 101, StartsAt: "2020-01-11 11:00", StartsAtFull: "2020-01-11 11:00:00", DurationMin: 15,
			BranchID: 496, BranchCode: "Куркино 2",
		},
	}
}

var errInvalidKind = &fakeErr{msg: "invalid kind"}

type fakeErr struct{ msg string }

func (e *fakeErr) Error() string { return e.msg }

func (fakeMeSvc) LabPanels(ctx context.Context, patientID string, daysBack int) ([]repo.LabPanel, error) {
	return []repo.LabPanel{
		{PatdirecID: 1, PanelName: "Биохимия", OrderedAt: "2026-04-21 10:00", ReadyAt: "2026-04-22 08:00", TestsCount: 0, HasOutOfRange: false},
	}, nil
}

func (fakeMeSvc) MeBookAppointment(ctx context.Context, patientID int64, planningID int, now time.Time) (int64, bool, error) {
	return 555, false, nil
}

func (fakeMeSvc) MeCancelAppointment(ctx context.Context, patientID int64, motconsuID int64, now time.Time) error {
	return nil
}

func TestGolden_Me_Profile_OK(t *testing.T) {
	h := MeHandler{Svc: fakeMeSvc{}}
	r := httptest.NewRequest(http.MethodGet, "/api/me/profile", nil)
	r = r.WithContext(middleware.WithPatientID(r.Context(), 1548055))
	w := httptest.NewRecorder()
	h.Profile(w, r)
	assertGolden(t, "me_profile_ok", w.Body.Bytes())
}

func TestGolden_Me_Appointments_OK(t *testing.T) {
	h := MeHandler{Svc: fakeMeSvc{}, CancelMinHours: 24}
	r := httptest.NewRequest(http.MethodGet, "/api/me/appointments", nil)
	r = r.WithContext(middleware.WithPatientID(r.Context(), 1548055))
	w := httptest.NewRecorder()
	h.Appointments(w, r)
	assertGolden(t, "me_appointments_ok", w.Body.Bytes())
}

func TestGolden_Me_LabPanels_OK(t *testing.T) {
	h := MeHandler{Svc: fakeMeSvc{}}
	r := httptest.NewRequest(http.MethodGet, "/api/me/lab-panels", nil)
	r = r.WithContext(middleware.WithPatientID(r.Context(), 1548055))
	w := httptest.NewRecorder()
	h.LabPanels(w, r)
	assertGolden(t, "me_lab_panels_ok", w.Body.Bytes())
}

type emptyPlanningSvc struct{ fakeMeSvc }

func (emptyPlanningSvc) MeAppointments(context.Context, int64, time.Time, string) ([]repo.PlanningVisit, error) {
	return []repo.PlanningVisit{}, nil
}

func TestGolden_Me_Appointments_Past(t *testing.T) {
	h := MeHandler{Svc: fakeMeSvc{}}
	r := httptest.NewRequest(http.MethodGet, "/api/me/appointments?status=past", nil)
	r = r.WithContext(middleware.WithPatientID(r.Context(), 1548055))
	w := httptest.NewRecorder()
	h.Appointments(w, r)
	assertGolden(t, "me_appointments_past", w.Body.Bytes())
}

func TestGolden_Me_Appointments_Empty(t *testing.T) {
	h := MeHandler{Svc: emptyPlanningSvc{}}
	r := httptest.NewRequest(http.MethodGet, "/api/me/appointments?status=upcoming", nil)
	r = r.WithContext(middleware.WithPatientID(r.Context(), 1548055))
	w := httptest.NewRecorder()
	h.Appointments(w, r)
	assertGolden(t, "me_appointments_empty", w.Body.Bytes())
}

func TestGolden_Me_Visits(t *testing.T) {
	h := MeHandler{Svc: fakeMeSvc{}}
	r := httptest.NewRequest(http.MethodGet, "/api/me/visits?from=2026-01-01&to=2026-10-14", nil)
	r = r.WithContext(middleware.WithPatientID(r.Context(), 1548055))
	w := httptest.NewRecorder()
	h.Visits(w, r)
	assertGolden(t, "me_visits_ok", w.Body.Bytes())
}

type waitlistPlanningSvc struct{ fakeMeSvc }

func (waitlistPlanningSvc) MeAppointments(context.Context, int64, time.Time, string) ([]repo.PlanningVisit, error) {
	return []repo.PlanningVisit{{
		PlanningID: 3118001, StartsAt: "2099-06-26 19:30", StartsAtFull: "2099-06-26 19:30:00",
		DurationMin: 10, BranchID: 3, BranchCode: "Куркино",
		MedecinsID: sql.NullInt64{Int64: 1828, Valid: true},
		DoctorName: "Гаевой Эдуард Валерьевич", PlExamID: 3118, ExamName: "Приём терапевта",
	}}, nil
}

func TestGolden_Me_Appointments_Waitlist(t *testing.T) {
	h := MeHandler{Svc: waitlistPlanningSvc{}}
	r := httptest.NewRequest(http.MethodGet, "/api/me/appointments", nil)
	r = r.WithContext(middleware.WithPatientID(r.Context(), 1548055))
	w := httptest.NewRecorder()
	h.Appointments(w, r)
	assertGolden(t, "me_appointments_waitlist", w.Body.Bytes())
}

func TestGolden_Me_Appointments_ValidationError(t *testing.T) {
	h := MeHandler{Svc: fakeMeSvc{}}
	r := httptest.NewRequest(http.MethodGet, "/api/me/appointments?status=bad", nil)
	r = r.WithContext(middleware.WithPatientID(r.Context(), 1548055))
	w := httptest.NewRecorder()
	h.Appointments(w, r)
	assertGolden(t, "me_appointments_validation_error", w.Body.Bytes())
}
