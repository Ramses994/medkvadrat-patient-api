package service

import (
	"context"

	"github.com/medkvadrat/medkvadrat-patient-api/internal/confirmations"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/repo"
)

func (s *Services) UpsertConfirmation(ctx context.Context, motconsuID, patientID int64, status, source string) (confirmations.Record, error) {
	ownerID, err := repo.MotconsuPatientID(ctx, s.MSSQL, motconsuID)
	if err != nil {
		return confirmations.Record{}, err
	}
	if ownerID != patientID {
		return confirmations.Record{}, confirmations.ErrPatientMismatch
	}
	return confirmations.NewRepo(s.SQLite).Upsert(ctx, motconsuID, patientID, status, source)
}
