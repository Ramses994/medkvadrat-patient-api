package repo

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

type MotconsuRepo struct {
	db *sql.DB
}

func (r *MotconsuRepo) MaxModifyDate(ctx context.Context) (time.Time, error) {
	var t time.Time
	row := r.db.QueryRowContext(ctx, "SELECT ISNULL(MAX(KRN_MODIFY_DATE), GETDATE()) FROM MOTCONSU")
	if err := row.Scan(&t); err != nil {
		return time.Time{}, fmt.Errorf("scan max modify date: %w", err)
	}
	return t, nil
}

func (r *MotconsuRepo) ChangesSince(ctx context.Context, since time.Time) ([]ScheduleChange, error) {
	query := `
		SELECT m.MOTCONSU_ID, m.PATIENTS_ID, m.MEDECINS_ID,
			m.DATE_CONSULTATION, m.KRN_MODIFY_DATE,
			ISNULL(p.NOM,'') + ' ' + ISNULL(p.PRENOM,'') AS PAT_NAME,
			ISNULL(d.NOM,'') + ' ' + ISNULL(d.PRENOM,'') AS DOC_NAME
		FROM MOTCONSU m
		LEFT JOIN PATIENTS p ON p.PATIENTS_ID = m.PATIENTS_ID
		LEFT JOIN MEDECINS d ON d.MEDECINS_ID = m.MEDECINS_ID
		WHERE m.KRN_MODIFY_DATE > @since
		ORDER BY m.KRN_MODIFY_DATE DESC`

	rows, err := r.db.QueryContext(ctx, query, sql.Named("since", since))
	if err != nil {
		return nil, fmt.Errorf("query changes: %w", err)
	}
	defer rows.Close()

	var changes []ScheduleChange
	for rows.Next() {
		var c ScheduleChange
		if err := rows.Scan(&c.MotconsuID, &c.PatientID, &c.DoctorID, &c.DateConsult, &c.ModifyDate, &c.PatientName, &c.DoctorName); err != nil {
			return nil, fmt.Errorf("scan changes: %w", err)
		}
		changes = append(changes, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows changes: %w", err)
	}
	return changes, nil
}

type PollRow struct {
	MotconsuID    int
	PatientID     int
	DoctorID      int
	DateConsult   time.Time
	ModifyDate    time.Time
	PatientNom    string
	PatientPrenom string
	DoctorNom     string
	DoctorPrenom  string
}

func (r *MotconsuRepo) PollAfter(ctx context.Context, last time.Time) ([]PollRow, error) {
	query := `
		SELECT m.MOTCONSU_ID, m.PATIENTS_ID, m.MEDECINS_ID,
			m.DATE_CONSULTATION, m.KRN_MODIFY_DATE,
			ISNULL(p.NOM,'') AS PAT_NOM, ISNULL(p.PRENOM,'') AS PAT_PRENOM,
			ISNULL(d.NOM,'') AS DOC_NOM, ISNULL(d.PRENOM,'') AS DOC_PRENOM
		FROM MOTCONSU m
		LEFT JOIN PATIENTS p ON p.PATIENTS_ID = m.PATIENTS_ID
		LEFT JOIN MEDECINS d ON d.MEDECINS_ID = m.MEDECINS_ID
		WHERE m.KRN_MODIFY_DATE > @lastDate
		ORDER BY m.KRN_MODIFY_DATE ASC`

	rows, err := r.db.QueryContext(ctx, query, sql.Named("lastDate", last))
	if err != nil {
		return nil, fmt.Errorf("query poll: %w", err)
	}
	defer rows.Close()

	var out []PollRow
	for rows.Next() {
		var pr PollRow
		if err := rows.Scan(&pr.MotconsuID, &pr.PatientID, &pr.DoctorID, &pr.DateConsult, &pr.ModifyDate, &pr.PatientNom, &pr.PatientPrenom, &pr.DoctorNom, &pr.DoctorPrenom); err != nil {
			return nil, fmt.Errorf("scan poll: %w", err)
		}
		out = append(out, pr)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows poll: %w", err)
	}
	return out, nil
}

func (r *MotconsuRepo) SetPlanningAndStatus(tx *sql.Tx, ctx context.Context, motconsuID int, planningID int, status string) error {
	_, err := tx.ExecContext(ctx, `
		UPDATE MOTCONSU SET PLANNING_ID = @planID, REC_STATUS = @st
		WHERE MOTCONSU_ID = @motID`,
		sql.Named("planID", planningID),
		sql.Named("st", status),
		sql.Named("motID", motconsuID),
	)
	return err
}
