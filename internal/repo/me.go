package repo

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"time"
)

type Profile struct {
	PatientID int64
	FullName  string
	Phone     string
	// BirthDate from Medialog PATIENTS.NE_LE (datetime, «né le»).
	BirthDate *time.Time
	// BirthYear from GOD_ROGDENIQ when NE_LE is missing; omitted in JSON when BirthDate is set.
	BirthYear *int
	Email string
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
	var birthText sql.NullString
	var neLe sql.NullTime
	err := r.db.QueryRowContext(ctx, `
SELECT
  ISNULL(NOM,'') + ' ' + ISNULL(PRENOM,'') AS FULL_NAME,
  ISNULL(MOBIL_TELEFON, ISNULL(TEL, ISNULL(RAB_TEL,''))) AS PHONE,
  NE_LE,
  GOD_ROGDENIQ AS BIRTH_TEXT,
  ISNULL(EMAIL,'') AS EMAIL
FROM PATIENTS
WHERE PATIENTS_ID = @id`,
		sql.Named("id", patientID),
	).Scan(&p.FullName, &p.Phone, &neLe, &birthText, &p.Email)
	if err != nil {
		return Profile{}, err
	}
	if neLe.Valid {
		t := neLe.Time
		if y := t.Year(); y >= 1800 && y <= 2200 {
			tt := time.Date(y, t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
			p.BirthDate = &tt
		}
	}
	if p.BirthDate == nil && birthText.Valid {
		if y, ok := parseBirthYear(birthText.String); ok {
			p.BirthYear = &y
		}
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

// parseBirthYear extracts a calendar year from Medialog varchar (most often "YYYY";
// if a full date is ever stored, uses that date's year).
func parseBirthYear(raw string) (int, bool) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return 0, false
	}
	if len(s) >= 10 {
		head := s[:10]
		for _, layout := range []string{"2006-01-02", "02.01.2006", "02-01-2006", "02/01/2006", "2006.01.02"} {
			if t, err := time.ParseInLocation(layout, head, time.Local); err == nil {
				y := t.Year()
				if y >= 1800 && y <= 2200 {
					return y, true
				}
				return 0, false
			}
		}
	}
	if len(s) == 4 {
		if y, err := strconv.Atoi(s); err == nil && y >= 1800 && y <= 2200 {
			return y, true
		}
	}
	return 0, false
}
