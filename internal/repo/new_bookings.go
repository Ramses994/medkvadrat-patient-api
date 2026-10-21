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
	CreatedAt time.Time
}

const maxNewBookingPatients = 50
const maxNewBookingRows = 200

func RequirePatientIDsCSV(raw string, maxN int) ([]int64, error) {
	ids, err := ParsePatientIDsCSV(raw)
	if err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return nil, fmt.Errorf("invalid patient_ids")
	}
	if maxN > 0 && len(ids) > maxN {
		return nil, fmt.Errorf("too many patient_ids")
	}
	return ids, nil
}

func NewBookings(ctx context.Context, db *sql.DB, since, now time.Time, patientIDs []int64) ([]NewBooking, error) {
	if len(patientIDs) == 0 || len(patientIDs) > maxNewBookingPatients {
		return nil, fmt.Errorf("invalid patient_ids")
	}
	loc := moscowLocation()
	sinceBound := since.In(loc).Format(mssqlReminderBoundLayout)
	nowBound := now.In(loc).Format(mssqlReminderBoundLayout)

	var in strings.Builder
	args := []any{
		sql.Named("since", sinceBound),
		sql.Named("now", nowBound),
	}
	for i, id := range patientIDs {
		if i > 0 {
			in.WriteString(",")
		}
		name := fmt.Sprintf("pid%d", i)
		in.WriteString("@" + name)
		args = append(args, sql.Named(name, id))
	}

	query := fmt.Sprintf(`
SELECT TOP %d
  p.PLANNING_ID,
  p.PATIENTS_ID,
  REPLACE(REPLACE(REPLACE(REPLACE(ISNULL(pt.MOBIL_TELEFON, ISNULL(pt.TEL, ISNULL(pt.RAB_TEL,''))),'+',''),'-',''),' ',''),'(','') AS PHONE,
  ISNULL(pt.NOM,'') + ' ' + ISNULL(pt.PRENOM,'') AS FULL_NAME,
%s AS DOCTOR_NAME,
%s AS SPECIALTY,
  ISNULL(o.FM_ORG_ID,0) AS BRANCH_ID,
  ISNULL(o.CODE,'') AS BRANCH_CODE,
  CONVERT(varchar(16), p.DATE_START, 120) AS DATE_CONSULTATION,
  ISNULL(p.STATUS,0) AS STATUS,
  CONVERT(varchar(19), p.CREATE_DATE_TIME, 120) AS CREATED_AT
FROM PLANNING p
JOIN PATIENTS pt ON pt.PATIENTS_ID = p.PATIENTS_ID
JOIN PL_SUBJ ps ON ps.PL_SUBJ_ID = p.PL_SUBJ_ID
LEFT JOIN FM_ORG o ON o.FM_ORG_ID = ps.FM_INTORG_ID
WHERE p.PATIENTS_ID IN (%s)
  AND p.CREATE_DATE_TIME > @since
  AND ISNULL(p.CANCELLED,0) = 0
  AND ISNULL(p.STATUS,0) = 0
  AND p.DATE_START > @now
ORDER BY p.CREATE_DATE_TIME`, maxNewBookingRows, doctorNameSelectSQL, specialtySelectSQL, in.String())

	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query new bookings: %w", err)
	}
	defer rows.Close()

	var out []NewBooking
	for rows.Next() {
		var r NewBooking
		var dateConsStr, createdStr string
		if err := rows.Scan(
			&r.PlanningID, &r.PatientID, &r.PatientPhone, &r.PatientName, &r.DoctorName, &r.Specialty,
			&r.BranchID, &r.BranchCode, &dateConsStr, &r.Status, &createdStr,
		); err != nil {
			return nil, fmt.Errorf("scan new bookings: %w", err)
		}
		r.DateConsultation, err = ParseConsultationWallClock(dateConsStr)
		if err != nil {
			return nil, err
		}
		r.CreatedAt, err = parseCreatedAt(createdStr)
		if err != nil {
			return nil, err
		}
		r.DoctorName = SanitizeScheduleLabel(r.DoctorName)
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows new bookings: %w", err)
	}
	return out, nil
}

func parseCreatedAt(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	const layout = "2006-01-02 15:04:05"
	if len(s) > len(layout) {
		s = s[:len(layout)]
	}
	if len(s) == len(consultationWallClockLayout) {
		return ParseConsultationWallClock(s)
	}
	t, err := time.ParseInLocation(layout, s, moscowLocation())
	if err != nil {
		return time.Time{}, fmt.Errorf("parse created_at %q: %w", s, err)
	}
	return t, nil
}
