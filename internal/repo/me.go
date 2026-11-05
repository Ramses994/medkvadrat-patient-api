package repo

import (
	"context"
	"database/sql"
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
	Email     string
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
