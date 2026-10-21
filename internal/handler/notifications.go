package handler

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/medkvadrat/medkvadrat-patient-api/internal/repo"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/response"
)

type NotificationsService interface {
	NewBookings(ctx context.Context, since, now time.Time, patientIDs []int64) ([]repo.NewBooking, error)
}

type NotificationsHandler struct {
	Svc    NotificationsService
	Logger *slog.Logger
	Now    func() time.Time
}

type newBookingDTO struct {
	PlanningID       int64  `json:"planning_id"`
	PatientID        int64  `json:"patient_id"`
	PatientPhone     string `json:"patient_phone"`
	PatientName      string `json:"patient_name"`
	DoctorName       string `json:"doctor_name"`
	Specialty        string `json:"specialty"`
	BranchID         int    `json:"branch_id"`
	BranchCode       string `json:"branch_code"`
	DateConsultation string `json:"date_consultation"`
	Status           int    `json:"status"`
	CreatedAt        string `json:"created_at"`
}

func (h NotificationsHandler) now() time.Time {
	if h.Now != nil {
		return h.Now()
	}
	return time.Now().In(moscowLocation())
}

func (h NotificationsHandler) NewBookings(w http.ResponseWriter, r *http.Request) {
	sinceRaw := r.URL.Query().Get("since")
	since, err := time.Parse(time.RFC3339, sinceRaw)
	if err != nil {
		response.Error(w, http.StatusBadRequest, "VALIDATION", "since: формат RFC3339")
		return
	}
	patientIDs, err := repo.RequirePatientIDsCSV(r.URL.Query().Get("patient_ids"), 50)
	if err != nil {
		msg := "patient_ids: ожидается CSV положительных целых"
		if err.Error() == "too many patient_ids" {
			msg = "patient_ids: не больше 50"
		}
		response.Error(w, http.StatusBadRequest, "VALIDATION", msg)
		return
	}
	rows, err := h.Svc.NewBookings(r.Context(), since, h.now(), patientIDs)
	if err != nil {
		if h.Logger != nil {
			h.Logger.Error("new bookings failed", "err", err)
		}
		response.Error(w, http.StatusInternalServerError, "INTERNAL", "Внутренняя ошибка")
		return
	}
	out := make([]newBookingDTO, 0, len(rows))
	for _, row := range rows {
		out = append(out, newBookingDTO{
			PlanningID:       row.PlanningID,
			PatientID:        row.PatientID,
			PatientPhone:     row.PatientPhone,
			PatientName:      row.PatientName,
			DoctorName:       abbrevDoctorName(row.DoctorName),
			Specialty:        row.Specialty,
			BranchID:         row.BranchID,
			BranchCode:       row.BranchCode,
			DateConsultation: row.DateConsultation.Format(reminderOutLayout),
			Status:           row.Status,
			CreatedAt:        row.CreatedAt.Format(time.RFC3339),
		})
	}
	response.OK(w, out)
}
