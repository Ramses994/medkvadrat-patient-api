package service

import (
	"context"
	"time"

	"github.com/medkvadrat/medkvadrat-patient-api/internal/confirmations"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/repo"
)

func (s *Services) DashboardWaitlist(ctx context.Context, filter confirmations.WaitlistListFilter) (confirmations.DashboardPage, error) {
	wl := confirmations.NewRepo(s.SQLite)
	rows, total, err := wl.ListWaitlist(ctx, filter)
	if err != nil {
		return confirmations.DashboardPage{}, err
	}
	openTotal, err := wl.CountOpenWaitlist(ctx)
	if err != nil {
		return confirmations.DashboardPage{}, err
	}

	planningIDs := make([]int64, 0, len(rows))
	patientIDs := make([]int64, 0, len(rows))
	seenPt := map[int64]bool{}
	for _, row := range rows {
		planningIDs = append(planningIDs, row.PlanningID)
		if !seenPt[row.PatientID] {
			seenPt[row.PatientID] = true
			patientIDs = append(patientIDs, row.PatientID)
		}
	}

	now := time.Now().In(confirmations.MoscowLocation())
	live, err := repo.WaitlistOriginals(ctx, s.MSSQL, planningIDs, s.Config.WaitlistExamIDs)
	if err != nil {
		return confirmations.DashboardPage{}, err
	}
	futures, err := repo.WaitlistFutures(ctx, s.MSSQL, patientIDs, now, s.Config.WaitlistExamIDs)
	if err != nil {
		return confirmations.DashboardPage{}, err
	}

	items := make([]confirmations.DashboardItem, 0, len(rows))
	for _, row := range rows {
		item := confirmations.DashboardItem{
			ID:           row.ID,
			PlanningID:   row.PlanningID,
			PatientID:    row.PatientID,
			Reason:       row.Reason,
			Status:       row.Status,
			Outcome:      row.Outcome,
			CreatedAt:    row.CreatedAt,
			CreatedAtMSK: row.CreatedAtMSK,
			BranchID:     row.BranchID,
		}
		if orig, ok := live[row.PlanningID]; ok {
			item.PatientName = orig.PatientName
			item.PatientPhone = orig.Phone
			item.PatientBirth = orig.BirthDate
			item.OrigDateStart = orig.DateStart
			item.OrigDoctor = orig.DoctorName
			item.OrigService = orig.Service
			item.MedialogState = repo.MedialogStateLabel(orig, row.ApptStart)
		} else {
			item.MedialogState = "запись не найдена в Медиалоге"
		}
		if fut := repo.PickNewerFuture(row.CreatedAtMSK, futures, row.PatientID); fut != nil {
			item.FutureLabel = repo.FutureVisitLabel(fut)
			item.FutureDateStart = fut.DateStart
			item.FutureDoctor = fut.DoctorName
			item.FutureService = fut.Service
		}
		items = append(items, item)
	}

	page := filter.Page
	if page < 1 {
		page = 1
	}
	per := filter.PerPage
	if per < 1 {
		per = 50
	}
	if per > 100 {
		per = 100
	}
	return confirmations.DashboardPage{Items: items, Total: total, OpenTotal: openTotal, Page: page, PerPage: per}, nil
}

func (s *Services) UpdateWaitlist(ctx context.Context, id int64, status, outcome, updatedBy string) (confirmations.WaitlistRow, error) {
	return confirmations.NewRepo(s.SQLite).UpdateWaitlist(ctx, id, status, outcome, updatedBy, time.Now().UTC())
}
