package handler

import (
	"context"
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

func (fakeMeSvc) MeAppointments(ctx context.Context, patientID int64, now time.Time, kind string) ([]repo.Appointment, error) {
	if kind != "" && kind != "upcoming" && kind != "past" {
		return nil, errInvalidKind
	}
	return []repo.Appointment{
		{
			MotconsuID:       10,
			DoctorID:         7,
			DoctorName:       "Доктор Док",
			DateConsultation: time.Date(2099, 1, 2, 9, 30, 0, 0, time.Local),
			RecStatus:        "W",
			PlanningID:       ptrI64(123),
		},
	}, nil
}

var errInvalidKind = &fakeErr{msg: "invalid kind"}

type fakeErr struct{ msg string }

func (e *fakeErr) Error() string { return e.msg }

func (fakeMeSvc) LabPanels(ctx context.Context, patientID string, daysBack int) ([]repo.LabPanel, error) {
	return []repo.LabPanel{
		{PatdirecID: 1, PanelName: "Биохимия", OrderedAt: "2026-04-21 10:00", ReadyAt: "2026-04-22 08:00", TestsCount: 0, HasOutOfRange: false},
	}, nil
}

func ptrI64(v int64) *int64 { return &v }

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

func TestGolden_Me_Appointments_ValidationError(t *testing.T) {
	h := MeHandler{Svc: fakeMeSvc{}}
	r := httptest.NewRequest(http.MethodGet, "/api/me/appointments?status=bad", nil)
	r = r.WithContext(middleware.WithPatientID(r.Context(), 1548055))
	w := httptest.NewRecorder()
	h.Appointments(w, r)
	assertGolden(t, "me_appointments_validation_error", w.Body.Bytes())
}
