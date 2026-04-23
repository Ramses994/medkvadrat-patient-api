package handler

import (
	"context"
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
}

type MeHandler struct {
	Svc    MeService
	Logger *slog.Logger

	CancelMinHours int
}

type meProfileDTO struct {
	PatientID int64  `json:"patient_id"`
	FullName  string `json:"full_name"`
	Phone     string `json:"phone"`
	BirthDate string `json:"birth_date,omitempty"`
	Email     string `json:"email"`
}

func (h MeHandler) Profile(w http.ResponseWriter, r *http.Request) {
	pid, ok := middleware.PatientIDFromContext(r.Context())
	if !ok {
		response.Error(w, http.StatusUnauthorized, "UNAUTHORIZED", "Unauthorized")
		return
	}
	p, err := h.Svc.MeProfile(r.Context(), pid)
	if err != nil {
		h.Logger.Error("me profile failed", "err", err)
		response.Error(w, http.StatusInternalServerError, "INTERNAL", "Внутренняя ошибка")
		return
	}
	out := meProfileDTO{PatientID: p.PatientID, FullName: p.FullName, Phone: p.Phone, Email: p.Email}
	if p.BirthDate != nil {
		out.BirthDate = p.BirthDate.Format("2006-01-02")
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
