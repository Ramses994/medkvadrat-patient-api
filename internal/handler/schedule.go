package handler

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/medkvadrat/medkvadrat-patient-api/internal/medialogwrite"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/repo"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/response"
)

type ScheduleService interface {
	ScheduleChanges(ctx context.Context, since time.Time) ([]repo.ScheduleChange, error)
}

type ScheduleHandler struct {
	Svc        ScheduleService
	Logger     *slog.Logger
	WriteGuard medialogwrite.HTTPDeps
}

type scheduleChangeDTO struct {
	MotconsuID  int    `json:"motconsu_id"`
	PatientID   int    `json:"patient_id"`
	PatientName string `json:"patient_name"`
	DoctorID    int    `json:"doctor_id"`
	DoctorName  string `json:"doctor_name"`
	DateConsult string `json:"date_consultation"`
	ModifyDate  string `json:"modify_date"`
}

func (h ScheduleHandler) Changes(w http.ResponseWriter, r *http.Request) {
	since := r.URL.Query().Get("since")
	if since == "" {
		since = time.Now().Add(-1 * time.Hour).Format("2006-01-02T15:04:05")
	}
	sinceTime, err := time.Parse("2006-01-02T15:04:05", since)
	if err != nil {
		response.Error(w, http.StatusBadRequest, "VALIDATION", "Формат since: 2006-01-02T15:04:05")
		return
	}

	changes, err := h.Svc.ScheduleChanges(r.Context(), sinceTime)
	if err != nil {
		h.Logger.Error("schedule changes failed", "err", err)
		response.Error(w, http.StatusInternalServerError, "INTERNAL", "Внутренняя ошибка")
		return
	}

	out := make([]scheduleChangeDTO, 0, len(changes))
	for _, c := range changes {
		out = append(out, scheduleChangeDTO{
			MotconsuID:  c.MotconsuID,
			PatientID:   c.PatientID,
			PatientName: c.PatientName,
			DoctorID:    c.DoctorID,
			DoctorName:  c.DoctorName,
			DateConsult: c.DateConsult.Format("2006-01-02 15:04"),
			ModifyDate:  c.ModifyDate.Format("2006-01-02 15:04:05"),
		})
	}

	response.OK(w, out)
}
