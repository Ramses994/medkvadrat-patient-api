package repo

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/medkvadrat/medkvadrat-patient-api/internal/confirmations"
)

// PlanningPatientID returns PATIENTS_ID for a scheduled appointment (read-only).
func PlanningPatientID(ctx context.Context, db *sql.DB, planningID int64) (int64, error) {
	var patientID int64
	err := db.QueryRowContext(ctx, `
SELECT PATIENTS_ID FROM PLANNING WHERE PLANNING_ID = @id`,
		sql.Named("id", planningID),
	).Scan(&patientID)
	if err == sql.ErrNoRows {
		return 0, confirmations.ErrAppointmentNotFound
	}
	if err != nil {
		return 0, fmt.Errorf("planning patient lookup: %w", err)
	}
	return patientID, nil
}
