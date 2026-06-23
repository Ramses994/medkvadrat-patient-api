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

type fakeRemindersSvc struct{}

func (fakeRemindersSvc) RemindersDue(ctx context.Context, from, to time.Time) ([]repo.DueReminder, error) {
	return []repo.DueReminder{{
		MotconsuID:       12345,
		PatientID:        6789,
		PatientPhone:     "79991234567",
		PatientName:      "Иванова Анна Петровна",
		DoctorName:       "Смирнов Иван",
		DepartmentID:     10,
		DepartmentLabel:  "Каширка",
		DateConsultation: time.Date(2026, 6, 24, 10, 30, 0, 0, time.FixedZone("MSK", 3*3600)),
	}}, nil
}

func TestGolden_Reminders_Due_OK(t *testing.T) {
	h := RemindersHandler{
		Svc: fakeRemindersSvc{},
		Now: func() time.Time {
			return time.Date(2026, 6, 23, 12, 0, 0, 0, time.FixedZone("MSK", 3*3600))
		},
	}
	r := httptest.NewRequest(http.MethodGet, "/api/reminders/due?from=2026-06-23T00:00:00&to=2026-06-26T23:59:59", nil)
	w := httptest.NewRecorder()
	h.Due(w, r)
	assertGolden(t, "reminders_due_ok", w.Body.Bytes())
}

func TestGolden_Reminders_Due_ValidationError(t *testing.T) {
	h := RemindersHandler{
		Svc: fakeRemindersSvc{},
		Now: func() time.Time {
			return time.Date(2026, 6, 23, 12, 0, 0, 0, time.FixedZone("MSK", 3*3600))
		},
	}
	r := httptest.NewRequest(http.MethodGet, "/api/reminders/due?from=not-a-date&to=2026-06-26T23:59:59", nil)
	w := httptest.NewRecorder()
	h.Due(w, r)
	assertGolden(t, "reminders_due_validation_error", w.Body.Bytes())
}

func Test__GenerateRemindersGolden(t *testing.T) {
	if os.Getenv("UPDATE_GOLDEN") != "1" {
		t.Skip("set UPDATE_GOLDEN=1 to (re)generate golden files")
	}
	now := func() time.Time {
		return time.Date(2026, 6, 23, 12, 0, 0, 0, time.FixedZone("MSK", 3*3600))
	}
	h := RemindersHandler{Svc: fakeRemindersSvc{}, Now: now}
	{
		r := httptest.NewRequest(http.MethodGet, "/api/reminders/due?from=2026-06-23T00:00:00&to=2026-06-26T23:59:59", nil)
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
}
