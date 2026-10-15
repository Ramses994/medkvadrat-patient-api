package medialogwrite

import (
	"context"
	"database/sql"
)

// PlanningLookup resolves schedule line ids (read-only Medialog).
type PlanningLookup interface {
	PlSubjIDForPlanning(ctx context.Context, planningID int64) (int64, error)
	PlanningIDForMotconsu(ctx context.Context, motconsuID, patientID int64) (int64, error)
}

type DBLookup struct {
	DB *sql.DB
}

func (d DBLookup) PlSubjIDForPlanning(ctx context.Context, planningID int64) (int64, error) {
	return PlSubjIDForPlanning(ctx, d.DB, planningID)
}

func (d DBLookup) PlanningIDForMotconsu(ctx context.Context, motconsuID, patientID int64) (int64, error) {
	return PlanningIDForMotconsu(ctx, d.DB, motconsuID, patientID)
}
