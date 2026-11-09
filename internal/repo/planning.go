package repo

import (
	"context"
	"database/sql"
	"time"
)

type PlanningRepo struct {
	db *sql.DB
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
