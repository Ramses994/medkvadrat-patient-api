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
	Service          string
	IsWaitlist       bool
	NoSMSNotify      bool
	MessengerNotify  *int // 1 / 0 / nil (no questionnaire)
	ApptVersion      string
	BranchID         int
	BranchCode       string
	DateConsultation time.Time
	Status           int
}

const mssqlReminderBoundLayout = "2006-01-02T15:04:05"
const consultationWallClockLayout = "2006-01-02 15:04"

func dueRemindersBaseSQL(waitlistIDs []int64) (string, []any) {
	examSQL, examArgs := examSelectSQL(waitlistIDs)
	q := `
SELECT
  p.PLANNING_ID,
  p.PATIENTS_ID,
  REPLACE(REPLACE(REPLACE(REPLACE(ISNULL(pt.MOBIL_TELEFON, ISNULL(pt.TEL, ISNULL(pt.RAB_TEL,''))),'+',''),'-',''),' ',''),'(','') AS PHONE,
  ISNULL(pt.NOM,'') + ' ' + ISNULL(pt.PRENOM,'') AS FULL_NAME,
` + doctorNameSelectSQL + ` AS DOCTOR_NAME,
` + examSQL + `,
  ISNULL(ex.NO_SMS_NOTIFY, 0) AS NO_SMS_NOTIFY,
  r.MESSENGER_NOTIFY AS MESSENGER_NOTIFY,
  ISNULL(p.PL_SUBJ_ID, 0) AS PL_SUBJ_ID,
  ISNULL(p.PL_EXAM_ID, 0) AS PL_EXAM_ID,
  CONVERT(varchar(19), p.DATE_START, 120) AS DATE_START_FULL,
  ISNULL(o.FM_ORG_ID,0) AS BRANCH_ID,
  ISNULL(o.CODE,'')     AS BRANCH_CODE,
  CONVERT(varchar(16), DATEADD(MINUTE,(p.HEURE/100)*60+(p.HEURE%100), p.DATE_CONS), 120) AS DATE_CONSULTATION,
  ISNULL(p.STATUS,-1) AS STATUS
FROM PLANNING p
JOIN PATIENTS pt ON pt.PATIENTS_ID = p.PATIENTS_ID
JOIN PL_SUBJ ps ON ps.PL_SUBJ_ID = p.PL_SUBJ_ID
LEFT JOIN PL_EXAM ex ON ex.PL_EXAM_ID = p.PL_EXAM_ID
LEFT JOIN DATA_W3_RASSYLKA r ON r.PATIENTS_ID = p.PATIENTS_ID
LEFT JOIN FM_ORG o ON o.FM_ORG_ID = ps.FM_INTORG_ID
WHERE p.PATIENTS_ID IS NOT NULL
  AND ISNULL(p.CANCELLED,0) = 0
  AND p.DATE_CONS >= CAST(@from AS date)
  AND p.DATE_CONS <= CAST(@to AS date)
  AND DATEADD(MINUTE,(p.HEURE/100)*60+(p.HEURE%100), p.DATE_CONS) >= @from
  AND DATEADD(MINUTE,(p.HEURE/100)*60+(p.HEURE%100), p.DATE_CONS) <= @to`
	return q, examArgs
}

// ParseConsultationWallClock parses MSSQL CONVERT(varchar(16), …, 120) as MSK wall-clock.
func ParseConsultationWallClock(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, fmt.Errorf("empty consultation time")
	}
	if len(s) > len(consultationWallClockLayout) {
		s = s[:len(consultationWallClockLayout)]
	}
	t, err := time.ParseInLocation(consultationWallClockLayout, s, moscowLocation())
	if err != nil {
		return time.Time{}, fmt.Errorf("parse consultation time %q: %w", s, err)
	}
	return t, nil
}

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

func DueReminders(ctx context.Context, db *sql.DB, from, to time.Time, patientIDs, waitlistIDs []int64) ([]DueReminder, error) {
	loc := moscowLocation()
	fromBound := from.In(loc).Format(mssqlReminderBoundLayout)
	toBound := to.In(loc).Format(mssqlReminderBoundLayout)

	baseSQL, examArgs := dueRemindersBaseSQL(waitlistIDs)
	filterSQL, filterArgs := patientFilterClause(patientIDs)
	query := baseSQL + filterSQL + `
ORDER BY DATEADD(MINUTE,(p.HEURE/100)*60+(p.HEURE%100), p.DATE_CONS)`

	args := []any{
		sql.Named("from", fromBound),
		sql.Named("to", toBound),
	}
	args = append(args, examArgs...)
	args = append(args, filterArgs...)

	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query due reminders: %w", err)
	}
	defer rows.Close()

	var out []DueReminder
	for rows.Next() {
		var r DueReminder
		var dateConsStr, webName, examName, dateStart string
		var isWaitlist, noSMS int
		var notify sql.NullInt64
		var plSubj, plExam int64
		if err := rows.Scan(
			&r.PlanningID,
			&r.PatientID,
			&r.PatientPhone,
			&r.PatientName,
			&r.DoctorName,
			&webName,
			&examName,
			&isWaitlist,
			&noSMS,
			&notify,
			&plSubj,
			&plExam,
			&dateStart,
			&r.BranchID,
			&r.BranchCode,
			&dateConsStr,
			&r.Status,
		); err != nil {
			return nil, fmt.Errorf("scan due reminders: %w", err)
		}
		r.DateConsultation, err = ParseConsultationWallClock(dateConsStr)
		if err != nil {
			return nil, fmt.Errorf("scan due reminders: %w", err)
		}
		fillReminderDerived(&r, webName, examName, isWaitlist, noSMS, notify, plSubj, plExam, dateStart)
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows due reminders: %w", err)
	}
	return out, nil
}

func fillReminderDerived(r *DueReminder, webName, examName string, isWaitlist, noSMS int, notify sql.NullInt64, plSubj, plExam int64, dateStart string) {
	r.DoctorName = trimDoctorName(r.DoctorName)
	r.Service = DisplayService(webName, examName)
	r.IsWaitlist = isWaitlist != 0
	r.NoSMSNotify = noSMS != 0
	if notify.Valid {
		v := int(notify.Int64)
		if v != 0 {
			v = 1
		}
		r.MessengerNotify = &v
	}
	r.ApptVersion = ApptVersion(dateStart, plSubj, plExam)
}

func moscowLocation() *time.Location {
	loc, err := time.LoadLocation("Europe/Moscow")
	if err != nil {
		return time.FixedZone("MSK", 3*3600)
	}
	return loc
}
