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
	"strings"
	"time"

	"github.com/medkvadrat/medkvadrat-patient-api/internal/medialogwrite"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/middleware"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/repo"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/response"
)

type MeService interface {
	MeProfile(ctx context.Context, patientID int64) (repo.Profile, error)
	MeAppointments(ctx context.Context, patientID int64, now time.Time, kind string) ([]repo.PlanningVisit, error)
	MeVisits(ctx context.Context, patientID int64, from, to time.Time) ([]repo.PlanningVisit, error)
	LabPanels(ctx context.Context, patientID string, daysBack int) ([]repo.LabPanel, error)
	MeBookAppointment(ctx context.Context, patientID int64, planningID int, now time.Time) (motconsuID int64, restored bool, err error)
	MeCancelAppointment(ctx context.Context, patientID int64, motconsuID int64, now time.Time) error
}

type MeHandler struct {
	Svc             MeService
	Logger          *slog.Logger
	WriteGuard      medialogwrite.HTTPDeps
	CancelMinHours  int
	WaitlistExamIDs []int64
}

type meProfileDTO struct {
	PatientID int64   `json:"patient_id"`
	FullName  string  `json:"full_name"`
	Phone     string  `json:"phone"`
	BirthDate *string `json:"birth_date,omitempty"` // YYYY-MM-DD from NE_LE
	BirthYear *int    `json:"birth_year,omitempty"` // from GOD_ROGDENIQ when NE_LE empty
	Email     string  `json:"email"`
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

type meBranchDTO struct {
	ID      int     `json:"id"`
	Name    string  `json:"name"`
	Address *string `json:"address"`
}

type meDoctorDTO struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

type mePlanningDTO struct {
	PlanningID   int64        `json:"planning_id"`
	StartsAt     string       `json:"starts_at"`
	DurationMin  int          `json:"duration_min"`
	State        string       `json:"state"`
	IsWaitlist   bool         `json:"is_waitlist"`
	Branch       *meBranchDTO `json:"branch"`
	Doctor       *meDoctorDTO `json:"doctor"`
	ExamName     *string      `json:"exam_name"`
	Confirmation *string      `json:"confirmation"`
}

func planningDTO(v repo.PlanningVisit, now time.Time, forceAttended bool, waitlistIDs []int64) mePlanningDTO {
	waitlist := repo.IsWaitlistExam(v.ExamName, v.PlExamID, waitlistIDs)
	state := repo.PlanningVisitState(v.Cancelled, v.Arrived, v.StartsAtFull, now, waitlist)
	if forceAttended {
		state = "attended"
	}
	dto := mePlanningDTO{
		PlanningID:   v.PlanningID,
		StartsAt:     v.StartsAt,
		DurationMin:  v.DurationMin,
		State:        state,
		IsWaitlist:   waitlist,
		Confirmation: v.Confirmation,
	}
	if name, addr, ok := repo.PlanningBranch(v.BranchID, v.BranchCode); ok {
		dto.Branch = &meBranchDTO{ID: v.BranchID, Name: name, Address: addr}
	}
	if v.MedecinsID.Valid && v.MedecinsID.Int64 > 0 {
		dto.Doctor = &meDoctorDTO{ID: v.MedecinsID.Int64, Name: v.DoctorName}
	}
	if v.ExamName != "" {
		exam := v.ExamName
		dto.ExamName = &exam
	}
	return dto
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
		h.Logger.Error("me appointments failed", "err", err, "patient_id", pid)
		response.Error(w, http.StatusInternalServerError, "INTERNAL", "Внутренняя ошибка")
		return
	}
	out := make([]mePlanningDTO, 0, len(rows))
	for _, row := range rows {
		out = append(out, planningDTO(row, now, false, h.WaitlistExamIDs))
	}
	response.OK(w, out)
}

func (h MeHandler) Visits(w http.ResponseWriter, r *http.Request) {
	pid, ok := middleware.PatientIDFromContext(r.Context())
	if !ok {
		response.Error(w, http.StatusUnauthorized, "UNAUTHORIZED", "Unauthorized")
		return
	}
	now := time.Now()
	from, to, err := parseVisitWindow(r.URL.Query().Get("from"), r.URL.Query().Get("to"), now)
	if err != nil {
		response.Error(w, http.StatusBadRequest, "VALIDATION", err.Error())
		return
	}
	rows, err := h.Svc.MeVisits(r.Context(), pid, from, to)
	if err != nil {
		h.Logger.Error("me visits failed", "err", err, "patient_id", pid)
		response.Error(w, http.StatusInternalServerError, "INTERNAL", "Внутренняя ошибка")
		return
	}
	out := make([]mePlanningDTO, 0, len(rows))
	for _, row := range rows {
		out = append(out, planningDTO(row, now, true, h.WaitlistExamIDs))
	}
	response.OK(w, out)
}

func parseVisitWindow(fromRaw, toRaw string, now time.Time) (time.Time, time.Time, error) {
	loc, err := time.LoadLocation("Europe/Moscow")
	if err != nil {
		loc = time.FixedZone("MSK", 3*3600)
	}
	today := now.In(loc)
	to := time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, loc)
	from := to.AddDate(-1, 0, 0)
	if strings.TrimSpace(fromRaw) != "" {
		from, err = time.ParseInLocation("2006-01-02", fromRaw, loc)
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("from: формат YYYY-MM-DD")
		}
	}
	if strings.TrimSpace(toRaw) != "" {
		to, err = time.ParseInLocation("2006-01-02", toRaw, loc)
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("to: формат YYYY-MM-DD")
		}
	}
	if to.Before(from) {
		return time.Time{}, time.Time{}, fmt.Errorf("to раньше from")
	}
	if from.Before(to.AddDate(-3, 0, 0)) {
		return time.Time{}, time.Time{}, fmt.Errorf("окно не больше 3 лет")
	}
	return from, to, nil
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
	medialogwrite.RespondFeatureDisabled(w)
}

// bookAppointmentLegacy — Deprecated: not an approved write path, see docs/lk/01 §4 stage 2. Kept for LK-2b.
func (h MeHandler) bookAppointmentLegacy(w http.ResponseWriter, r *http.Request) {
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
	writeCtx, cancel := context.WithTimeout(r.Context(), medialogwrite.MedialogQueryTimeout)
	defer cancel()
	logID, ok := h.WriteGuard.GuardBook(writeCtx, w, r, "POST /api/me/appointments", pid, int64(req.PlanningID))
	if !ok {
		return
	}
	mid, rest, err := h.Svc.MeBookAppointment(writeCtx, pid, req.PlanningID, time.Now())
	rid := middleware.GetRequestID(r.Context())
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			medialogwrite.FinishWriteLog(h.Logger, rid, h.WriteGuard.Log, logID, "error", "NOT_FOUND")
			response.Error(w, http.StatusNotFound, "NOT_FOUND", "Слот не найден")
			return
		}
		if errors.Is(err, medialogwrite.ErrWriteNotConfigured) {
			medialogwrite.FinishWriteLog(h.Logger, rid, h.WriteGuard.Log, logID, "error", "WRITE_NOT_CONFIGURED")
			response.Error(w, http.StatusServiceUnavailable, "WRITE_NOT_CONFIGURED", medialogwrite.PatientForbiddenMessage)
			return
		}
		switch err.Error() {
		case "SLOT_NOT_FREE":
			medialogwrite.FinishWriteLog(h.Logger, rid, h.WriteGuard.Log, logID, "conflict", "SLOT_NOT_FREE")
			response.Error(w, http.StatusConflict, "SLOT_NOT_FREE", "Слот недоступен для записи")
		case "SLOT_TAKEN":
			medialogwrite.FinishWriteLog(h.Logger, rid, h.WriteGuard.Log, logID, "conflict", "SLOT_TAKEN")
			response.Error(w, http.StatusConflict, "SLOT_TAKEN", "Слот уже занят")
		case "SLOT_IN_PAST":
			medialogwrite.FinishWriteLog(h.Logger, rid, h.WriteGuard.Log, logID, "error", "SLOT_IN_PAST")
			response.Error(w, http.StatusBadRequest, "SLOT_IN_PAST", "Нельзя записаться в прошлое")
		case "ALREADY_BOOKED":
			medialogwrite.FinishWriteLog(h.Logger, rid, h.WriteGuard.Log, logID, "conflict", "ALREADY_BOOKED")
			response.Error(w, http.StatusConflict, "ALREADY_BOOKED", "Вы уже записаны на этот слот")
		default:
			medialogwrite.FinishWriteLog(h.Logger, rid, h.WriteGuard.Log, logID, "error", "INTERNAL")
			h.Logger.Error("me book failed", "err", err)
			response.Error(w, http.StatusInternalServerError, "INTERNAL", "Внутренняя ошибка")
		}
		return
	}
	medialogwrite.FinishWriteLog(h.Logger, rid, h.WriteGuard.Log, logID, "ok", "")
	response.OK(w, MeBookResult{MotconsuID: mid, Restored: rest})
}

func (h MeHandler) CancelAppointment(w http.ResponseWriter, r *http.Request) {
	medialogwrite.RespondFeatureDisabled(w)
}

// cancelAppointmentLegacy — Deprecated: not an approved write path, see docs/lk/01 §4 stage 2. Kept for LK-2b.
func (h MeHandler) cancelAppointmentLegacy(w http.ResponseWriter, r *http.Request) {
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
	writeCtx, cancel := context.WithTimeout(r.Context(), medialogwrite.MedialogQueryTimeout)
	defer cancel()
	logID, ok := h.WriteGuard.GuardCancel(writeCtx, w, r, "DELETE /api/me/appointments/{motconsu_id}", pid, id)
	if !ok {
		return
	}
	rid := middleware.GetRequestID(r.Context())
	err = h.Svc.MeCancelAppointment(writeCtx, pid, id, time.Now())
	if err != nil {
		if errors.Is(err, medialogwrite.ErrWriteNotConfigured) {
			medialogwrite.FinishWriteLog(h.Logger, rid, h.WriteGuard.Log, logID, "error", "WRITE_NOT_CONFIGURED")
			response.Error(w, http.StatusServiceUnavailable, "WRITE_NOT_CONFIGURED", medialogwrite.PatientForbiddenMessage)
			return
		}
		switch err.Error() {
		case "FORBIDDEN":
			medialogwrite.FinishWriteLog(h.Logger, rid, h.WriteGuard.Log, logID, "error", "FORBIDDEN")
			response.Error(w, http.StatusForbidden, "FORBIDDEN", "Forbidden")
		case "ALREADY_CANCELLED":
			medialogwrite.FinishWriteLog(h.Logger, rid, h.WriteGuard.Log, logID, "conflict", "ALREADY_CANCELLED")
			response.Error(w, http.StatusConflict, "ALREADY_CANCELLED", "Запись уже отменена")
		case "CANCEL_NOT_SUPPORTED":
			medialogwrite.FinishWriteLog(h.Logger, rid, h.WriteGuard.Log, logID, "conflict", "CANCEL_NOT_SUPPORTED")
			response.Error(w, http.StatusConflict, "CANCEL_NOT_SUPPORTED", "Эту запись можно отменить только через регистратуру +7 (499) 288-88-14")
		case "CANCEL_TOO_LATE":
			msg := fmt.Sprintf("Отмена доступна не позднее чем за %d ч. до приёма", minH)
			medialogwrite.FinishWriteLog(h.Logger, rid, h.WriteGuard.Log, logID, "conflict", "CANCEL_TOO_LATE")
			response.Error(w, http.StatusConflict, "CANCEL_TOO_LATE", msg)
		default:
			if errors.Is(err, sql.ErrNoRows) {
				medialogwrite.FinishWriteLog(h.Logger, rid, h.WriteGuard.Log, logID, "error", "NOT_FOUND")
				response.Error(w, http.StatusNotFound, "NOT_FOUND", "Запись не найдена")
				return
			}
			medialogwrite.FinishWriteLog(h.Logger, rid, h.WriteGuard.Log, logID, "error", "INTERNAL")
			h.Logger.Error("me cancel failed", "err", err)
			response.Error(w, http.StatusInternalServerError, "INTERNAL", "Внутренняя ошибка")
		}
		return
	}
	medialogwrite.FinishWriteLog(h.Logger, rid, h.WriteGuard.Log, logID, "ok", "")
	response.OK(w, struct{}{})
}
