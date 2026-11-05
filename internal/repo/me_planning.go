package repo

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// PlanningVisit is one PLANNING row for the patient cabinet (read-only).
type PlanningVisit struct {
	PlanningID   int64
	StartsAt     string
	DurationMin  int
	Cancelled    int
	Arrived      int
	BranchID     int
	BranchCode   string
	MedecinsID   sql.NullInt64
	DoctorName   string
	ExamName     string
	Confirmation *string
}

func planningVisitSelect() string {
	return `
SELECT TOP (%d)
  p.PLANNING_ID,
  CONVERT(varchar(16), p.DATE_START, 120) AS STARTS_AT,
  ISNULL(p.DUREE, 0) AS DURATION_MIN,
  CAST(ISNULL(p.CANCELLED, 0) AS int) AS CANCELLED,
  CAST(ISNULL(p.PATIENT_ARRIVEE, 0) AS int) AS ARRIVED,
  ISNULL(o.FM_ORG_ID, 0) AS BRANCH_ID,
  ISNULL(o.CODE, '') AS BRANCH_CODE,
  ps.MEDECINS_ID,
  LTRIM(RTRIM(ISNULL(m.NOM,'') + ' ' + ISNULL(m.PRENOM,'') + ' ' + ISNULL(m.PATRONYME,''))) AS DOCTOR_NAME,
  ISNULL(ex.NAME, '') AS EXAM_NAME
FROM PLANNING p
LEFT JOIN PL_SUBJ ps ON ps.PL_SUBJ_ID = p.PL_SUBJ_ID
LEFT JOIN FM_ORG o ON o.FM_ORG_ID = ps.FM_INTORG_ID
LEFT JOIN MEDECINS m ON m.MEDECINS_ID = ps.MEDECINS_ID
LEFT JOIN PL_EXAM ex ON ex.PL_EXAM_ID = p.PL_EXAM_ID
`
}

// PlanningAppointments lists upcoming or past PLANNING rows for one patient.
func (r *MeRepo) PlanningAppointments(ctx context.Context, patientID int64, now time.Time, kind string) ([]PlanningVisit, error) {
	nowBound := now.In(moscowLocation()).Format(mssqlReminderBoundLayout)
	var window, order string
	switch kind {
	case "", "upcoming":
		window = `p.DATE_START >= CONVERT(datetime, @now, 126) AND p.DATE_START < DATEADD(day, 180, CONVERT(datetime, @now, 126))`
		order = `ORDER BY p.DATE_START`
	case "past":
		window = `p.DATE_START < CONVERT(datetime, @now, 126) AND p.DATE_START >= DATEADD(year, -3, CONVERT(datetime, @now, 126))`
		order = `ORDER BY p.DATE_START DESC`
	default:
		return nil, fmt.Errorf("invalid kind")
	}
	q := fmt.Sprintf(planningVisitSelect(), 200) + `
WHERE p.PATIENTS_ID = @pid
  AND ISNULL(p.STATUS, 0) = 0
  AND ` + window + `
` + order
	return scanPlanningVisits(ctx, r.db, q, sql.Named("pid", patientID), sql.Named("now", nowBound))
}

// PlanningVisits lists arrivals (PATIENT_ARRIVEE=1) in a DATE_CONS window, newest first.
func (r *MeRepo) PlanningVisits(ctx context.Context, patientID int64, from, to time.Time) ([]PlanningVisit, error) {
	q := fmt.Sprintf(planningVisitSelect(), 500) + `
WHERE p.PATIENTS_ID = @pid
  AND ISNULL(p.STATUS, 0) = 0
  AND ISNULL(p.PATIENT_ARRIVEE, 0) = 1
  AND p.DATE_CONS >= CONVERT(date, @from, 23)
  AND p.DATE_CONS <= CONVERT(date, @to, 23)
ORDER BY p.DATE_START DESC`
	return scanPlanningVisits(ctx, r.db, q,
		sql.Named("pid", patientID),
		sql.Named("from", from.Format("2006-01-02")),
		sql.Named("to", to.Format("2006-01-02")),
	)
}

func scanPlanningVisits(ctx context.Context, db *sql.DB, q string, args ...any) ([]PlanningVisit, error) {
	rows, err := db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("query planning visits: %w", err)
	}
	defer rows.Close()
	out := make([]PlanningVisit, 0)
	for rows.Next() {
		var v PlanningVisit
		var cancelled, arrived bitInt
		var doctor, exam sql.NullString
		if err := rows.Scan(
			&v.PlanningID, &v.StartsAt, &v.DurationMin, &cancelled, &arrived,
			&v.BranchID, &v.BranchCode, &v.MedecinsID, &doctor, &exam,
		); err != nil {
			return nil, fmt.Errorf("scan planning visit: %w", err)
		}
		v.Cancelled = int(cancelled)
		v.Arrived = int(arrived)
		v.DoctorName = strings.Join(strings.Fields(doctor.String), " ")
		v.ExamName = strings.TrimSpace(exam.String)
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// PlanningVisitState maps Medialog flags to a patient-facing state.
// warned_absent is "Пациент не придёт", not a no-show verdict.
func PlanningVisitState(cancelled, arrived int, startsAt string, now time.Time) string {
	if cancelled != 0 {
		return "warned_absent"
	}
	if arrived != 0 {
		return "attended"
	}
	nowWall := now.In(moscowLocation()).Format("2006-01-02 15:04")
	if startsAt >= nowWall {
		return "scheduled"
	}
	return "not_marked"
}

// PlanningBranch resolves a human name and address. Unknown ids keep the Medialog code.
func PlanningBranch(id int, code string) (name string, address *string, ok bool) {
	code = strings.TrimSpace(code)
	if id == 0 && code == "" {
		return "", nil, false
	}
	if b, found := BranchByID(id); found {
		addr := b.Address
		return b.Name, &addr, true
	}
	return code, nil, true
}
