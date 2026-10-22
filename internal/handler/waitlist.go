package handler

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/medkvadrat/medkvadrat-patient-api/internal/confirmations"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/middleware"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/response"
)

type WaitlistService interface {
	DashboardWaitlist(ctx context.Context, filter confirmations.WaitlistListFilter) (confirmations.DashboardPage, error)
	UpdateWaitlist(ctx context.Context, id int64, status, outcome, updatedBy string) (confirmations.WaitlistRow, error)
}

type WaitlistHandler struct {
	Svc    WaitlistService
	Logger *slog.Logger
}

type waitlistItemDTO struct {
	ID              int64  `json:"id"`
	PlanningID      int64  `json:"planning_id"`
	PatientID       int64  `json:"patient_id"`
	Reason          string `json:"reason"`
	Status          string `json:"status"`
	Outcome         string `json:"outcome,omitempty"`
	CreatedAt       string `json:"created_at"`
	CreatedAtMSK    string `json:"created_at_msk"`
	BranchID        int    `json:"branch_id"`
	PatientName     string `json:"patient_name"`
	PatientPhone    string `json:"patient_phone"`
	PatientBirth    string `json:"patient_birth"`
	OrigDateStart   string `json:"orig_date_start"`
	OrigDoctor      string `json:"orig_doctor"`
	OrigService     string `json:"orig_service"`
	MedialogState   string `json:"medialog_state"`
	FutureLabel     string `json:"future_label,omitempty"`
	FutureDateStart string `json:"future_date_start,omitempty"`
	FutureDoctor    string `json:"future_doctor,omitempty"`
	FutureService   string `json:"future_service,omitempty"`
}

type waitlistPageDTO struct {
	Items     []waitlistItemDTO `json:"items"`
	Total     int               `json:"total"`
	OpenTotal int               `json:"open_total"`
	Page      int               `json:"page"`
	PerPage   int               `json:"per_page"`
}

func (h WaitlistHandler) List(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	q := r.URL.Query()
	status := strings.ToLower(strings.TrimSpace(q.Get("status")))
	if status == "" {
		status = "open"
	}
	if status != "open" && status != "all" {
		response.Error(w, http.StatusBadRequest, "VALIDATION", "status: open или all")
		return
	}
	filter := confirmations.WaitlistListFilter{OpenOnly: status == "open"}
	if raw := strings.TrimSpace(q.Get("branch")); raw != "" {
		b, err := strconv.Atoi(raw)
		if err != nil || (b != 106 && b != 3 && b != 496) {
			response.Error(w, http.StatusBadRequest, "VALIDATION", "branch: 106, 3 или 496")
			return
		}
		filter.BranchID = &b
	}
	page, _ := strconv.Atoi(q.Get("page"))
	per, _ := strconv.Atoi(q.Get("per_page"))
	filter.Page = page
	filter.PerPage = per

	pageData, err := h.Svc.DashboardWaitlist(r.Context(), filter)
	if err != nil {
		h.Logger.Error("waitlist list failed", "err", err)
		response.Error(w, http.StatusInternalServerError, "INTERNAL", "Внутренняя ошибка")
		return
	}
	items := make([]waitlistItemDTO, 0, len(pageData.Items))
	for _, it := range pageData.Items {
		items = append(items, waitlistItemDTO{
			ID: it.ID, PlanningID: it.PlanningID, PatientID: it.PatientID,
			Reason: it.Reason, Status: it.Status, Outcome: it.Outcome,
			CreatedAt: it.CreatedAt, CreatedAtMSK: it.CreatedAtMSK, BranchID: it.BranchID,
			PatientName: it.PatientName, PatientPhone: it.PatientPhone, PatientBirth: it.PatientBirth,
			OrigDateStart: it.OrigDateStart, OrigDoctor: it.OrigDoctor, OrigService: it.OrigService,
			MedialogState: it.MedialogState, FutureLabel: it.FutureLabel,
			FutureDateStart: it.FutureDateStart, FutureDoctor: it.FutureDoctor, FutureService: it.FutureService,
		})
	}
	response.OK(w, waitlistPageDTO{
		Items: items, Total: pageData.Total, OpenTotal: pageData.OpenTotal,
		Page: pageData.Page, PerPage: pageData.PerPage,
	})
}

type waitlistUpdateRequest struct {
	Status  string `json:"status"`
	Outcome string `json:"outcome"`
}

func (h WaitlistHandler) Update(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		response.Error(w, http.StatusBadRequest, "VALIDATION", "id")
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		response.Error(w, http.StatusBadRequest, "VALIDATION", "Некорректное тело запроса")
		return
	}
	var req waitlistUpdateRequest
	if err := json.Unmarshal(body, &req); err != nil {
		response.Error(w, http.StatusBadRequest, "VALIDATION", "Некорректный JSON")
		return
	}

	rec, err := h.Svc.UpdateWaitlist(r.Context(), id, req.Status, req.Outcome, middleware.StaffUpdatedBy(r))
	if err != nil {
		switch {
		case errors.Is(err, confirmations.ErrWaitlistNotFound):
			response.Error(w, http.StatusNotFound, "NOT_FOUND", "Строка листа не найдена")
		case errors.Is(err, confirmations.ErrInvalidWaitlistTransition):
			response.Error(w, http.StatusUnprocessableEntity, "INVALID_TRANSITION", "Недопустимый переход статуса")
		case errors.Is(err, confirmations.ErrUnknownWaitlistOutcome):
			response.Error(w, http.StatusUnprocessableEntity, "VALIDATION", "Недопустимый итог звонка")
		default:
			h.Logger.Error("waitlist update failed", "err", err, "waitlist_id", id, "status", req.Status, "outcome", req.Outcome)
			response.Error(w, http.StatusInternalServerError, "INTERNAL", "Внутренняя ошибка")
		}
		return
	}
	if h.Logger != nil {
		h.Logger.Info("waitlist updated", "waitlist_id", rec.ID, "status", rec.Status, "outcome", rec.Outcome)
	}
	response.OK(w, waitlistItemDTO{
		ID: rec.ID, PlanningID: rec.PlanningID, PatientID: rec.PatientID,
		Reason: rec.Reason, Status: rec.Status, Outcome: rec.Outcome,
		CreatedAt: rec.CreatedAt, CreatedAtMSK: rec.CreatedAtMSK, BranchID: rec.BranchID,
	})
}
