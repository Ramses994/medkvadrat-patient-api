package repo

import (
	"context"
	"database/sql"
	"fmt"
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
