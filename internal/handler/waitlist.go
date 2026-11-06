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
	"github.com/medkvadrat/medkvadrat-patient-api/internal/staff"
)

type WaitlistService interface {
	DashboardWaitlist(ctx context.Context, filter confirmations.WaitlistListFilter) (confirmations.DashboardPage, error)
	PatchWaitlist(ctx context.Context, id int64, version int, outcome, action, updatedBy string) (confirmations.WaitlistRow, error)
	PatchRegistryWaitlist(ctx context.Context, planningID int64, version int, outcome, action, updatedBy string) (confirmations.MisWork, error)
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
	Source          string `json:"source,omitempty"`
	Comment         string `json:"comment,omitempty"`
	CreatorName     string `json:"creator_name,omitempty"`
	UpdatedBy       string `json:"updated_by,omitempty"`
	UpdatedAt       string `json:"updated_at,omitempty"`
	Version         int    `json:"version"`
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
	filter := confirmations.WaitlistListFilter{}
	switch status {
	case "open":
		filter.OpenOnly = true
	case "all":
	case confirmations.WaitlistStatusNew, confirmations.WaitlistStatusContacted, confirmations.WaitlistStatusRebooked, confirmations.WaitlistStatusClosed:
		filter.Status = status
	default:
		response.Error(w, http.StatusBadRequest, "VALIDATION", "status: open, all, new, contacted, rebooked или closed")
		return
	}
	source := strings.ToLower(strings.TrimSpace(q.Get("source")))
	switch source {
	case "", "all", "max", "registry":
		filter.Source = source
	default:
		response.Error(w, http.StatusBadRequest, "VALIDATION", "source: all, max или registry")
		return
	}
	if p, ok := staff.PrincipalFrom(r.Context()); ok {
		filter.BranchIDs = p.VisibleBranches()
	}
	if raw := strings.TrimSpace(q.Get("branch")); raw != "" {
		b, err := strconv.Atoi(raw)
		if err != nil || (b != 106 && b != 3 && b != 496) {
			response.Error(w, http.StatusBadRequest, "VALIDATION", "branch: 106, 3 или 496")
			return
		}
		if p, ok := staff.PrincipalFrom(r.Context()); ok && !p.Allows(b) {
			response.Error(w, http.StatusForbidden, "FORBIDDEN", "Нет доступа к этому филиалу")
			return
		}
		filter.BranchID = &b
		filter.BranchIDs = nil
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
			Source: it.Source, Comment: it.Comment, CreatorName: it.CreatorName,
			UpdatedBy: it.UpdatedBy, UpdatedAt: it.UpdatedAt, Version: it.Version,
		})
	}
	response.OK(w, waitlistPageDTO{
		Items: items, Total: pageData.Total, OpenTotal: pageData.OpenTotal,
		Page: pageData.Page, PerPage: pageData.PerPage,
	})
}

type waitlistPatchRequest struct {
	Outcome string `json:"outcome"`
	Action  string `json:"action"`
	Version int    `json:"version"`
}

func (h WaitlistHandler) Patch(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	source := strings.ToLower(strings.TrimSpace(r.PathValue("source")))
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 || (source != "max" && source != "registry") {
		response.Error(w, http.StatusBadRequest, "VALIDATION", "source:id")
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		response.Error(w, http.StatusBadRequest, "VALIDATION", "Некорректное тело запроса")
		return
	}
	var req waitlistPatchRequest
	if err := json.Unmarshal(body, &req); err != nil {
		response.Error(w, http.StatusBadRequest, "VALIDATION", "Некорректный JSON")
		return
	}
	by := middleware.StaffUpdatedBy(r)
	if source == "registry" {
		work, err := h.Svc.PatchRegistryWaitlist(r.Context(), id, req.Version, req.Outcome, req.Action, by)
		if err != nil {
			h.writeWaitlistErr(w, err, id, workItem(work))
			return
		}
		response.OK(w, workItem(work))
		return
	}
	rec, err := h.Svc.PatchWaitlist(r.Context(), id, req.Version, req.Outcome, req.Action, by)
	if err != nil {
		h.writeWaitlistErr(w, err, id, rowItem(rec))
		return
	}
	if h.Logger != nil {
		h.Logger.Info("waitlist updated", "waitlist_id", rec.ID, "status", rec.Status)
	}
	response.OK(w, rowItem(rec))
}

func rowItem(rec confirmations.WaitlistRow) waitlistItemDTO {
	return waitlistItemDTO{
		ID: rec.ID, PlanningID: rec.PlanningID, PatientID: rec.PatientID,
		Reason: rec.Reason, Status: rec.Status, Outcome: rec.Outcome,
		CreatedAt: rec.CreatedAt, CreatedAtMSK: rec.CreatedAtMSK, BranchID: rec.BranchID,
		Source: "max", UpdatedBy: rec.UpdatedBy, UpdatedAt: rec.UpdatedAt, Version: rec.Version,
	}
}

func workItem(work confirmations.MisWork) waitlistItemDTO {
	return waitlistItemDTO{
		ID: work.PlanningID, PlanningID: work.PlanningID, Source: "registry",
		Status: work.Status, Outcome: work.Outcome, UpdatedBy: work.UpdatedBy, UpdatedAt: work.UpdatedAt,
		Version: work.Version,
	}
}

func (h WaitlistHandler) writeWaitlistErr(w http.ResponseWriter, err error, id int64, fresh waitlistItemDTO) {
	switch {
	case errors.Is(err, confirmations.ErrWaitlistNotFound):
		response.Error(w, http.StatusNotFound, "NOT_FOUND", "Строка листа не найдена")
	case errors.Is(err, confirmations.ErrWaitlistChanged):
		response.ErrorWithData(w, http.StatusConflict, "WAITLIST_CHANGED", "Строку уже изменил другой сотрудник", fresh)
	case errors.Is(err, confirmations.ErrUnknownWaitlistOutcome):
		response.Error(w, http.StatusUnprocessableEntity, "VALIDATION", "Недопустимый итог звонка")
	case errors.Is(err, staff.ErrBranchForbidden):
		response.Error(w, http.StatusForbidden, "FORBIDDEN", "Нет доступа к этому филиалу")
	default:
		if h.Logger != nil {
			h.Logger.Error("waitlist update failed", "err", err, "waitlist_id", id)
		}
		response.Error(w, http.StatusInternalServerError, "INTERNAL", "Внутренняя ошибка")
	}
}
