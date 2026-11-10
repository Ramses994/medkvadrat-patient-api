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
	BranchID   int
	OriginID   int64
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
  COALESCE(NULLIF(LTRIM(RTRIM(pt.MOBIL_TELEFON)),''), NULLIF(LTRIM(RTRIM(pt.TEL)),''), NULLIF(LTRIM(RTRIM(pt.RAB_TEL)),''), '') AS PHONE,
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
  ISNULL(ps.FM_INTORG_ID, 0) AS BRANCH_ID,
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
			&row.PlanningID, &row.PatientID, &created, &dateStart, &row.BranchID,
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

// WaitlistPageEnrich loads the original appointment and its next future visit
// for the current page in one Medialog query. Patients whose original row is
// gone (registry-only lines) are read by a second query.
func WaitlistPageEnrich(ctx context.Context, db *sql.DB, planningIDs, patientIDs, waitlistIDs []int64, now time.Time, answered map[int64]string) (map[int64]WaitlistPlanningLive, []WaitlistFutureVisit, error) {
	live := map[int64]WaitlistPlanningLive{}
	var futures []WaitlistFutureVisit
	if len(planningIDs) == 0 {
		if len(patientIDs) == 0 {
			return live, nil, nil
		}
		rows, err := WaitlistFutures(ctx, db, patientIDs, now, waitlistIDs)
		return live, rows, err
	}
	const batch = 100
	for start := 0; start < len(planningIDs); start += batch {
		end := start + batch
		if end > len(planningIDs) {
			end = len(planningIDs)
		}
		partLive, partFut, err := waitlistPageEnrichChunk(ctx, db, planningIDs[start:end], waitlistIDs, now, answered)
		if err != nil {
			return nil, nil, err
		}
		for id, row := range partLive {
			live[id] = row
		}
		futures = append(futures, partFut...)
	}
	covered := map[int64]bool{}
	for _, row := range live {
		covered[row.PatientID] = true
	}
	var missing []int64
	seen := map[int64]bool{}
	for _, id := range patientIDs {
		if id == 0 || covered[id] || seen[id] {
			continue
		}
		seen[id] = true
		missing = append(missing, id)
	}
	if len(missing) == 0 {
		return live, futures, nil
	}
	extra, err := WaitlistFutures(ctx, db, missing, now, waitlistIDs)
	if err != nil {
		return nil, nil, err
	}
	return live, append(futures, extra...), nil
}

func waitlistPageEnrichChunk(ctx context.Context, db *sql.DB, planningIDs, waitlistIDs []int64, now time.Time, answered map[int64]string) (map[int64]WaitlistPlanningLive, []WaitlistFutureVisit, error) {
	live := map[int64]WaitlistPlanningLive{}
	var futures []WaitlistFutureVisit
	q, args := waitlistPageSQL(planningIDs, waitlistIDs, now, answered)
	rs, err := db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, nil, fmt.Errorf("waitlist page: %w", err)
	}
	defer rs.Close()
	for rs.Next() {
		var row WaitlistPlanningLive
		var cancelled, status, isWL bitInt
		var name, phone, dob, dateStart, doctor, webName, examName sql.NullString
		var futID sql.NullInt64
		var futCreated, futDate, futDoctor, futWeb, futExam sql.NullString
		var futBranch sql.NullInt64
		var futWL bitInt
		if err := rs.Scan(
			&row.PlanningID, &row.PatientID, &name, &phone, &dob,
			&dateStart, &cancelled, &status, &row.BranchID, &doctor,
			&webName, &examName, &isWL,
			&futID, &futCreated, &futDate, &futBranch, &futDoctor, &futWeb, &futExam, &futWL,
		); err != nil {
			return nil, nil, err
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
		live[row.PlanningID] = row
		if futID.Valid {
			futures = append(futures, WaitlistFutureVisit{
				OriginID:   row.PlanningID,
				PlanningID: futID.Int64,
				PatientID:  row.PatientID,
				CreatedAt:  futCreated.String,
				DateStart:  futDate.String,
				BranchID:   int(futBranch.Int64),
				DoctorName: trimDoctorName(futDoctor.String),
				Service:    DisplayService(futWeb.String, futExam.String),
				IsWaitlist: futWL != 0,
			})
		}
	}
	if err := rs.Err(); err != nil {
		return nil, nil, err
	}
	return live, futures, nil
}

func VisitCreatedAfter(origin, candidate string) bool {
	left := parseMoscowClock(origin)
	right := parseMoscowClock(candidate)
	if left.IsZero() || right.IsZero() {
		return false
	}
	return right.After(left)
}

func parseMoscowClock(raw string) time.Time {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}
	}
	loc := moscowLocation()
	for _, layout := range []string{time.RFC3339, "2006-01-02 15:04:05", "2006-01-02T15:04:05", "2006-01-02T15:04:05Z07:00"} {
		if t, err := time.ParseInLocation(layout, raw, loc); err == nil {
			return t
		}
	}
	return time.Time{}
}

func WaitlistBranches(ctx context.Context, db *sql.DB, ids []int64) (map[int64]int, error) {
	found := map[int64]int{}
	if db == nil || len(ids) == 0 {
		return found, nil
	}
	const batch = 100
	for start := 0; start < len(ids); start += batch {
		end := start + batch
		if end > len(ids) {
			end = len(ids)
		}
		chunk := ids[start:end]
		holders := make([]string, len(chunk))
		args := make([]any, len(chunk))
		for i, id := range chunk {
			name := fmt.Sprintf("b%d", i)
			holders[i] = "@" + name
			args[i] = sql.Named(name, id)
		}
		q := `
SELECT p.PLANNING_ID, ISNULL(ps.FM_INTORG_ID, 0)
FROM PLANNING p
LEFT JOIN PL_SUBJ ps ON ps.PL_SUBJ_ID = p.PL_SUBJ_ID
WHERE p.PLANNING_ID IN (` + strings.Join(holders, ",") + `)`
		rs, err := db.QueryContext(ctx, q, args...)
		if err != nil {
			return nil, err
		}
		for rs.Next() {
			var id int64
			var branch int
			if err := rs.Scan(&id, &branch); err != nil {
				rs.Close()
				return nil, err
			}
			found[id] = branch
		}
		err = rs.Err()
		rs.Close()
		if err != nil {
			return nil, err
		}
	}
	return found, nil
}

func waitlistPageSQL(planningIDs, waitlistIDs []int64, now time.Time, answered map[int64]string) (string, []any) {
	inSQL, inArgs := waitlistIN("pid", planningIDs)
	examSQL, examArgs := examSelectSQL(waitlistIDs)
	futExam, futArgs := examSelectAliased("ex2", "fwl", waitlistIDs)
	notIn, notArgs := examNotInSQL(waitlistIDs)
	answerSQL, answerArgs := futureAnswerSQL(planningIDs, answered)
	doctor2 := strings.ReplaceAll(doctorNameSelectSQL, "ps.", "ps2.")
	q := `
SELECT
  p.PLANNING_ID,
  p.PATIENTS_ID,
  ISNULL(pt.NOM,'') + ' ' + ISNULL(pt.PRENOM,'') AS PATIENT_NAME,
  COALESCE(NULLIF(LTRIM(RTRIM(pt.MOBIL_TELEFON)),''), NULLIF(LTRIM(RTRIM(pt.TEL)),''), NULLIF(LTRIM(RTRIM(pt.RAB_TEL)),''), '') AS PHONE,
  ISNULL(CONVERT(varchar(10), pt.NE_LE, 23), '') AS DOB,
  CONVERT(varchar(19), p.DATE_START, 120) AS DATE_START,
  CAST(ISNULL(p.CANCELLED,0) AS int) AS CANCELLED,
  CAST(ISNULL(p.STATUS,0) AS int) AS STATUS,
  ISNULL(ps.FM_INTORG_ID, 0) AS BRANCH_ID,
` + doctorNameSelectSQL + ` AS DOCTOR_NAME,
` + examSQL + `,
  fut.FUT_PLANNING_ID,
  fut.FUT_CREATED,
  fut.FUT_DATE,
  fut.FUT_BRANCH,
  fut.FUT_DOCTOR,
  fut.FUT_WEB,
  fut.FUT_EXAM,
  fut.FUT_WAITLIST
FROM PLANNING p
JOIN PATIENTS pt ON pt.PATIENTS_ID = p.PATIENTS_ID
LEFT JOIN PL_SUBJ ps ON ps.PL_SUBJ_ID = p.PL_SUBJ_ID
LEFT JOIN PL_EXAM ex ON ex.PL_EXAM_ID = p.PL_EXAM_ID
OUTER APPLY (
  SELECT TOP 1
    p2.PLANNING_ID AS FUT_PLANNING_ID,
    CONVERT(varchar(19), p2.CREATE_DATE_TIME, 120) AS FUT_CREATED,
    CONVERT(varchar(19), p2.DATE_START, 120) AS FUT_DATE,
    ISNULL(ps2.FM_INTORG_ID, 0) AS FUT_BRANCH,
` + doctor2 + ` AS FUT_DOCTOR,
` + futExam + `
  FROM PLANNING p2
  LEFT JOIN PL_SUBJ ps2 ON ps2.PL_SUBJ_ID = p2.PL_SUBJ_ID
  LEFT JOIN PL_EXAM ex2 ON ex2.PL_EXAM_ID = p2.PL_EXAM_ID
  WHERE p2.PATIENTS_ID = p.PATIENTS_ID
    AND ISNULL(p2.CANCELLED,0) = 0
    AND ISNULL(p2.STATUS,0) = 0
    AND p2.DATE_START > CONVERT(datetime, @now, 126)
    AND p2.PLANNING_ID <> p.PLANNING_ID
    AND ISNULL(ps2.FM_INTORG_ID, 0) IN (106, 3, 496)
    AND ISNULL(p2.PL_EXAM_ID, 0) NOT IN (` + notIn + `)
    AND ` + answerSQL + `
  ORDER BY p2.DATE_START, p2.PLANNING_ID
) fut
WHERE p.PLANNING_ID IN (` + inSQL + `)`
	args := append(append(inArgs, examArgs...), futArgs...)
	args = append(args, notArgs...)
	args = append(args, answerArgs...)
	args = append(args, sql.Named("now", waitlistNowBound(now)))
	return q, args
}

func examNotInSQL(waitlistIDs []int64) (string, []any) {
	ids := NormalizeWaitlistExamIDs(waitlistIDs)
	holders := make([]string, len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		name := fmt.Sprintf("nex%d", i)
		holders[i] = "@" + name
		args[i] = sql.Named(name, id)
	}
	return strings.Join(holders, ","), args
}

func futureAnswerSQL(planningIDs []int64, answered map[int64]string) (string, []any) {
	if len(answered) == 0 {
		return "p2.CREATE_DATE_TIME > p.CREATE_DATE_TIME", nil
	}
	var b strings.Builder
	b.WriteString("p2.CREATE_DATE_TIME > CASE p.PLANNING_ID")
	args := make([]any, 0, len(answered)*2)
	n := 0
	for _, id := range planningIDs {
		at, ok := answered[id]
		if !ok || strings.TrimSpace(at) == "" {
			continue
		}
		pid := fmt.Sprintf("ap%d", n)
		tm := fmt.Sprintf("at%d", n)
		n++
		fmt.Fprintf(&b, " WHEN @%s THEN CONVERT(datetime, @%s, 120)", pid, tm)
		args = append(args, sql.Named(pid, id), sql.Named(tm, at))
	}
	if n == 0 {
		return "p2.CREATE_DATE_TIME > p.CREATE_DATE_TIME", nil
	}
	b.WriteString(" ELSE p.CREATE_DATE_TIME END")
	return b.String(), args
}

func examSelectAliased(alias, argPrefix string, waitlistIDs []int64) (string, []any) {
	ids := NormalizeWaitlistExamIDs(waitlistIDs)
	var in strings.Builder
	args := make([]any, 0, len(ids))
	for i, id := range ids {
		if i > 0 {
			in.WriteString(",")
		}
		name := fmt.Sprintf("%s%d", argPrefix, i)
		in.WriteString("@" + name)
		args = append(args, sql.Named(name, id))
	}
	frag := `
ISNULL(` + alias + `.WEB_NAME,'') AS FUT_WEB,
ISNULL(` + alias + `.NAME,'') AS FUT_EXAM,
CAST(CASE WHEN ISNULL(` + alias + `.NAME,'') LIKE N'%ожидан%' OR ` + alias + `.PL_EXAM_ID IN (` + in.String() + `) THEN 1 ELSE 0 END AS int) AS FUT_WAITLIST`
	return frag, args
}
