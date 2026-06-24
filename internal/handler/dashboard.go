package handler

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/medkvadrat/medkvadrat-patient-api/internal/repo"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/response"
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
	PlanningID   int64   `json:"planning_id"`
	Time         string  `json:"time"`
	PatientName  string  `json:"patient_name"`
	DoctorName   string  `json:"doctor_name"`
	Status       int     `json:"status"`
	Confirmation *string `json:"confirmation"`
}

type dashboardBranchDTO struct {
	BranchID     int     `json:"branch_id"`
	BranchCode   string  `json:"branch_code"`
	Name         string  `json:"name"`
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

	sched, err := h.Svc.DashboardSchedule(r.Context(), date)
	if err != nil {
		h.Logger.Error("dashboard schedule failed", "err", err)
		response.Error(w, http.StatusInternalServerError, "INTERNAL", "Внутренняя ошибка")
		return
	}

	branches := make([]dashboardBranchDTO, 0, len(sched.Branches))
	for _, b := range sched.Branches {
		appts := make([]dashboardAppointmentDTO, 0, len(b.Appointments))
		for _, a := range b.Appointments {
			appts = append(appts, dashboardAppointmentDTO{
				PlanningID:   a.PlanningID,
				Time:         a.Time,
				PatientName:  a.PatientName,
				DoctorName:   abbrevDoctorName(a.DoctorName),
				Status:       a.Status,
				Confirmation: a.Confirmation,
			})
		}
		branches = append(branches, dashboardBranchDTO{
			BranchID:     b.BranchID,
			BranchCode:   b.BranchCode,
			Name:         b.Name,
			Appointments: appts,
		})
	}

	response.OK(w, dashboardScheduleDTO{
		Date:     sched.Date,
		Branches: branches,
	})
}
