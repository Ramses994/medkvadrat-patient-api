package repo

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

type NewBooking struct {
	DueReminder
	CreatedAt string // CONVERT(varchar(23), CREATE_DATE_TIME, 121)
}

type BookingCursor struct {
	CreatedAt  string `json:"created_at"`
	PlanningID int64  `json:"planning_id"`
}

type NewBookingsPage struct {
	Rows       []NewBooking
	NextCursor *BookingCursor
	HasMore    bool
}

const maxNewBookingPatients = 500
const maxNewBookingRows = 200

func NormalizeNewBookingLimit(n int) int {
	if n <= 0 || n > maxNewBookingRows {
		return maxNewBookingRows
	}
	return n
}

func RequirePatientIDList(ids []int64, maxN int) ([]int64, error) {
	if len(ids) == 0 {
		return nil, fmt.Errorf("invalid patient_ids")
	}
	if maxN > 0 && len(ids) > maxN {
		return nil, fmt.Errorf("too many patient_ids")
	}
	for _, id := range ids {
		if id <= 0 {
			return nil, fmt.Errorf("invalid patient_ids")
		}
	}
	return ids, nil
}

func NewBookings(ctx context.Context, db *sql.DB, now time.Time, patientIDs []int64, cursor *BookingCursor, limit int, waitlistIDs []int64) (NewBookingsPage, error) {
	ids, err := RequirePatientIDList(patientIDs, maxNewBookingPatients)
	if err != nil {
		return NewBookingsPage{}, err
	}
	limit = NormalizeNewBookingLimit(limit)
	loc := moscowLocation()
	nowBound := now.In(loc).Format(mssqlReminderBoundLayout)

	ts := "1900-01-01 00:00:00.000"
	var pid int64
	if cursor != nil && strings.TrimSpace(cursor.CreatedAt) != "" {
		ts = strings.TrimSpace(cursor.CreatedAt)
		pid = cursor.PlanningID
	}

	var in strings.Builder
	args := []any{
		sql.Named("now", nowBound),
		sql.Named("ts", ts),
		sql.Named("pid", pid),
	}
	for i, id := range ids {
		if i > 0 {
			in.WriteString(",")
		}
		name := fmt.Sprintf("pid%d", i)
		in.WriteString("@" + name)
		args = append(args, sql.Named(name, id))
	}
	examSQL, examArgs := examSelectSQL(waitlistIDs)
	args = append(args, examArgs...)

	query := fmt.Sprintf(`
SELECT TOP %d
  p.PLANNING_ID,
  p.PATIENTS_ID,
  REPLACE(REPLACE(REPLACE(REPLACE(ISNULL(pt.MOBIL_TELEFON, ISNULL(pt.TEL, ISNULL(pt.RAB_TEL,''))),'+',''),'-',''),' ',''),'(','') AS PHONE,
  ISNULL(pt.NOM,'') + ' ' + ISNULL(pt.PRENOM,'') AS FULL_NAME,
%s AS DOCTOR_NAME,
%s,
  CAST(ISNULL(ex.NO_SMS_NOTIFY, 0) AS int) AS NO_SMS_NOTIFY,
  CAST(r.MESSENGER_NOTIFY AS int) AS MESSENGER_NOTIFY,
  ISNULL(p.PL_SUBJ_ID, 0) AS PL_SUBJ_ID,
  ISNULL(p.PL_EXAM_ID, 0) AS PL_EXAM_ID,
  CONVERT(varchar(19), p.DATE_START, 120) AS DATE_START_FULL,
  ISNULL(o.FM_ORG_ID,0) AS BRANCH_ID,
  ISNULL(o.CODE,'') AS BRANCH_CODE,
  CONVERT(varchar(16), p.DATE_START, 120) AS DATE_CONSULTATION,
  CAST(ISNULL(p.STATUS,0) AS int) AS STATUS,
  CONVERT(varchar(23), p.CREATE_DATE_TIME, 121) AS CREATED_AT
FROM PLANNING p
JOIN PATIENTS pt ON pt.PATIENTS_ID = p.PATIENTS_ID
JOIN PL_SUBJ ps ON ps.PL_SUBJ_ID = p.PL_SUBJ_ID
LEFT JOIN PL_EXAM ex ON ex.PL_EXAM_ID = p.PL_EXAM_ID
LEFT JOIN DATA_W3_RASSYLKA r ON r.PATIENTS_ID = p.PATIENTS_ID
LEFT JOIN FM_ORG o ON o.FM_ORG_ID = ps.FM_INTORG_ID
WHERE p.PATIENTS_ID IN (%s)
  AND ISNULL(p.CANCELLED,0) = 0
  AND ISNULL(p.STATUS,0) = 0
  AND p.DATE_START > @now
  AND (
    p.CREATE_DATE_TIME > CONVERT(datetime, @ts, 121)
    OR (p.CREATE_DATE_TIME = CONVERT(datetime, @ts, 121) AND p.PLANNING_ID > @pid)
  )
ORDER BY p.CREATE_DATE_TIME, p.PLANNING_ID`, limit+1, doctorNameSelectSQL, examSQL, in.String())

	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return NewBookingsPage{}, fmt.Errorf("query new bookings: %w", err)
	}
	defer rows.Close()

	var out []NewBooking
	for rows.Next() {
		var r NewBooking
		var dateConsStr, webName, examName, dateStart string
		var isWaitlist, noSMS, status bitInt
		var notify nullBitInt
		var plSubj, plExam int64
		if err := rows.Scan(
			&r.PlanningID, &r.PatientID, &r.PatientPhone, &r.PatientName, &r.DoctorName,
			&webName, &examName, &isWaitlist, &noSMS, &notify, &plSubj, &plExam, &dateStart,
			&r.BranchID, &r.BranchCode, &dateConsStr, &status, &r.CreatedAt,
		); err != nil {
			return NewBookingsPage{}, fmt.Errorf("scan new bookings: %w", err)
		}
		r.DateConsultation, err = ParseConsultationWallClock(dateConsStr)
		if err != nil {
			return NewBookingsPage{}, err
		}
		r.Status = int(status)
		fillReminderDerived(&r.DueReminder, webName, examName, int(isWaitlist), int(noSMS), notify, plSubj, plExam, dateStart)
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return NewBookingsPage{}, fmt.Errorf("rows new bookings: %w", err)
	}

	page := NewBookingsPage{HasMore: len(out) > limit}
	if page.HasMore {
		out = out[:limit]
	}
	page.Rows = out
	if len(out) > 0 {
		last := out[len(out)-1]
		page.NextCursor = &BookingCursor{CreatedAt: last.CreatedAt, PlanningID: last.PlanningID}
	}
	return page, nil
}

type BookingNotice struct {
	DueReminder
	CreatedAt string
	Cancelled bool
}

func BookingByPlanningID(ctx context.Context, db *sql.DB, planningID int64, waitlistIDs []int64) (BookingNotice, error) {
	if planningID <= 0 {
		return BookingNotice{}, fmt.Errorf("invalid planning_id")
	}
	examSQL, examArgs := examSelectSQL(waitlistIDs)
	query := fmt.Sprintf(`
SELECT
  p.PLANNING_ID,
  p.PATIENTS_ID,
  REPLACE(REPLACE(REPLACE(REPLACE(ISNULL(pt.MOBIL_TELEFON, ISNULL(pt.TEL, ISNULL(pt.RAB_TEL,''))),'+',''),'-',''),' ',''),'(','') AS PHONE,
  ISNULL(pt.NOM,'') + ' ' + ISNULL(pt.PRENOM,'') AS FULL_NAME,
%s AS DOCTOR_NAME,
%s,
  CAST(ISNULL(ex.NO_SMS_NOTIFY, 0) AS int) AS NO_SMS_NOTIFY,
  CAST(r.MESSENGER_NOTIFY AS int) AS MESSENGER_NOTIFY,
  ISNULL(p.PL_SUBJ_ID, 0) AS PL_SUBJ_ID,
  ISNULL(p.PL_EXAM_ID, 0) AS PL_EXAM_ID,
  CONVERT(varchar(19), p.DATE_START, 120) AS DATE_START_FULL,
  ISNULL(o.FM_ORG_ID,0) AS BRANCH_ID,
  ISNULL(o.CODE,'') AS BRANCH_CODE,
  CONVERT(varchar(16), p.DATE_START, 120) AS DATE_CONSULTATION,
  CAST(ISNULL(p.STATUS,0) AS int) AS STATUS,
  CONVERT(varchar(23), p.CREATE_DATE_TIME, 121) AS CREATED_AT,
  CAST(ISNULL(p.CANCELLED,0) AS int) AS CANCELLED
FROM PLANNING p
JOIN PATIENTS pt ON pt.PATIENTS_ID = p.PATIENTS_ID
JOIN PL_SUBJ ps ON ps.PL_SUBJ_ID = p.PL_SUBJ_ID
LEFT JOIN PL_EXAM ex ON ex.PL_EXAM_ID = p.PL_EXAM_ID
LEFT JOIN DATA_W3_RASSYLKA r ON r.PATIENTS_ID = p.PATIENTS_ID
LEFT JOIN FM_ORG o ON o.FM_ORG_ID = ps.FM_INTORG_ID
WHERE p.PLANNING_ID = @id`, doctorNameSelectSQL, examSQL)

	args := append([]any{sql.Named("id", planningID)}, examArgs...)
	row := db.QueryRowContext(ctx, query, args...)
	var r BookingNotice
	var dateConsStr, webName, examName, dateStart string
	var isWaitlist, noSMS, status, cancelled bitInt
	var notify nullBitInt
	var plSubj, plExam int64
	err := row.Scan(
		&r.PlanningID, &r.PatientID, &r.PatientPhone, &r.PatientName, &r.DoctorName,
		&webName, &examName, &isWaitlist, &noSMS, &notify, &plSubj, &plExam, &dateStart,
		&r.BranchID, &r.BranchCode, &dateConsStr, &status, &r.CreatedAt, &cancelled,
	)
	if err == sql.ErrNoRows {
		return BookingNotice{}, err
	}
	if err != nil {
		return BookingNotice{}, fmt.Errorf("scan booking: %w", err)
	}
	if strings.TrimSpace(dateConsStr) != "" {
		r.DateConsultation, err = ParseConsultationWallClock(dateConsStr)
		if err != nil {
			return BookingNotice{}, err
		}
	}
	r.Status = int(status)
	r.Cancelled = cancelled != 0
	fillReminderDerived(&r.DueReminder, webName, examName, int(isWaitlist), int(noSMS), notify, plSubj, plExam, dateStart)
	return r, nil
}
