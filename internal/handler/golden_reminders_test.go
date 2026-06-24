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

type fakeRemindersSvc struct {
	lastPatientIDs []int64
}

func (f *fakeRemindersSvc) RemindersDue(ctx context.Context, from, to time.Time, patientIDs []int64) ([]repo.DueReminder, error) {
	f.lastPatientIDs = patientIDs
	return []repo.DueReminder{{
		PlanningID:       11737097,
		PatientID:        1587578,
		PatientPhone:     "79265906994",
		PatientName:      "Мартынов Дмитрий",
		DoctorName:       "Гаевой Эдуард",
		BranchID:         3,
		BranchCode:       "Куркино",
		DateConsultation: time.Date(2026, 6, 26, 19, 30, 0, 0, time.FixedZone("MSK", 3*3600)),
		Status:           0,
	}}, nil
}

func TestGolden_Reminders_Due_OK(t *testing.T) {
	svc := &fakeRemindersSvc{}
	h := RemindersHandler{
		Svc: svc,
		Now: func() time.Time {
			return time.Date(2026, 6, 23, 12, 0, 0, 0, time.FixedZone("MSK", 3*3600))
		},
	}
	r := httptest.NewRequest(http.MethodGet, "/api/reminders/due?from=2026-06-26T00:00:00&to=2026-06-27T00:00:00&patient_ids=1,1587578", nil)
	w := httptest.NewRecorder()
	h.Due(w, r)
	assertGolden(t, "reminders_due_ok", w.Body.Bytes())
	if len(svc.lastPatientIDs) != 2 {
		t.Fatalf("patient_ids=%v", svc.lastPatientIDs)
	}
}

func TestGolden_Reminders_Due_ValidationError(t *testing.T) {
	h := RemindersHandler{
		Svc: &fakeRemindersSvc{},
		Now: func() time.Time {
			return time.Date(2026, 6, 23, 12, 0, 0, 0, time.FixedZone("MSK", 3*3600))
		},
	}
	r := httptest.NewRequest(http.MethodGet, "/api/reminders/due?from=not-a-date&to=2026-06-26T23:59:59", nil)
	w := httptest.NewRecorder()
	h.Due(w, r)
	assertGolden(t, "reminders_due_validation_error", w.Body.Bytes())
}

func TestGolden_Reminders_Due_InvalidPatientIDs(t *testing.T) {
	h := RemindersHandler{
		Svc: &fakeRemindersSvc{},
		Now: func() time.Time {
			return time.Date(2026, 6, 23, 12, 0, 0, 0, time.FixedZone("MSK", 3*3600))
		},
	}
	r := httptest.NewRequest(http.MethodGet, "/api/reminders/due?patient_ids=abc", nil)
	w := httptest.NewRecorder()
	h.Due(w, r)
	assertGolden(t, "reminders_due_invalid_patient_ids", w.Body.Bytes())
}

func Test__GenerateRemindersGolden(t *testing.T) {
	if os.Getenv("UPDATE_GOLDEN") != "1" {
		t.Skip("set UPDATE_GOLDEN=1 to (re)generate golden files")
	}
	now := func() time.Time {
		return time.Date(2026, 6, 23, 12, 0, 0, 0, time.FixedZone("MSK", 3*3600))
	}
	svc := &fakeRemindersSvc{}
	h := RemindersHandler{Svc: svc, Now: now}
	{
		r := httptest.NewRequest(http.MethodGet, "/api/reminders/due?from=2026-06-26T00:00:00&to=2026-06-27T00:00:00&patient_ids=1,1587578", nil)
		w := httptest.NewRecorder()
		h.Due(w, r)
		writeGolden(t, "reminders_due_ok", w.Body.Bytes())
	}
	{
		r := httptest.NewRequest(http.MethodGet, "/api/reminders/due?from=not-a-date&to=2026-06-26T23:59:59", nil)
		w := httptest.NewRecorder()
		h.Due(w, r)
		writeGolden(t, "reminders_due_validation_error", w.Body.Bytes())
	}
	{
		r := httptest.NewRequest(http.MethodGet, "/api/reminders/due?patient_ids=abc", nil)
		w := httptest.NewRecorder()
		h.Due(w, r)
		writeGolden(t, "reminders_due_invalid_patient_ids", w.Body.Bytes())
	}
}
