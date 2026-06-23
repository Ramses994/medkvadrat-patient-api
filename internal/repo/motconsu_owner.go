package repo

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/medkvadrat/medkvadrat-patient-api/internal/confirmations"
)

// MotconsuPatientID returns PATIENTS_ID for an appointment (read-only Medialog check).
func MotconsuPatientID(ctx context.Context, db *sql.DB, motconsuID int64) (int64, error) {
	var patientID int64
	err := db.QueryRowContext(ctx, `
SELECT PATIENTS_ID FROM MOTCONSU WHERE MOTCONSU_ID = @id`,
		sql.Named("id", motconsuID),
	).Scan(&patientID)
	if err == sql.ErrNoRows {
		return 0, confirmations.ErrAppointmentNotFound
	}
	if err != nil {
		return 0, fmt.Errorf("motconsu patient lookup: %w", err)
	}
	return patientID, nil
}
