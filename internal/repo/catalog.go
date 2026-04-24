package repo

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

type CatalogRepo struct {
	db *sql.DB
}

func (r *CatalogRepo) Specialties(ctx context.Context) ([]Specialty, error) {
	rows, err := r.db.QueryContext(ctx, `
SELECT FM_DEP_PROF_ID, ISNULL(CODE,''), ISNULL(LABEL,'')
FROM FM_DEP_PROF
WHERE ISNULL(ARCHIVE,0)=0
ORDER BY LABEL`)
	if err != nil {
		return nil, fmt.Errorf("query specialties: %w", err)
	}
	defer rows.Close()

	var out []Specialty
	for rows.Next() {
		var s Specialty
		if err := rows.Scan(&s.SpecialtyID, &s.Code, &s.Label); err != nil {
			return nil, fmt.Errorf("scan specialties: %w", err)
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows specialties: %w", err)
	}
	return out, nil
}

func (r *CatalogRepo) Departments(ctx context.Context) ([]Department, error) {
	rows, err := r.db.QueryContext(ctx, `
SELECT FM_DEP_ID, ISNULL(CODE,''), ISNULL(LABEL,'')
FROM FM_DEP
WHERE ISNULL(ARCHIVE,0)=0
ORDER BY LABEL`)
	if err != nil {
		return nil, fmt.Errorf("query departments: %w", err)
	}
	defer rows.Close()

	var out []Department
	for rows.Next() {
		var d Department
		if err := rows.Scan(&d.DepartmentID, &d.Code, &d.Label); err != nil {
			return nil, fmt.Errorf("scan departments: %w", err)
		}
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows departments: %w", err)
	}
	return out, nil
}

func (r *CatalogRepo) Doctors(ctx context.Context, specialtyID *int, meddepID *int) ([]CatalogDoctor, error) {
	q := `
SELECT DISTINCT
  m.MEDECINS_ID,
  ISNULL(m.NOM,'') + ' ' + ISNULL(m.PRENOM,'') AS FULL_NAME,
  ISNULL(md.FM_DEP_PROF_ID,0) AS FM_DEP_PROF_ID,
  ISNULL(fp.LABEL,'') AS SPEC_LABEL,
  ISNULL(md.MEDDEP_ID,0) AS MEDDEP_ID
FROM MEDECINS m
JOIN MEDDEP md ON md.MEDECINS_ID = m.MEDECINS_ID
LEFT JOIN FM_DEP_PROF fp ON fp.FM_DEP_PROF_ID = md.FM_DEP_PROF_ID
WHERE ISNULL(m.NOM,'') != ''
  AND ISNULL(md.ARCHIVE,0)=0`
	args := []any{}
	if specialtyID != nil {
		q += " AND md.FM_DEP_PROF_ID = @spec"
		args = append(args, sql.Named("spec", *specialtyID))
	}
	if meddepID != nil {
		q += " AND md.MEDDEP_ID = @meddep"
		args = append(args, sql.Named("meddep", *meddepID))
	}
	q += " ORDER BY FULL_NAME"

	rows, err := r.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("query doctors: %w", err)
	}
	defer rows.Close()

	var out []CatalogDoctor
	for rows.Next() {
		var d CatalogDoctor
		if err := rows.Scan(&d.DoctorID, &d.FullName, &d.SpecialtyID, &d.Specialty, &d.MeddepID); err != nil {
			return nil, fmt.Errorf("scan doctors: %w", err)
		}
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows doctors: %w", err)
	}
	return out, nil
}

type Slot struct {
	PlanningID int
	DoctorID   int
	DoctorName string
	DateCons   time.Time
	Heure      int
	Duration   int
}

func (r *CatalogRepo) Slots(ctx context.Context, doctorID int, dateFrom, dateTo time.Time) ([]Slot, error) {
	rows, err := r.db.QueryContext(ctx, `
SELECT p.PLANNING_ID, ps.MEDECINS_ID,
  ISNULL(m.NOM,'') + ' ' + ISNULL(m.PRENOM,'') AS DOC_NAME,
  p.DATE_CONS, p.HEURE, p.DUREE
FROM PLANNING p
JOIN PL_SUBJ ps ON ps.PL_SUBJ_ID = p.PL_SUBJ_ID
LEFT JOIN MEDECINS m ON m.MEDECINS_ID = ps.MEDECINS_ID
WHERE ps.MEDECINS_ID = @doc
  AND p.DATE_CONS >= @from AND p.DATE_CONS < @to
  AND p.PATIENTS_ID IS NULL
  AND p.STATUS = 0
ORDER BY p.DATE_CONS, p.HEURE`,
		sql.Named("doc", doctorID),
		sql.Named("from", dateFrom.Format("2006-01-02")),
		sql.Named("to", dateTo.Format("2006-01-02")),
	)
	if err != nil {
		return nil, fmt.Errorf("query slots: %w", err)
	}
	defer rows.Close()

	var out []Slot
	for rows.Next() {
		var s Slot
		if err := rows.Scan(&s.PlanningID, &s.DoctorID, &s.DoctorName, &s.DateCons, &s.Heure, &s.Duration); err != nil {
			return nil, fmt.Errorf("scan slots: %w", err)
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows slots: %w", err)
	}
	return out, nil
}
