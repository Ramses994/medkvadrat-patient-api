package handler

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/medkvadrat/medkvadrat-patient-api/internal/repo"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/response"
)

type ScheduleService interface {
	ScheduleChanges(ctx context.Context, since time.Time) ([]repo.ScheduleChange, error)
	FreeSlots(ctx context.Context, doctorID, date string) ([]repo.FreeSlot, error)
	Book(ctx context.Context, in repo.BookInput) (repo.BookResult, error)
}

type ScheduleHandler struct {
	Svc    ScheduleService
	Logger *slog.Logger
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

type freeSlotDTO struct {
	PlanningID int    `json:"planning_id"`
	DoctorName string `json:"doctor_name"`
	PlSubjID   int    `json:"pl_subj_id"`
	Date       string `json:"date"`
	Time       string `json:"time"`
	Duration   int    `json:"duration_min"`
}

func (h ScheduleHandler) FreeSlots(w http.ResponseWriter, r *http.Request) {
	doctorID := r.URL.Query().Get("doctor_id")
	date := r.URL.Query().Get("date")
	if doctorID == "" || date == "" {
		response.Error(w, http.StatusBadRequest, "VALIDATION", "Нужны doctor_id и date (YYYY-MM-DD)")
		return
	}

	slots, err := h.Svc.FreeSlots(r.Context(), doctorID, date)
	if err != nil {
		h.Logger.Error("free slots failed", "err", err)
		response.Error(w, http.StatusInternalServerError, "INTERNAL", "Внутренняя ошибка")
		return
	}

	out := make([]freeSlotDTO, 0, len(slots))
	for _, s := range slots {
		out = append(out, freeSlotDTO{
			PlanningID: s.PlanningID,
			DoctorName: s.DoctorName,
			PlSubjID:   s.PlSubjID,
			Date:       s.DateCons.Format("2006-01-02"),
			Time:       fmt.Sprintf("%02d:%02d", s.Heure/100, s.Heure%100),
			Duration:   s.Duration,
		})
	}

	response.OK(w, out)
}

type bookRequest struct {
	PlanningID int `json:"planning_id"`
	PatientID  int `json:"patient_id"`
	ModelsID   int `json:"models_id"`
	MeddepID   int `json:"meddep_id"`
}

type bookResponse struct {
	MotconsuID int `json:"motconsu_id"`
}

func (h ScheduleHandler) Book(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		response.Error(w, http.StatusMethodNotAllowed, "VALIDATION", "POST only")
		return
	}
	var req bookRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.Error(w, http.StatusBadRequest, "VALIDATION", "Невалидный JSON")
		return
	}
	if req.PlanningID == 0 || req.PatientID == 0 {
		response.Error(w, http.StatusBadRequest, "VALIDATION", "planning_id и patient_id обязательны")
		return
	}

	res, err := h.Svc.Book(r.Context(), repo.BookInput{
		PlanningID: req.PlanningID,
		PatientID:  req.PatientID,
		ModelsID:   req.ModelsID,
		MeddepID:   req.MeddepID,
	})
	if err != nil {
		// Keep legacy user-facing message.
		if errors.Is(err, sql.ErrNoRows) {
			response.Error(w, http.StatusNotFound, "NOT_FOUND", "Слот не найден или уже занят")
			return
		}
		h.Logger.Error("book failed", "err", err)
		response.Error(w, http.StatusInternalServerError, "INTERNAL", "Внутренняя ошибка")
		return
	}

	h.Logger.Info("booked", "motconsu_id", res.MotconsuID, "planning_id", req.PlanningID, "patient_id", req.PatientID)
	response.OK(w, bookResponse{MotconsuID: res.MotconsuID})
}
