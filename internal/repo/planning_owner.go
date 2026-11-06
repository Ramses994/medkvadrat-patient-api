package repo

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/medkvadrat/medkvadrat-patient-api/internal/confirmations"
)

// PlanningAppointment is the owner and start time of a PLANNING row (read-only).
type PlanningAppointment struct {
	PatientID int64
	DateStart string // wall-clock "2006-01-02 15:04:05", empty if NULL
	PlSubjID  int64
	PlExamID  int64
	BranchID  int // PL_SUBJ.FM_INTORG_ID at lookup time
}

// PlanningPatientID returns PATIENTS_ID for a scheduled appointment (read-only).
func PlanningPatientID(ctx context.Context, db *sql.DB, planningID int64) (int64, error) {
	appt, err := PlanningAppointmentByID(ctx, db, planningID)
	if err != nil {
		return 0, err
	}
	return appt.PatientID, nil
}

// PlanningAppointmentByID returns owner and DATE_START (read-only).
func PlanningAppointmentByID(ctx context.Context, db *sql.DB, planningID int64) (PlanningAppointment, error) {
	var patientID sql.NullInt64
	var dateStart sql.NullString
	var plSubj, plExam, branch sql.NullInt64
	err := db.QueryRowContext(ctx, `
SELECT p.PATIENTS_ID, CONVERT(varchar(19), p.DATE_START, 120), ISNULL(p.PL_SUBJ_ID,0), ISNULL(p.PL_EXAM_ID,0), ISNULL(ps.FM_INTORG_ID,0)
FROM PLANNING p
LEFT JOIN PL_SUBJ ps ON ps.PL_SUBJ_ID = p.PL_SUBJ_ID
WHERE p.PLANNING_ID = @id`,
		sql.Named("id", planningID),
	).Scan(&patientID, &dateStart, &plSubj, &plExam, &branch)
	if err == sql.ErrNoRows {
		return PlanningAppointment{}, confirmations.ErrAppointmentNotFound
	}
	if err != nil {
		return PlanningAppointment{}, fmt.Errorf("planning patient lookup: %w", err)
	}
	if !patientID.Valid || patientID.Int64 <= 0 {
		return PlanningAppointment{}, confirmations.ErrAppointmentNotFound
	}
	start := ""
	if dateStart.Valid {
		start = dateStart.String
	}
	return PlanningAppointment{
		PatientID: patientID.Int64,
		DateStart: start,
		PlSubjID:  plSubj.Int64,
		PlExamID:  plExam.Int64,
		BranchID:  int(branch.Int64),
	}, nil
}
