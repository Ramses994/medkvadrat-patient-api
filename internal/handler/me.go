package handler

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/medkvadrat/medkvadrat-patient-api/internal/middleware"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/repo"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/response"
)

type MeService interface {
	MeProfile(ctx context.Context, patientID int64) (repo.Profile, error)
	MeAppointments(ctx context.Context, patientID int64, now time.Time, kind string) ([]repo.Appointment, error)
	LabPanels(ctx context.Context, patientID string, daysBack int) ([]repo.LabPanel, error)
	MeBookAppointment(ctx context.Context, patientID int64, planningID int, now time.Time) (motconsuID int64, restored bool, err error)
	MeCancelAppointment(ctx context.Context, patientID int64, motconsuID int64, now time.Time) error
}

type MeHandler struct {
	Svc    MeService
	Logger *slog.Logger

	CancelMinHours int
}

type meProfileDTO struct {
	PatientID  int64   `json:"patient_id"`
	FullName   string  `json:"full_name"`
	Phone      string  `json:"phone"`
	BirthDate  *string `json:"birth_date,omitempty"`  // YYYY-MM-DD from NE_LE
	BirthYear  *int    `json:"birth_year,omitempty"`  // from GOD_ROGDENIQ when NE_LE empty
	Email      string  `json:"email"`
}

func (h MeHandler) Profile(w http.ResponseWriter, r *http.Request) {
	pid, ok := middleware.PatientIDFromContext(r.Context())
	if !ok {
		response.Error(w, http.StatusUnauthorized, "UNAUTHORIZED", "Unauthorized")
		return
	}
	p, err := h.Svc.MeProfile(r.Context(), pid)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			response.Error(w, http.StatusNotFound, "NOT_FOUND", "Пациент не найден")
			return
		}
		h.Logger.Error("me profile failed", "err", err)
		response.Error(w, http.StatusInternalServerError, "INTERNAL", "Внутренняя ошибка")
		return
	}
	out := meProfileDTO{PatientID: p.PatientID, FullName: p.FullName, Phone: p.Phone, Email: p.Email}
	if p.BirthDate != nil {
		s := p.BirthDate.UTC().Format("2006-01-02")
		out.BirthDate = &s
	} else {
		out.BirthYear = p.BirthYear
	}
	response.OK(w, out)
}

type meAppointmentDTO struct {
	MotconsuID       int64  `json:"motconsu_id"`
	DoctorID         int64  `json:"doctor_id"`
	DoctorName       string `json:"doctor_name"`
	DateConsultation string `json:"date_consultation"`
	RecStatus        string `json:"rec_status"`
	PlanningID       *int64 `json:"planning_id,omitempty"`

	CanCancel       bool   `json:"can_cancel"`
	CanCancelReason string `json:"can_cancel_reason"`
}

func (h MeHandler) Appointments(w http.ResponseWriter, r *http.Request) {
	pid, ok := middleware.PatientIDFromContext(r.Context())
	if !ok {
		response.Error(w, http.StatusUnauthorized, "UNAUTHORIZED", "Unauthorized")
		return
	}
	kind := r.URL.Query().Get("status")
	now := time.Now()
	rows, err := h.Svc.MeAppointments(r.Context(), pid, now, kind)
	if err != nil {
		if err.Error() == "invalid kind" {
			response.Error(w, http.StatusBadRequest, "VALIDATION", "status: upcoming|past")
			return
		}
		h.Logger.Error("me appointments failed", "err", err)
		response.Error(w, http.StatusInternalServerError, "INTERNAL", "Внутренняя ошибка")
		return
	}
	minH := h.CancelMinHours
	if minH <= 0 {
		minH = 24
	}
	out := make([]meAppointmentDTO, 0, len(rows))
	for _, a := range rows {
		can, reason := canCancel(a, now, time.Duration(minH)*time.Hour)
		out = append(out, meAppointmentDTO{
			MotconsuID:       a.MotconsuID,
			DoctorID:         a.DoctorID,
			DoctorName:       a.DoctorName,
			DateConsultation: a.DateConsultation.Format("2006-01-02 15:04"),
			RecStatus:        a.RecStatus,
			PlanningID:       a.PlanningID,
			CanCancel:        can,
			CanCancelReason:  reason,
		})
	}
	response.OK(w, out)
}

func canCancel(a repo.Appointment, now time.Time, minBefore time.Duration) (bool, string) {
	switch a.RecStatus {
	case "W":
		// ok
	case "D":
		return false, "already_cancelled"
	default:
		return false, "not_supported"
	}
	if a.PlanningID == nil {
		return false, "not_supported"
	}
	if a.DateConsultation.Before(now.Add(minBefore)) {
		return false, "too_late"
	}
	return true, "ok"
}

func (h MeHandler) LabPanels(w http.ResponseWriter, r *http.Request) {
	pid, ok := middleware.PatientIDFromContext(r.Context())
	if !ok {
		response.Error(w, http.StatusUnauthorized, "UNAUTHORIZED", "Unauthorized")
		return
	}
	daysBack := 90
	if s := r.URL.Query().Get("days_back"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 0 || n > 3650 {
			response.Error(w, http.StatusBadRequest, "VALIDATION", "days_back должен быть числом 0..3650")
			return
		}
		daysBack = n
	}
	rows, err := h.Svc.LabPanels(r.Context(), strconv.FormatInt(pid, 10), daysBack)
	if err != nil {
		h.Logger.Error("me lab panels failed", "err", err)
		response.Error(w, http.StatusInternalServerError, "INTERNAL", "Внутренняя ошибка")
		return
	}
	response.OK(w, rows)
}

type meBookRequest struct {
	PlanningID int `json:"planning_id"`
}

type MeBookResult struct {
	MotconsuID int64 `json:"motconsu_id"`
	Restored   bool  `json:"restored"`
}

func (h MeHandler) BookAppointment(w http.ResponseWriter, r *http.Request) {
	pid, ok := middleware.PatientIDFromContext(r.Context())
	if !ok {
		response.Error(w, http.StatusUnauthorized, "UNAUTHORIZED", "Unauthorized")
		return
	}
	var req meBookRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.Error(w, http.StatusBadRequest, "VALIDATION", "Невалидный JSON")
		return
	}
	if req.PlanningID <= 0 {
		response.Error(w, http.StatusBadRequest, "VALIDATION", "planning_id обязателен")
		return
	}
	mid, rest, err := h.Svc.MeBookAppointment(r.Context(), pid, req.PlanningID, time.Now())
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			response.Error(w, http.StatusNotFound, "NOT_FOUND", "Слот не найден")
			return
		}
		switch err.Error() {
		case "SLOT_TAKEN":
			response.Error(w, http.StatusConflict, "SLOT_TAKEN", "Слот уже занят")
		case "SLOT_IN_PAST":
			response.Error(w, http.StatusBadRequest, "SLOT_IN_PAST", "Нельзя записаться в прошлое")
		case "ALREADY_BOOKED":
			response.Error(w, http.StatusConflict, "ALREADY_BOOKED", "Вы уже записаны на этот слот")
		default:
			h.Logger.Error("me book failed", "err", err)
			response.Error(w, http.StatusInternalServerError, "INTERNAL", "Внутренняя ошибка")
		}
		return
	}
	response.OK(w, MeBookResult{MotconsuID: mid, Restored: rest})
}

func (h MeHandler) CancelAppointment(w http.ResponseWriter, r *http.Request) {
	minH := h.CancelMinHours
	if minH <= 0 {
		minH = 24
	}
	pid, ok := middleware.PatientIDFromContext(r.Context())
	if !ok {
		response.Error(w, http.StatusUnauthorized, "UNAUTHORIZED", "Unauthorized")
		return
	}
	idStr := r.PathValue("motconsu_id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil || id <= 0 {
		response.Error(w, http.StatusBadRequest, "VALIDATION", "motconsu_id должен быть числом")
		return
	}
	err = h.Svc.MeCancelAppointment(r.Context(), pid, id, time.Now())
	if err != nil {
		switch err.Error() {
		case "FORBIDDEN":
			response.Error(w, http.StatusForbidden, "FORBIDDEN", "Forbidden")
		case "ALREADY_CANCELLED":
			response.Error(w, http.StatusConflict, "ALREADY_CANCELLED", "Запись уже отменена")
		case "CANCEL_NOT_SUPPORTED":
			response.Error(w, http.StatusConflict, "CANCEL_NOT_SUPPORTED", "Эту запись можно отменить только через регистратуру +7 (499) 288-88-14")
		case "CANCEL_TOO_LATE":
			msg := fmt.Sprintf("Отмена доступна не позднее чем за %d ч. до приёма", minH)
			response.Error(w, http.StatusConflict, "CANCEL_TOO_LATE", msg)
		default:
			if errors.Is(err, sql.ErrNoRows) {
				response.Error(w, http.StatusNotFound, "NOT_FOUND", "Запись не найдена")
				return
			}
			h.Logger.Error("me cancel failed", "err", err)
			response.Error(w, http.StatusInternalServerError, "INTERNAL", "Внутренняя ошибка")
		}
		return
	}
	response.OK(w, struct{}{})
}
