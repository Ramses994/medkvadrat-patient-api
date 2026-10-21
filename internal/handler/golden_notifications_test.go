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

type fakeNotifySvc struct{}

func (fakeNotifySvc) NewBookings(context.Context, time.Time, time.Time, []int64) ([]repo.NewBooking, error) {
	loc := time.FixedZone("MSK", 3*3600)
	return []repo.NewBooking{{
		DueReminder: repo.DueReminder{
			PlanningID: 10, PatientID: 1, DoctorName: "Иванов Иван", Specialty: "Терапия",
			BranchID: 3, BranchCode: "Куркино",
			DateConsultation: time.Date(2026, 6, 26, 19, 30, 0, 0, loc),
		},
		CreatedAt: time.Date(2026, 6, 23, 12, 0, 0, 0, loc),
	}}, nil
}

func TestGolden_NewBookings_OK(t *testing.T) {
	h := NotificationsHandler{Svc: fakeNotifySvc{}, Now: func() time.Time {
		return time.Date(2026, 6, 23, 12, 0, 0, 0, time.FixedZone("MSK", 3*3600))
	}}
	r := httptest.NewRequest(http.MethodGet, "/api/notifications/new-bookings?since=2026-06-23T09:00:00%2B03:00&patient_ids=1,1587578", nil)
	w := httptest.NewRecorder()
	h.NewBookings(w, r)
	assertGolden(t, "new_bookings_ok", w.Body.Bytes())
}

func TestGolden_NewBookings_PatientIDsRequired(t *testing.T) {
	h := NotificationsHandler{Svc: fakeNotifySvc{}}
	r := httptest.NewRequest(http.MethodGet, "/api/notifications/new-bookings?since=2026-06-23T09:00:00Z", nil)
	w := httptest.NewRecorder()
	h.NewBookings(w, r)
	assertGolden(t, "new_bookings_patient_ids_required", w.Body.Bytes())
}

func Test__GenerateNotificationsGolden(t *testing.T) {
	if os.Getenv("UPDATE_GOLDEN") != "1" {
		t.Skip()
	}
	h := NotificationsHandler{Svc: fakeNotifySvc{}, Now: func() time.Time {
		return time.Date(2026, 6, 23, 12, 0, 0, 0, time.FixedZone("MSK", 3*3600))
	}}
	{
		w := httptest.NewRecorder()
		h.NewBookings(w, httptest.NewRequest(http.MethodGet, "/api/notifications/new-bookings?since=2026-06-23T09:00:00%2B03:00&patient_ids=1", nil))
		writeGolden(t, "new_bookings_ok", w.Body.Bytes())
	}
	{
		w := httptest.NewRecorder()
		h.NewBookings(w, httptest.NewRequest(http.MethodGet, "/api/notifications/new-bookings?since=2026-06-23T09:00:00Z", nil))
		writeGolden(t, "new_bookings_patient_ids_required", w.Body.Bytes())
	}
}
