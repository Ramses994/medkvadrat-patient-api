package service

import (
	"context"
	"time"

	"github.com/medkvadrat/medkvadrat-patient-api/internal/confirmations"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/repo"
)

func (s *Services) DashboardSchedule(ctx context.Context, date time.Time) (repo.DashboardSchedule, error) {
	rows, err := repo.DashboardPlanningRows(ctx, s.MSSQL, date)
	if err != nil {
		return repo.DashboardSchedule{}, err
	}

	ids := make([]int64, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.PlanningID)
	}

	overlay, err := confirmations.NewRepo(s.SQLite).StatusByPlanningIDs(ctx, ids)
	if err != nil {
		return repo.DashboardSchedule{}, err
	}

	return repo.BuildDashboardSchedule(date, rows, overlay), nil
}
