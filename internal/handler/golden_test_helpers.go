package handler

import (
	"context"

	"github.com/medkvadrat/medkvadrat-patient-api/internal/medialogwrite"
)

type fakePlanningLookup struct{}

func (fakePlanningLookup) PlSubjIDForPlanning(context.Context, int64) (int64, error) {
	return 1, nil
}

func (fakePlanningLookup) PlanningIDForMotconsu(context.Context, int64, int64) (int64, error) {
	return 100, nil
}

func permissiveWriteGuard() medialogwrite.HTTPDeps {
	return medialogwrite.HTTPDeps{
		Guard:  medialogwrite.NewGuard(true, true, []int64{1}, []int64{1}),
		Lookup: fakePlanningLookup{},
	}
}
