package handler

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/medkvadrat/medkvadrat-patient-api/internal/confirmations"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/response"
)

type ConfirmationsService interface {
	UpsertConfirmation(ctx context.Context, motconsuID, patientID int64, status, source string) (confirmations.Record, error)
}

type ConfirmationsHandler struct {
	Svc    ConfirmationsService
	Logger *slog.Logger
	Now    func() time.Time
}

type confirmationRequest struct {
	MotconsuID int64  `json:"motconsu_id"`
	PatientID  int64  `json:"patient_id"`
	Status     string `json:"status"`
	Source     string `json:"source,omitempty"`
}

type confirmationDTO struct {
	MotconsuID int64  `json:"motconsu_id"`
	PatientID  int64  `json:"patient_id"`
	Status     string `json:"status"`
	Source     string `json:"source"`
	UpdatedAt  string `json:"updated_at"`
}

func (h ConfirmationsHandler) Upsert(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		response.Error(w, http.StatusBadRequest, "VALIDATION", "Некорректное тело запроса")
		return
	}

	var req confirmationRequest
	if err := json.Unmarshal(body, &req); err != nil {
		response.Error(w, http.StatusBadRequest, "VALIDATION", "Некорректный JSON")
		return
	}
	if req.MotconsuID <= 0 || req.PatientID <= 0 {
		response.Error(w, http.StatusBadRequest, "VALIDATION", "motconsu_id и patient_id обязательны")
		return
	}
	if !confirmations.ValidStatus(req.Status) {
		response.Error(w, http.StatusBadRequest, "VALIDATION", "status: допустимые значения confirmed, declined, reschedule")
		return
	}

	source := strings.TrimSpace(req.Source)
	if source == "" {
		source = "max_bot"
	}

	rec, err := h.Svc.UpsertConfirmation(r.Context(), req.MotconsuID, req.PatientID, req.Status, source)
	if err != nil {
		switch {
		case errors.Is(err, confirmations.ErrAppointmentNotFound):
			response.Error(w, http.StatusNotFound, "APPOINTMENT_NOT_FOUND", "Запись на приём не найдена")
		case errors.Is(err, confirmations.ErrPatientMismatch):
			response.Error(w, http.StatusForbidden, "PATIENT_MISMATCH", "Пациент не владеет этой записью")
		default:
			h.Logger.Error("confirmation upsert failed", "err", err)
			response.Error(w, http.StatusInternalServerError, "INTERNAL", "Внутренняя ошибка")
		}
		return
	}

	response.OK(w, confirmationDTO{
		MotconsuID: rec.MotconsuID,
		PatientID:  rec.PatientID,
		Status:     rec.Status,
		Source:     rec.Source,
		UpdatedAt:  rec.UpdatedAt.Format("2006-01-02T15:04:05"),
	})
}
