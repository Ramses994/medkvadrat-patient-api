package repo

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"time"
)

type DueReminder struct {
	PlanningID       int64
	PatientID        int64
	PatientPhone     string
	PatientName      string
	DoctorName       string
	DepartmentID     int
	DepartmentLabel  string
	DateConsultation time.Time
	Status           int
}

const mssqlLocalDateTimeLayout = "2006-01-02 15:04:05"

const dueRemindersBaseSQL = `
SELECT
  p.PLANNING_ID,
  p.PATIENTS_ID,
  REPLACE(REPLACE(REPLACE(REPLACE(ISNULL(pt.MOBIL_TELEFON, ISNULL(pt.TEL, ISNULL(pt.RAB_TEL,''))),'+',''),'-',''),' ',''),'(','') AS PHONE,
  ISNULL(pt.NOM,'') + ' ' + ISNULL(pt.PRENOM,'') AS FULL_NAME,
  ISNULL(ps.NAME,'') AS DOCTOR_NAME,
  ISNULL(fd.FM_DEP_ID,0) AS DEPARTMENT_ID,
  ISNULL(fd.LABEL,'')    AS DEPARTMENT_LABEL,
  DATEADD(MINUTE,(p.HEURE/100)*60+(p.HEURE%100), p.DATE_CONS) AS DATE_CONSULTATION,
  ISNULL(p.STATUS,-1) AS STATUS
FROM PLANNING p
JOIN PATIENTS pt ON pt.PATIENTS_ID = p.PATIENTS_ID
LEFT JOIN PL_SUBJ ps ON ps.PL_SUBJ_ID = p.PL_SUBJ_ID
LEFT JOIN MEDDEP md  ON md.MEDDEP_ID  = ps.MEDDEP_ID
LEFT JOIN FM_DEP fd  ON fd.FM_DEP_ID  = md.FM_DEP_ID
WHERE p.PATIENTS_ID IS NOT NULL
  AND ISNULL(p.CANCELLED,0) = 0
  AND p.DATE_CONS >= CAST(@from AS date)
  AND p.DATE_CONS <= CAST(@to AS date)
  AND DATEADD(MINUTE,(p.HEURE/100)*60+(p.HEURE%100), p.DATE_CONS) >= @from
  AND DATEADD(MINUTE,(p.HEURE/100)*60+(p.HEURE%100), p.DATE_CONS) <= @to`

// ParsePatientIDsCSV parses optional comma-separated patient ids (empty → nil, no filter).
func ParsePatientIDsCSV(raw string) ([]int64, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	parts := strings.Split(raw, ",")
	out := make([]int64, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		id, err := strconv.ParseInt(part, 10, 64)
		if err != nil || id <= 0 {
			return nil, fmt.Errorf("invalid patient_ids")
		}
		out = append(out, id)
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

func patientFilterClause(patientIDs []int64) (string, []any) {
	if len(patientIDs) == 0 {
		return " AND (@hasPatients = 0 OR 1=1)", []any{sql.Named("hasPatients", 0)}
	}
	var b strings.Builder
	b.WriteString(" AND (@hasPatients = 0 OR p.PATIENTS_ID IN (")
	args := []any{sql.Named("hasPatients", 1)}
	for i, id := range patientIDs {
		if i > 0 {
			b.WriteString(",")
		}
		name := fmt.Sprintf("pid%d", i)
		b.WriteString("@" + name)
		args = append(args, sql.Named(name, id))
	}
	b.WriteString("))")
	return b.String(), args
}

func DueReminders(ctx context.Context, db *sql.DB, from, to time.Time, patientIDs []int64) ([]DueReminder, error) {
	loc := moscowLocation()
	fromBound := from.In(loc).Format(mssqlLocalDateTimeLayout)
	toBound := to.In(loc).Format(mssqlLocalDateTimeLayout)

	filterSQL, filterArgs := patientFilterClause(patientIDs)
	query := dueRemindersBaseSQL + filterSQL + "\nORDER BY DATE_CONSULTATION"

	args := []any{
		sql.Named("from", fromBound),
		sql.Named("to", toBound),
	}
	args = append(args, filterArgs...)

	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query due reminders: %w", err)
	}
	defer rows.Close()

	var out []DueReminder
	for rows.Next() {
		var r DueReminder
		if err := rows.Scan(
			&r.PlanningID,
			&r.PatientID,
			&r.PatientPhone,
			&r.PatientName,
			&r.DoctorName,
			&r.DepartmentID,
			&r.DepartmentLabel,
			&r.DateConsultation,
			&r.Status,
		); err != nil {
			return nil, fmt.Errorf("scan due reminders: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows due reminders: %w", err)
	}
	return out, nil
}

func moscowLocation() *time.Location {
	loc, err := time.LoadLocation("Europe/Moscow")
	if err != nil {
		return time.FixedZone("MSK", 3*3600)
	}
	return loc
}
