package handler

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/medkvadrat/medkvadrat-patient-api/internal/repo"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/response"
)

const (
	reminderTimeLayout = "2006-01-02T15:04:05"
	reminderOutLayout  = "2006-01-02 15:04"
	maxReminderWindow  = 14 * 24 * time.Hour
	defaultReminderTo  = 72 * time.Hour
)

type RemindersService interface {
	RemindersDue(ctx context.Context, from, to time.Time) ([]repo.DueReminder, error)
}

type RemindersHandler struct {
	Svc    RemindersService
	Logger *slog.Logger
	Now    func() time.Time
}

type dueReminderDTO struct {
	MotconsuID       int64  `json:"motconsu_id"`
	PatientID        int64  `json:"patient_id"`
	PatientPhone     string `json:"patient_phone"`
	PatientName      string `json:"patient_name"`
	DoctorName       string `json:"doctor_name"`
	DepartmentID     int    `json:"department_id"`
	DepartmentLabel  string `json:"department_label"`
	DateConsultation string `json:"date_consultation"`
}

func (h RemindersHandler) now() time.Time {
	if h.Now != nil {
		return h.Now()
	}
	return time.Now().In(moscowLocation())
}

func moscowLocation() *time.Location {
	loc, err := time.LoadLocation("Europe/Moscow")
	if err != nil {
		return time.FixedZone("MSK", 3*3600)
	}
	return loc
}

func parseReminderWindow(fromStr, toStr string, now time.Time) (time.Time, time.Time, error) {
	loc := moscowLocation()
	now = now.In(loc)

	var from, to time.Time
	var err error

	if fromStr == "" {
		from = now
	} else {
		from, err = time.ParseInLocation(reminderTimeLayout, fromStr, loc)
		if err != nil {
			return time.Time{}, time.Time{}, err
		}
	}

	if toStr == "" {
		to = now.Add(defaultReminderTo)
	} else {
		to, err = time.ParseInLocation(reminderTimeLayout, toStr, loc)
		if err != nil {
			return time.Time{}, time.Time{}, err
		}
	}

	if to.Before(from) {
		return time.Time{}, time.Time{}, errReminderRange
	}
	if to.Sub(from) > maxReminderWindow {
		return time.Time{}, time.Time{}, errReminderWindow
	}
	return from, to, nil
}

var (
	errReminderRange   = errValidation("from должен быть раньше to")
	errReminderWindow  = errValidation("окно не больше 14 дней")
	errReminderFormat  = errValidation("from/to: формат 2006-01-02T15:04:05")
)

type errValidation string

func (e errValidation) Error() string { return string(e) }

func abbrevDoctorName(full string) string {
	parts := strings.Fields(strings.TrimSpace(full))
	if len(parts) == 0 {
		return ""
	}
	if len(parts) == 1 {
		return parts[0]
	}
	r := []rune(parts[1])
	if len(r) == 0 {
		return parts[0]
	}
	return parts[0] + " " + string(r[0]) + "."
}

func (h RemindersHandler) Due(w http.ResponseWriter, r *http.Request) {
	from, to, err := parseReminderWindow(r.URL.Query().Get("from"), r.URL.Query().Get("to"), h.now())
	if err != nil {
		switch {
		case err == errReminderRange, err == errReminderWindow:
			response.Error(w, http.StatusBadRequest, "VALIDATION", err.Error())
		default:
			response.Error(w, http.StatusBadRequest, "VALIDATION", errReminderFormat.Error())
		}
		return
	}

	rows, err := h.Svc.RemindersDue(r.Context(), from, to)
	if err != nil {
		h.Logger.Error("reminders due failed", "err", err)
		response.Error(w, http.StatusInternalServerError, "INTERNAL", "Внутренняя ошибка")
		return
	}

	loc := moscowLocation()
	out := make([]dueReminderDTO, 0, len(rows))
	for _, row := range rows {
		out = append(out, dueReminderDTO{
			MotconsuID:       row.MotconsuID,
			PatientID:        row.PatientID,
			PatientPhone:     row.PatientPhone,
			PatientName:      row.PatientName,
			DoctorName:       abbrevDoctorName(row.DoctorName),
			DepartmentID:     row.DepartmentID,
			DepartmentLabel:  row.DepartmentLabel,
			DateConsultation: row.DateConsultation.In(loc).Format(reminderOutLayout),
		})
	}
	response.OK(w, out)
}
