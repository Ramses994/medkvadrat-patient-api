package handler

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/medkvadrat/medkvadrat-patient-api/internal/repo"
)

type fakeNotifySvc struct{}

func (fakeNotifySvc) NewBookings(context.Context, time.Time, []int64, *repo.BookingCursor, int) (repo.NewBookingsPage, error) {
	loc := time.FixedZone("MSK", 3*3600)
	one := 1
	row := repo.NewBooking{
		DueReminder: repo.DueReminder{
			PlanningID: 10, PatientID: 1, DoctorName: "Иванов Иван", Service: "Прием терапевта",
			MessengerNotify: &one, ApptVersion: "abc123def456",
			BranchID: 3, BranchCode: "Куркино",
			DateConsultation: time.Date(2026, 6, 26, 19, 30, 0, 0, loc),
		},
		CreatedAt: "2026-06-23 12:00:00.123",
	}
	return repo.NewBookingsPage{
		Rows:       []repo.NewBooking{row},
		NextCursor: &repo.BookingCursor{CreatedAt: row.CreatedAt, PlanningID: row.PlanningID},
		HasMore:    false,
	}, nil
}

func TestGolden_NewBookings_OK(t *testing.T) {
	h := NotificationsHandler{Svc: fakeNotifySvc{}, Now: func() time.Time {
		return time.Date(2026, 6, 23, 12, 0, 0, 0, time.FixedZone("MSK", 3*3600))
	}}
	r := httptest.NewRequest(http.MethodPost, "/api/notifications/new-bookings", bytes.NewBufferString(`{"patient_ids":[1,1587578]}`))
	w := httptest.NewRecorder()
	h.NewBookings(w, r)
	assertGolden(t, "new_bookings_ok", w.Body.Bytes())
}

func TestGolden_NewBookings_PatientIDsRequired(t *testing.T) {
	h := NotificationsHandler{Svc: fakeNotifySvc{}}
	r := httptest.NewRequest(http.MethodPost, "/api/notifications/new-bookings", bytes.NewBufferString(`{}`))
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
		h.NewBookings(w, httptest.NewRequest(http.MethodPost, "/api/notifications/new-bookings", bytes.NewBufferString(`{"patient_ids":[1]}`)))
		writeGolden(t, "new_bookings_ok", w.Body.Bytes())
	}
	{
		w := httptest.NewRecorder()
		h.NewBookings(w, httptest.NewRequest(http.MethodPost, "/api/notifications/new-bookings", bytes.NewBufferString(`{}`)))
		writeGolden(t, "new_bookings_patient_ids_required", w.Body.Bytes())
	}
}
