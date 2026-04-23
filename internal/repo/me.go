package repo

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

type Profile struct {
	PatientID int64
	FullName  string
	Phone     string
	BirthDate *time.Time
	Email     string
}

type Appointment struct {
	MotconsuID         int64
	DoctorID           int64
	DoctorName         string
	DateConsultation   time.Time
	RecStatus          string
	PlanningID         *int64
	PlanningPatientsID *int64
}

type MeRepo struct {
	db *sql.DB
}

func (r *MeRepo) Profile(ctx context.Context, patientID int64) (Profile, error) {
	var p Profile
	p.PatientID = patientID
	var bd sql.NullTime
	err := r.db.QueryRowContext(ctx, `
SELECT
  ISNULL(NOM,'') + ' ' + ISNULL(PRENOM,'') AS FULL_NAME,
  ISNULL(MOBIL_TELEFON, ISNULL(TEL, ISNULL(RAB_TEL,''))) AS PHONE,
  CAST(NULL AS datetime) AS BIRTH_DATE,
  ISNULL(EMAIL,'') AS EMAIL
FROM PATIENTS
WHERE PATIENTS_ID = @id`,
		sql.Named("id", patientID),
	).Scan(&p.FullName, &p.Phone, &bd, &p.Email)
	if err != nil {
		return Profile{}, err
	}
	if bd.Valid {
		t := bd.Time
		p.BirthDate = &t
	}
	return p, nil
}

func (r *MeRepo) Appointments(ctx context.Context, patientID int64, now time.Time, kind string) ([]Appointment, error) {
	// kind: upcoming|past
	where := ""
	switch kind {
	case "", "upcoming":
		where = "m.DATE_CONSULTATION >= @now AND m.REC_STATUS = 'W'"
	case "past":
		where = "(m.DATE_CONSULTATION < @now OR m.REC_STATUS = 'A')"
	default:
		return nil, fmt.Errorf("invalid kind")
	}
	rows, err := r.db.QueryContext(ctx, `
SELECT
  m.MOTCONSU_ID,
  m.MEDECINS_ID,
  ISNULL(d.NOM,'') + ' ' + ISNULL(d.PRENOM,'') AS DOC_NAME,
  m.DATE_CONSULTATION,
  ISNULL(m.REC_STATUS,'') AS REC_STATUS,
  m.PLANNING_ID,
  p.PATIENTS_ID
FROM MOTCONSU m
LEFT JOIN MEDECINS d ON d.MEDECINS_ID = m.MEDECINS_ID
LEFT JOIN PLANNING p ON p.PLANNING_ID = m.PLANNING_ID
WHERE m.PATIENTS_ID = @pid
  AND `+where+`
ORDER BY m.DATE_CONSULTATION`,
		sql.Named("pid", patientID),
		sql.Named("now", now),
	)
	if err != nil {
		return nil, fmt.Errorf("query appointments: %w", err)
	}
	defer rows.Close()

	var out []Appointment
	for rows.Next() {
		var a Appointment
		var planningID sql.NullInt64
		var planningPat sql.NullInt64
		if err := rows.Scan(&a.MotconsuID, &a.DoctorID, &a.DoctorName, &a.DateConsultation, &a.RecStatus, &planningID, &planningPat); err != nil {
			return nil, fmt.Errorf("scan appointments: %w", err)
		}
		if planningID.Valid {
			v := planningID.Int64
			a.PlanningID = &v
		}
		if planningPat.Valid {
			v := planningPat.Int64
			a.PlanningPatientsID = &v
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows appointments: %w", err)
	}
	return out, nil
}
