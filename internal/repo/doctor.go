package repo

import (
	"context"
	"database/sql"
	"fmt"
)

type DoctorRepo struct {
	db *sql.DB
}

func (r *DoctorRepo) List(ctx context.Context) ([]Doctor, error) {
	query := `
		SELECT MEDECINS_ID,
			ISNULL(NOM,'') + ' ' + ISNULL(PRENOM,'') AS FULL_NAME,
			'' AS SPECIALTY
		FROM MEDECINS
		WHERE ISNULL(NOM,'') != ''
		ORDER BY NOM`

	rows, err := r.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("query doctors: %w", err)
	}
	defer rows.Close()

	var doctors []Doctor
	for rows.Next() {
		var d Doctor
		if err := rows.Scan(&d.DoctorID, &d.FullName, &d.Specialty); err != nil {
			return nil, fmt.Errorf("scan doctors: %w", err)
		}
		doctors = append(doctors, d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows doctors: %w", err)
	}
	return doctors, nil
}
