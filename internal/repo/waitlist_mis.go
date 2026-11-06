package repo

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// WaitlistMisRow is one active registry waitlist placeholder from Medialog (read-only).
type WaitlistMisRow struct {
	PlanningID  int64
	PatientID   int64
	PatientName string
	Phone       string
	BirthDate   string
	DateStart   string
	BranchID    int
	DoctorName  string
	Comment     string
	CreatorName string
	CreatedAt   string
}

// WaitlistMisWindow is [today-back, today+ahead] in Moscow, as CONVERT(...,126) strings.
// The end is exclusive: the morning after the last included day.
func WaitlistMisWindow(now time.Time, back, ahead int) (from, to string) {
	if back < 0 {
		back = 0
	}
	if ahead < 0 {
		ahead = 0
	}
	loc := moscowLocation()
	n := now.In(loc)
	start := time.Date(n.Year(), n.Month(), n.Day(), 0, 0, 0, 0, loc)
	from = start.AddDate(0, 0, -back).Format(mssqlReminderBoundLayout)
	to = start.AddDate(0, 0, ahead+1).Format(mssqlReminderBoundLayout)
	return from, to
}

func waitlistMisSQL(examIDs []int64) (string, []any) {
	ids := NormalizeWaitlistExamIDs(examIDs)
	var in strings.Builder
	args := make([]any, 0, len(ids))
	for i, id := range ids {
		if i > 0 {
			in.WriteString(",")
		}
		name := fmt.Sprintf("misex%d", i)
		in.WriteString("@" + name)
		args = append(args, sql.Named(name, id))
	}
	q := `
SELECT
  p.PLANNING_ID,
  p.PATIENTS_ID,
  ISNULL(pt.NOM,'') + ' ' + ISNULL(pt.PRENOM,'') AS PATIENT_NAME,
  ISNULL(pt.MOBIL_TELEFON, ISNULL(pt.TEL, ISNULL(pt.RAB_TEL,''))) AS PHONE,
  ISNULL(CONVERT(varchar(10), pt.NE_LE, 23), '') AS DOB,
  CONVERT(varchar(19), p.DATE_START, 126) AS DATE_START,
  ISNULL(ps.FM_INTORG_ID, 0) AS BRANCH_ID,
` + doctorNameSelectSQL + ` AS DOCTOR_NAME,
  SUBSTRING(ISNULL(p.COMMENTAIRE, ''), 1, 200) AS COMMENTAIRE,
  ISNULL((
    SELECT ISNULL(cr.NOM, '') + ' ' + ISNULL(cr.PRENOM, '')
    FROM MEDECINS cr
    WHERE cr.MEDECINS_ID = p.MEDECINS_CREATOR_ID
  ), '') AS CREATOR_NAME,
  CONVERT(varchar(19), p.CREATE_DATE_TIME, 126) AS CREATED_AT
FROM PLANNING p
JOIN PATIENTS pt ON pt.PATIENTS_ID = p.PATIENTS_ID
LEFT JOIN PL_SUBJ ps ON ps.PL_SUBJ_ID = p.PL_SUBJ_ID
WHERE ISNULL(p.CANCELLED, 0) = 0
  AND ISNULL(p.STATUS, 0) = 0
  AND p.PL_EXAM_ID IN (` + in.String() + `)
  AND p.DATE_START >= CONVERT(datetime, @misFrom, 126)
  AND p.DATE_START < CONVERT(datetime, @misTo, 126)
  AND ps.FM_INTORG_ID IN (106, 3, 496)
ORDER BY p.DATE_START, p.PLANNING_ID`
	return q, args
}

// ListWaitlistMis reads registry waitlist placeholders. It does not write to Medialog.
func ListWaitlistMis(ctx context.Context, db *sql.DB, now time.Time, back, ahead int, examIDs []int64) ([]WaitlistMisRow, error) {
	q, args := waitlistMisSQL(examIDs)
	from, to := WaitlistMisWindow(now, back, ahead)
	args = append(args, sql.Named("misFrom", from), sql.Named("misTo", to))
	rs, err := db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("waitlist mis: %w", err)
	}
	defer rs.Close()
	var out []WaitlistMisRow
	for rs.Next() {
		var row WaitlistMisRow
		var name, phone, dob, dateStart, doctor, comment, creator, created sql.NullString
		if err := rs.Scan(
			&row.PlanningID, &row.PatientID, &name, &phone, &dob, &dateStart,
			&row.BranchID, &doctor, &comment, &creator, &created,
		); err != nil {
			return nil, err
		}
		row.PatientName = strings.TrimSpace(name.String)
		row.Phone = phone.String
		row.BirthDate = dob.String
		row.DateStart = dateStart.String
		row.DoctorName = trimDoctorName(doctor.String)
		row.Comment = strings.TrimSpace(comment.String)
		row.CreatorName = trimDoctorName(creator.String)
		row.CreatedAt = created.String
		out = append(out, row)
	}
	if out == nil {
		out = []WaitlistMisRow{}
	}
	return out, rs.Err()
}

// WaitlistMisBranch returns the branch of an active registry placeholder.
// ok is false when the row is missing, cancelled, or not a waitlist exam.
func WaitlistMisBranch(ctx context.Context, db *sql.DB, planningID int64, examIDs []int64) (int, bool, error) {
	ids := NormalizeWaitlistExamIDs(examIDs)
	var in strings.Builder
	args := make([]any, 0, len(ids)+1)
	args = append(args, sql.Named("misID", planningID))
	for i, id := range ids {
		if i > 0 {
			in.WriteString(",")
		}
		name := fmt.Sprintf("misbr%d", i)
		in.WriteString("@" + name)
		args = append(args, sql.Named(name, id))
	}
	q := `
SELECT ISNULL(ps.FM_INTORG_ID, 0)
FROM PLANNING p
LEFT JOIN PL_SUBJ ps ON ps.PL_SUBJ_ID = p.PL_SUBJ_ID
WHERE p.PLANNING_ID = @misID
  AND ISNULL(p.CANCELLED, 0) = 0
  AND ISNULL(p.STATUS, 0) = 0
  AND p.PL_EXAM_ID IN (` + in.String() + `)`
	var branch int
	err := db.QueryRowContext(ctx, q, args...).Scan(&branch)
	if err == sql.ErrNoRows {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return branch, true, nil
}
