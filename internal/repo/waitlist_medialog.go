package repo

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/medkvadrat/medkvadrat-patient-api/internal/confirmations"
)

// WaitlistPlanningLive is the current Medialog view of an original appointment.
type WaitlistPlanningLive struct {
	PlanningID  int64
	PatientID   int64
	PatientName string
	Phone       string
	BirthDate   string
	DateStart   string
	Cancelled   int
	Status      int
	BranchID    int
	DoctorName  string
	Service     string
	IsWaitlist  bool
}

// WaitlistFutureVisit is a later future appointment for the same patient.
type WaitlistFutureVisit struct {
	PlanningID int64
	PatientID  int64
	CreatedAt  string
	DateStart  string
	DoctorName string
	Service    string
	IsWaitlist bool
}

func waitlistIN(prefix string, ids []int64) (string, []any) {
	var b strings.Builder
	args := make([]any, 0, len(ids))
	for i, id := range ids {
		if i > 0 {
			b.WriteString(",")
		}
		name := fmt.Sprintf("%s%d", prefix, i)
		b.WriteString("@" + name)
		args = append(args, sql.Named(name, id))
	}
	return b.String(), args
}

func waitlistOriginalSQL(planningIDs, waitlistIDs []int64) (string, []any) {
	inSQL, inArgs := waitlistIN("pid", planningIDs)
	examSQL, examArgs := examSelectSQL(waitlistIDs)
	q := `
SELECT
  p.PLANNING_ID,
  p.PATIENTS_ID,
  ISNULL(pt.NOM,'') + ' ' + ISNULL(pt.PRENOM,'') AS PATIENT_NAME,
  ISNULL(pt.MOBIL_TELEFON, ISNULL(pt.TEL, ISNULL(pt.RAB_TEL,''))) AS PHONE,
  ISNULL(CONVERT(varchar(10), pt.NE_LE, 23), '') AS DOB,
  CONVERT(varchar(19), p.DATE_START, 120) AS DATE_START,
  CAST(ISNULL(p.CANCELLED,0) AS int) AS CANCELLED,
  CAST(ISNULL(p.STATUS,0) AS int) AS STATUS,
  ISNULL(ps.FM_INTORG_ID, 0) AS BRANCH_ID,
` + doctorNameSelectSQL + ` AS DOCTOR_NAME,
` + examSQL + `
FROM PLANNING p
JOIN PATIENTS pt ON pt.PATIENTS_ID = p.PATIENTS_ID
LEFT JOIN PL_SUBJ ps ON ps.PL_SUBJ_ID = p.PL_SUBJ_ID
LEFT JOIN PL_EXAM ex ON ex.PL_EXAM_ID = p.PL_EXAM_ID
WHERE p.PLANNING_ID IN (` + inSQL + `)`
	return q, append(inArgs, examArgs...)
}

func waitlistFutureSQL(patientIDs, waitlistIDs []int64) (string, []any) {
	inSQL, inArgs := waitlistIN("pt", patientIDs)
	examSQL, examArgs := examSelectSQL(waitlistIDs)
	q := `
SELECT
  p.PLANNING_ID,
  p.PATIENTS_ID,
  CONVERT(varchar(19), p.CREATE_DATE_TIME, 120) AS CREATED_AT,
  CONVERT(varchar(19), p.DATE_START, 120) AS DATE_START,
` + doctorNameSelectSQL + ` AS DOCTOR_NAME,
` + examSQL + `
FROM PLANNING p
LEFT JOIN PL_SUBJ ps ON ps.PL_SUBJ_ID = p.PL_SUBJ_ID
LEFT JOIN PL_EXAM ex ON ex.PL_EXAM_ID = p.PL_EXAM_ID
WHERE p.PATIENTS_ID IN (` + inSQL + `)
  AND ISNULL(p.CANCELLED,0) = 0
  AND ISNULL(p.STATUS,0) = 0
  AND p.DATE_START > CONVERT(datetime, @now, 126)
ORDER BY p.DATE_START, p.PLANNING_ID`
	args := append(inArgs, examArgs...)
	return q, args
}

// WaitlistOriginals loads Medialog rows for the current waitlist page only.
func WaitlistOriginals(ctx context.Context, db *sql.DB, planningIDs []int64, waitlistIDs []int64) (map[int64]WaitlistPlanningLive, error) {
	out := map[int64]WaitlistPlanningLive{}
	if len(planningIDs) == 0 {
		return out, nil
	}
	q, args := waitlistOriginalSQL(planningIDs, waitlistIDs)
	rs, err := db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("waitlist originals: %w", err)
	}
	defer rs.Close()
	for rs.Next() {
		var row WaitlistPlanningLive
		var cancelled, status, isWL bitInt
		var name, phone, dob, dateStart, doctor sql.NullString
		var webName, examName sql.NullString
		if err := rs.Scan(
			&row.PlanningID, &row.PatientID, &name, &phone, &dob,
			&dateStart, &cancelled, &status, &row.BranchID, &doctor,
			&webName, &examName, &isWL,
		); err != nil {
			return nil, err
		}
		row.PatientName = name.String
		row.Phone = phone.String
		row.BirthDate = dob.String
		row.DateStart = dateStart.String
		row.Cancelled = int(cancelled)
		row.Status = int(status)
		row.DoctorName = trimDoctorName(doctor.String)
		row.Service = DisplayService(webName.String, examName.String)
		row.IsWaitlist = isWL != 0
		out[row.PlanningID] = row
	}
	return out, rs.Err()
}

// WaitlistFutures loads future uncancelled appointments for patients on the page.
func WaitlistFutures(ctx context.Context, db *sql.DB, patientIDs []int64, now time.Time, waitlistIDs []int64) ([]WaitlistFutureVisit, error) {
	if len(patientIDs) == 0 {
		return nil, nil
	}
	q, args := waitlistFutureSQL(patientIDs, waitlistIDs)
	args = append(args, sql.Named("now", waitlistNowBound(now)))
	rs, err := db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("waitlist futures: %w", err)
	}
	defer rs.Close()
	var out []WaitlistFutureVisit
	for rs.Next() {
		var row WaitlistFutureVisit
		var isWL bitInt
		var created, dateStart, doctor, webName, examName sql.NullString
		if err := rs.Scan(
			&row.PlanningID, &row.PatientID, &created, &dateStart,
			&doctor, &webName, &examName, &isWL,
		); err != nil {
			return nil, err
		}
		row.CreatedAt = created.String
		row.DateStart = dateStart.String
		row.DoctorName = trimDoctorName(doctor.String)
		row.Service = DisplayService(webName.String, examName.String)
		row.IsWaitlist = isWL != 0
		out = append(out, row)
	}
	return out, rs.Err()
}

func waitlistNowBound(now time.Time) string {
	return now.In(moscowLocation()).Format(mssqlReminderBoundLayout)
}

func PickNewerFuture(createdAtMSK string, futures []WaitlistFutureVisit, patientID int64) *WaitlistFutureVisit {
	for i := range futures {
		f := &futures[i]
		if f.PatientID != patientID {
			continue
		}
		if confirmations.MedialogCreateIsNewer(createdAtMSK, f.CreatedAt) {
			cp := *f
			return &cp
		}
	}
	return nil
}

func MedialogStateLabel(live WaitlistPlanningLive, apptStart string) string {
	if live.Cancelled != 0 {
		return "отмечено «Пациент не придёт»"
	}
	if live.DateStart != "" && apptStart != "" && live.DateStart != apptStart {
		return "запись перенесена"
	}
	return "запись действует"
}

func FutureVisitLabel(f *WaitlistFutureVisit) string {
	if f == nil {
		return ""
	}
	if f.IsWaitlist {
		return "внесён в лист ожидания Медиалога"
	}
	return "есть новая будущая запись"
}
