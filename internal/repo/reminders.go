package repo

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

type DueReminder struct {
	MotconsuID       int64
	PatientID        int64
	PatientPhone     string
	PatientName      string
	DoctorName       string
	DepartmentID     int
	DepartmentLabel  string
	DateConsultation time.Time
}

func DueReminders(ctx context.Context, db *sql.DB, from, to time.Time) ([]DueReminder, error) {
	rows, err := db.QueryContext(ctx, `
SELECT
  m.MOTCONSU_ID,
  m.PATIENTS_ID,
  ISNULL(p.MOBIL_TELEFON, ISNULL(p.TEL, ISNULL(p.RAB_TEL,''))) AS PHONE,
  ISNULL(p.NOM,'') + ' ' + ISNULL(p.PRENOM,'') AS PAT_NAME,
  ISNULL(d.NOM,'') + ' ' + ISNULL(d.PRENOM,'') AS DOC_NAME,
  ISNULL(fd.FM_DEP_ID, 0) AS DEP_ID,
  ISNULL(fd.LABEL,'') AS DEP_LABEL,
  m.DATE_CONSULTATION
FROM MOTCONSU m
JOIN PATIENTS p ON p.PATIENTS_ID = m.PATIENTS_ID
LEFT JOIN MEDECINS d ON d.MEDECINS_ID = m.MEDECINS_ID
LEFT JOIN PLANNING pl ON pl.PLANNING_ID = m.PLANNING_ID
LEFT JOIN PL_SUBJ ps ON ps.PL_SUBJ_ID = pl.PL_SUBJ_ID
LEFT JOIN MEDDEP md ON md.MEDDEP_ID = ps.MEDDEP_ID
LEFT JOIN FM_DEP fd ON fd.FM_DEP_ID = md.FM_DEP_ID
WHERE m.REC_STATUS = 'W'
  AND m.PLANNING_ID IS NOT NULL
  AND m.DATE_CONSULTATION >= @from
  AND m.DATE_CONSULTATION <= @to
ORDER BY m.DATE_CONSULTATION`,
		sql.Named("from", from),
		sql.Named("to", to),
	)
	if err != nil {
		return nil, fmt.Errorf("query due reminders: %w", err)
	}
	defer rows.Close()

	var out []DueReminder
	for rows.Next() {
		var r DueReminder
		var phone string
		if err := rows.Scan(
			&r.MotconsuID,
			&r.PatientID,
			&phone,
			&r.PatientName,
			&r.DoctorName,
			&r.DepartmentID,
			&r.DepartmentLabel,
			&r.DateConsultation,
		); err != nil {
			return nil, fmt.Errorf("scan due reminders: %w", err)
		}
		r.PatientPhone = CleanPhoneDigits(phone)
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows due reminders: %w", err)
	}
	return out, nil
}
