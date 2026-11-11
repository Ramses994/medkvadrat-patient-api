package handler

import (
	"context"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/medkvadrat/medkvadrat-patient-api/internal/repo"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/response"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/staff"
)

const dashboardDateLayout = "2006-01-02"

type DashboardService interface {
	DashboardSchedule(ctx context.Context, date time.Time) (repo.DashboardSchedule, error)
}

type DashboardHandler struct {
	Svc    DashboardService
	Logger *slog.Logger
	Now    func() time.Time
}

type dashboardAppointmentDTO struct {
	PlanningID       int64   `json:"planning_id"`
	Time             string  `json:"time"`
	PatientName      string  `json:"patient_name"`
	DoctorName       string  `json:"doctor_name"`
	Status           int     `json:"status"`
	Confirmation     *string `json:"confirmation"`
	ConfirmationHint string  `json:"confirmation_hint,omitempty"`
	IsWaitlist       bool    `json:"is_waitlist"`
	OnWaitlist       bool    `json:"on_waitlist,omitempty"`
}

type dashboardBranchDTO struct {
	BranchID     int                       `json:"branch_id"`
	BranchCode   string                    `json:"branch_code"`
	Name         string                    `json:"name"`
	Appointments []dashboardAppointmentDTO `json:"appointments"`
}

type dashboardScheduleDTO struct {
	Date     string               `json:"date"`
	Branches []dashboardBranchDTO `json:"branches"`
}

func (h DashboardHandler) now() time.Time {
	if h.Now != nil {
		return h.Now()
	}
	return time.Now().In(moscowLocation())
}

func parseDashboardDate(raw string, now time.Time) (time.Time, error) {
	loc := moscowLocation()
	if raw == "" {
		n := now.In(loc)
		return time.Date(n.Year(), n.Month(), n.Day(), 0, 0, 0, 0, loc), nil
	}
	t, err := time.ParseInLocation(dashboardDateLayout, raw, loc)
	if err != nil {
		return time.Time{}, err
	}
	return t, nil
}

func (h DashboardHandler) Schedule(w http.ResponseWriter, r *http.Request) {
	date, err := parseDashboardDate(r.URL.Query().Get("date"), h.now())
	if err != nil {
		response.Error(w, http.StatusBadRequest, "VALIDATION", "date: формат YYYY-MM-DD")
		return
	}
	if _, code, msg := dashboardBranches(r); code != 0 {
		kind := "VALIDATION"
		if code == http.StatusForbidden {
			kind = "FORBIDDEN"
		}
		response.Error(w, code, kind, msg)
		return
	}

	sched, err := h.Svc.DashboardSchedule(r.Context(), date)
	if err != nil {
		h.Logger.Error("dashboard schedule failed", "err", err)
		response.Error(w, http.StatusInternalServerError, "INTERNAL", "Внутренняя ошибка")
		return
	}

	branches := make([]dashboardBranchDTO, 0, len(sched.Branches))
	for _, b := range sched.Branches {
		if !scheduleBranchVisible(r, b.BranchID) {
			continue
		}
		appts := make([]dashboardAppointmentDTO, 0, len(b.Appointments))
		for _, a := range b.Appointments {
			appts = append(appts, dashboardAppointmentDTO{
				PlanningID:       a.PlanningID,
				Time:             a.Time,
				PatientName:      a.PatientName,
				DoctorName:       displayDoctorName(a.DoctorName),
				Status:           a.Status,
				Confirmation:     a.Confirmation,
				ConfirmationHint: a.ConfirmationHint,
				IsWaitlist:       a.IsWaitlist,
				OnWaitlist:       a.OnWaitlist,
			})
		}
		branches = append(branches, dashboardBranchDTO{
			BranchID:     b.BranchID,
			BranchCode:   b.BranchCode,
			Name:         b.Name,
			Appointments: appts,
		})
	}

	w.Header().Set("Cache-Control", "no-store")
	response.OK(w, dashboardScheduleDTO{
		Date:     sched.Date,
		Branches: branches,
	})
}

func scheduleBranchVisible(r *http.Request, branch int) bool {
	p, ok := staff.PrincipalFrom(r.Context())
	ids, code, _ := dashboardBranches(r)
	if code != 0 {
		return false
	}
	if len(ids) > 0 && !branchInList(ids, branch) {
		return false
	}
	if !ok {
		return true
	}
	return p.Allows(branch)
}

func dashboardBranches(r *http.Request) ([]int, int, string) {
	p, has := staff.PrincipalFrom(r.Context())
	raw := strings.TrimSpace(r.URL.Query().Get("branch"))
	if raw == "" {
		return nil, 0, ""
	}
	var ids []int
	seen := map[int]bool{}
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		b, err := strconv.Atoi(part)
		if err != nil || !knownDashboardBranch(b) {
			return nil, http.StatusBadRequest, "branch: 106, 3 или 496"
		}
		if has && !p.Allows(b) {
			return nil, http.StatusForbidden, "Нет доступа к этому филиалу"
		}
		if !seen[b] {
			seen[b] = true
			ids = append(ids, b)
		}
	}
	if len(ids) == 0 {
		return nil, http.StatusBadRequest, "branch: 106, 3 или 496"
	}
	return ids, 0, ""
}

func knownDashboardBranch(id int) bool {
	for _, b := range staff.DashboardBranchIDs {
		if b == id {
			return true
		}
	}
	return false
}

func branchInList(ids []int, branch int) bool {
	for _, id := range ids {
		if id == branch {
			return true
		}
	}
	return false
}
