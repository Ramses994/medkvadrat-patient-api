package handler

import (
	"context"
	"database/sql"
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
