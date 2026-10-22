package service

import (
	"context"
	"time"

	"github.com/medkvadrat/medkvadrat-patient-api/internal/confirmations"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/repo"
)

func (s *Services) DashboardSchedule(ctx context.Context, date time.Time) (repo.DashboardSchedule, error) {
	rows, err := repo.DashboardPlanningRows(ctx, s.MSSQL, date, s.Config.WaitlistExamIDs)
	if err != nil {
		return repo.DashboardSchedule{}, err
	}

	ids := make([]int64, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.PlanningID)
	}

	raw, err := confirmations.NewRepo(s.SQLite).OverlayByPlanningIDs(ctx, ids)
	if err != nil {
		return repo.DashboardSchedule{}, err
	}
	overlay := make(map[int64]repo.ConfirmationOverlay, len(raw))
	for id, o := range raw {
		overlay[id] = repo.ConfirmationOverlay{Status: o.Status, Version: o.Version}
	}

	sched := repo.BuildDashboardSchedule(date, rows, overlay)
	open, err := confirmations.NewRepo(s.SQLite).OpenWaitlistPlanningIDs(ctx, ids)
	if err != nil {
		return repo.DashboardSchedule{}, err
	}
	for i := range sched.Branches {
		for j := range sched.Branches[i].Appointments {
			a := &sched.Branches[i].Appointments[j]
			a.OnWaitlist = open[a.PlanningID]
		}
	}
	return sched, nil
}
