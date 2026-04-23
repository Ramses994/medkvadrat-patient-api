package repo

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

type PlanningRepo struct {
	db *sql.DB
}

func (r *PlanningRepo) FreeSlots(ctx context.Context, doctorID string, date string) ([]FreeSlot, error) {
	query := `
		SELECT p.PLANNING_ID, ps.NAME, ps.PL_SUBJ_ID,
			p.DATE_CONS, p.HEURE, p.DUREE
		FROM PLANNING p
		JOIN PL_SUBJ ps ON ps.PL_SUBJ_ID = p.PL_SUBJ_ID
		WHERE ps.MEDECINS_ID = @doctorID
			AND p.DATE_CONS >= @dateStart AND p.DATE_CONS < DATEADD(day,1,@dateStart)
			AND p.PATIENTS_ID IS NULL
			AND p.STATUS = 0
		ORDER BY p.HEURE`

	rows, err := r.db.QueryContext(ctx, query, sql.Named("doctorID", doctorID), sql.Named("dateStart", date))
	if err != nil {
		return nil, fmt.Errorf("query free slots: %w", err)
	}
	defer rows.Close()

	var slots []FreeSlot
	for rows.Next() {
		var s FreeSlot
		if err := rows.Scan(&s.PlanningID, &s.DoctorName, &s.PlSubjID, &s.DateCons, &s.Heure, &s.Duration); err != nil {
			return nil, fmt.Errorf("scan free slots: %w", err)
		}
		slots = append(slots, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows free slots: %w", err)
	}
	return slots, nil
}

type LockedSlot struct {
	PlSubjID int
	DateCons time.Time
	Heure    int
}

func (r *PlanningRepo) LockFreeSlot(tx *sql.Tx, ctx context.Context, planningID int) (LockedSlot, error) {
	var out LockedSlot
	err := tx.QueryRowContext(ctx, `
		SELECT PL_SUBJ_ID, DATE_CONS, HEURE
		FROM PLANNING WITH (UPDLOCK, HOLDLOCK)
		WHERE PLANNING_ID = @id AND PATIENTS_ID IS NULL`,
		sql.Named("id", planningID),
	).Scan(&out.PlSubjID, &out.DateCons, &out.Heure)
	if err != nil {
		return LockedSlot{}, err
	}
	return out, nil
}

func (r *PlanningRepo) FillSlot(tx *sql.Tx, ctx context.Context, planningID int, patientID int, nom, prenom string) error {
	_, err := tx.ExecContext(ctx, `
		UPDATE PLANNING SET PATIENTS_ID = @patID, NOM = @nom, PRENOM = @prenom
		WHERE PLANNING_ID = @planID`,
		sql.Named("patID", patientID),
		sql.Named("nom", nom),
		sql.Named("prenom", prenom),
		sql.Named("planID", planningID),
	)
	return err
}
