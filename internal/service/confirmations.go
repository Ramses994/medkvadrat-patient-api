package service

import (
	"context"

	"github.com/medkvadrat/medkvadrat-patient-api/internal/confirmations"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/repo"
)

func (s *Services) UpsertConfirmation(ctx context.Context, planningID, patientID int64, status, source, apptVersion string) (confirmations.Record, error) {
	appt, err := repo.PlanningAppointmentByID(ctx, s.MSSQL, planningID)
	if err != nil {
		return confirmations.Record{}, err
	}
	if appt.PatientID != patientID {
		return confirmations.Record{}, confirmations.ErrPatientMismatch
	}
	current := repo.ApptVersion(appt.DateStart, appt.PlSubjID, appt.PlExamID)
	if apptVersion == "" || apptVersion != current {
		return confirmations.Record{}, confirmations.ErrStaleReminder
	}
	return confirmations.NewRepo(s.SQLite).Upsert(ctx, planningID, patientID, status, source, appt.DateStart, current)
}
