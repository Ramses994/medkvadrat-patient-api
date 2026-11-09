package repo

import (
	"context"
	"database/sql/driver"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/confirmations"
)

func TestWaitlistMedialogSQL_ReadOnly(t *testing.T) {
	b, err := os.ReadFile("waitlist_medialog.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	if SQLLooksMutating(src) {
		t.Fatal("waitlist Medialog SQL must be SELECT-only")
	}
	for _, frag := range []string{
		"CAST(ISNULL(p.CANCELLED,0) AS int)",
		"CAST(ISNULL(p.STATUS,0) AS int)",
		"ISNULL(p.CANCELLED,0) = 0",
		"ISNULL(p.STATUS,0) = 0",
	} {
		if !strings.Contains(src, frag) {
			t.Errorf("missing %s", frag)
		}
	}
}

func TestWaitlistOriginals_NullCancelledAsZero(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	cols := []string{
		"PLANNING_ID", "PATIENTS_ID", "PATIENT_NAME", "PHONE", "DOB", "DATE_START",
		"CANCELLED", "STATUS", "BRANCH_ID", "DOCTOR_NAME", "EXAM_WEB_NAME", "EXAM_NAME", "IS_WAITLIST",
	}
	row := []driver.Value{int64(1), int64(20), "Иванов", "7900", "1980-01-01", "2026-10-22 10:00:00", nil, nil, int64(106), "Петров", "", "Приём", 0}
	mock.ExpectQuery(`(?s)FROM PLANNING p`).WillReturnRows(sqlmock.NewRows(cols).AddRow(row...))
	got, err := WaitlistOriginals(context.Background(), db, []int64{1}, nil)
	if err != nil {
		t.Fatal(err)
	}
	live := got[1]
	if live.Cancelled != 0 || live.Status != 0 {
		t.Fatalf("%+v", live)
	}
	if MedialogStateLabel(live, "2026-10-22 10:00:00") != "запись действует" {
		t.Fatalf("state=%q", MedialogStateLabel(live, "2026-10-22 10:00:00"))
	}
}

func TestPickNewerFuture_OtherDoctorNeutral(t *testing.T) {
	created := confirmations.FormatWaitlistMSK(time.Date(2026, 10, 21, 23, 30, 0, 0, time.UTC))
	futures := []WaitlistFutureVisit{
		{PlanningID: 9, PatientID: 20, CreatedAt: "2026-10-22 01:00:00", DateStart: "2026-10-23 11:00:00", DoctorName: "A"},
		{PlanningID: 11, PatientID: 20, CreatedAt: "2026-10-22 03:00:00", DateStart: "2026-10-24 09:00:00", DoctorName: "Другой врач", Service: "Терапевт"},
	}
	got := PickNewerFuture(created, futures, 20)
	if got == nil || got.PlanningID != 11 {
		t.Fatalf("%+v", got)
	}
	if FutureVisitLabel(got) != "есть новая будущая запись" {
		t.Fatalf("label=%q", FutureVisitLabel(got))
	}
	got.IsWaitlist = true
	if FutureVisitLabel(got) != "внесён в лист ожидания Медиалога" {
		t.Fatalf("wl label=%q", FutureVisitLabel(got))
	}
}

func TestAuditWaitlistOriginals_NullDOBNotFatal(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	cols := []string{
		"PLANNING_ID", "PATIENTS_ID", "PATIENT_NAME", "PHONE", "DOB", "DATE_START",
		"CANCELLED", "STATUS", "BRANCH_ID", "DOCTOR_NAME", "EXAM_WEB_NAME", "EXAM_NAME", "IS_WAITLIST",
	}
	mock.ExpectQuery(`(?s)FROM PLANNING p`).WillReturnRows(sqlmock.NewRows(cols).AddRow(
		int64(1), int64(1), "", "", nil, "2026-10-23 10:00:00", int64(0), int64(0), int64(496), "", "", "", int64(0),
	))
	got, err := WaitlistOriginals(context.Background(), db, []int64{1}, nil)
	if err != nil {
		t.Fatalf("nullable DOB must not break the page: %v", err)
	}
	if got[1].BirthDate != "" {
		t.Fatalf("dob=%q", got[1].BirthDate)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestWaitlistOriginalsSQL_UsesNE_LE(t *testing.T) {
	q, _ := waitlistOriginalSQL([]int64{1}, nil)
	if !strings.Contains(q, "pt.NE_LE") {
		t.Fatal("DOB must come from PATIENTS.NE_LE")
	}
	if strings.Contains(q, "DATENAISS") {
		t.Fatal("DATENAISS is not the confirmed birth column")
	}
	if !strings.Contains(q, "COALESCE(NULLIF(LTRIM(RTRIM(pt.MOBIL_TELEFON)),''), NULLIF(LTRIM(RTRIM(pt.TEL)),''), NULLIF(LTRIM(RTRIM(pt.RAB_TEL)),''), '')") {
		t.Fatal("phone must skip a blank mobile number")
	}
}

func TestWaitlistFuturesSQL_ConvertDatetime126(t *testing.T) {
	q, _ := waitlistFutureSQL([]int64{1}, nil)
	if !strings.Contains(q, "p.DATE_START > CONVERT(datetime, @now, 126)") {
		t.Fatalf("need CONVERT 126, got %s", q)
	}
	if !strings.Contains(q, "ISNULL(ps.FM_INTORG_ID, 0) AS BRANCH_ID") {
		t.Fatal("future visit must carry the branch")
	}
}

func TestWaitlistNowBound_Cutoff(t *testing.T) {
	loc := time.FixedZone("MSK", 3*3600)
	now := time.Date(2026, 10, 22, 13, 30, 0, 0, loc)
	bound := waitlistNowBound(now)
	if bound != "2026-10-22T13:30:00" {
		t.Fatalf("bound=%q", bound)
	}
	if waitlistDateStartAfter("2026-10-22 11:00:00", bound) {
		t.Fatal("11:00 MSK is not future at 13:30")
	}
	if !waitlistDateStartAfter("2026-10-22 15:00:00", bound) {
		t.Fatal("15:00 MSK is future at 13:30")
	}
	endOfDay := waitlistNowBound(time.Date(2026, 10, 22, 23, 59, 0, 0, loc))
	if waitlistDateStartAfter("2026-10-22 23:59:00", endOfDay) {
		t.Fatal("23:59 is not after 23:59")
	}
	if !waitlistDateStartAfter("2026-10-23 00:01:00", endOfDay) {
		t.Fatal("00:01 next day is future")
	}
}

type nowStringArg string

func (n nowStringArg) Match(v driver.Value) bool {
	if _, ok := v.(time.Time); ok {
		return false
	}
	s, ok := v.(string)
	return ok && s == string(n)
}

func waitlistDateStartAfter(dateStart, nowBound string) bool {
	start, err1 := time.Parse("2006-01-02 15:04:05", dateStart)
	bound, err2 := time.Parse(mssqlReminderBoundLayout, nowBound)
	if err1 != nil || err2 != nil {
		return false
	}
	return start.After(bound)
}

func TestWaitlistFutures_DriverGetsStringNotTime(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	cols := []string{"PLANNING_ID", "PATIENTS_ID", "CREATED_AT", "DATE_START", "DOCTOR_NAME", "EXAM_WEB_NAME", "EXAM_NAME", "IS_WAITLIST"}
	mock.ExpectQuery(`(?s)CONVERT\(datetime, @now, 126\)`).WithArgs(
		sqlmock.AnyArg(),
		sqlmock.AnyArg(),
		nowStringArg("2026-10-22T13:30:00"),
	).WillReturnRows(sqlmock.NewRows(cols))
	loc := time.FixedZone("MSK", 3*3600)
	_, err = WaitlistFutures(context.Background(), db, []int64{1}, time.Date(2026, 10, 22, 13, 30, 0, 0, loc), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestWaitlistPageEnrich_OneQueryForThePage(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	cols := []string{
		"PLANNING_ID", "PATIENTS_ID", "PATIENT_NAME", "PHONE", "DOB", "DATE_START",
		"CANCELLED", "STATUS", "BRANCH_ID", "DOCTOR_NAME", "EXAM_WEB_NAME", "EXAM_NAME", "IS_WAITLIST",
		"FUT_PLANNING_ID", "FUT_CREATED", "FUT_DATE", "FUT_BRANCH", "FUT_DOCTOR", "FUT_WEB", "FUT_EXAM", "FUT_WAITLIST",
	}
	row := []driver.Value{
		int64(1), int64(20), "Иванов", "7900", "1980-01-01", "2026-10-22 10:00:00",
		nil, nil, int64(106), "Петров", "", "Приём", 0,
		int64(9), "2026-10-22 03:00:00", "2026-10-23 11:00:00", int64(3), "Врач", "", "Осмотр", 0,
	}
	mock.ExpectQuery(`(?s)OUTER APPLY`).WillReturnRows(sqlmock.NewRows(cols).AddRow(row...))
	ids := make([]int64, 25)
	for i := range ids {
		ids[i] = int64(i + 1)
	}
	loc := time.FixedZone("MSK", 3*3600)
	now := time.Date(2026, 10, 22, 13, 30, 0, 0, loc)
	live, futures, err := WaitlistPageEnrich(context.Background(), db, ids, []int64{20}, nil, now, nil)
	if err != nil {
		t.Fatal(err)
	}
	if live[1].BranchID != 106 || len(futures) != 1 || futures[0].PlanningID != 9 || futures[0].BranchID != 3 {
		t.Fatalf("live=%+v futures=%+v", live[1], futures)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
	q, _ := waitlistPageSQL(ids, nil, now, nil)
	if strings.Count(q, "CONVERT(datetime, @now, 126)") != 1 {
		t.Fatal("page enrich must bind the future window once")
	}
}

type futureCandidate struct {
	ID        int64
	Created   time.Time
	Start     time.Time
	Branch    int
	Exam      int64
	Cancelled int
	Status    int
}

func chooseNextVisit(originalID int64, answer, now time.Time, exams []int64, cands []futureCandidate) (int64, bool) {
	var best *futureCandidate
	for i := range cands {
		c := &cands[i]
		if c.ID == originalID || c.Cancelled != 0 || c.Status != 0 || !c.Start.After(now) || !c.Created.After(answer) {
			continue
		}
		if c.Branch != 106 && c.Branch != 3 && c.Branch != 496 {
			continue
		}
		skip := false
		for _, exam := range NormalizeWaitlistExamIDs(exams) {
			if c.Exam == exam {
				skip = true
			}
		}
		if skip {
			continue
		}
		if best == nil || c.Start.Before(best.Start) || (c.Start.Equal(best.Start) && c.ID < best.ID) {
			best = c
		}
	}
	if best == nil {
		return 0, false
	}
	return best.ID, true
}

func TestFutureApply_SecondVisitNotOriginalOrOlder(t *testing.T) {
	answer := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	now := answer
	cands := []futureCandidate{
		{ID: 10, Created: answer.Add(-2 * time.Hour), Start: answer.Add(time.Hour), Branch: 106, Exam: 100},
		{ID: 11, Created: answer.Add(-time.Minute), Start: answer.Add(2 * time.Hour), Branch: 106, Exam: 100},
		{ID: 13, Created: answer.Add(time.Minute), Start: answer.Add(30 * time.Minute), Branch: 1, Exam: 100},
		{ID: 14, Created: answer.Add(time.Minute), Start: answer.Add(40 * time.Minute), Branch: 106, Exam: 3118},
		{ID: 12, Created: answer.Add(time.Minute), Start: answer.Add(3 * time.Hour), Branch: 106, Exam: 100},
	}
	id, ok := chooseNextVisit(10, answer, now, []int64{3118}, cands)
	if !ok || id != 12 {
		t.Fatalf("chosen=%d ok=%v", id, ok)
	}

	q, _ := waitlistPageSQL([]int64{10}, []int64{3118}, now, map[int64]string{10: "2026-11-06 15:00:00"})
	apply := q[strings.Index(q, "OUTER APPLY"):]
	where := strings.Index(apply, "WHERE p2.PATIENTS_ID")
	order := strings.Index(apply, "ORDER BY p2.DATE_START")
	if where < 0 || order < where {
		t.Fatal("future filters must sit in the APPLY before ORDER BY")
	}
	body := apply[where:order]
	for _, frag := range []string{
		"p2.PLANNING_ID <> p.PLANNING_ID",
		"ISNULL(ps2.FM_INTORG_ID, 0) IN (106, 3, 496)",
		"ISNULL(p2.PL_EXAM_ID, 0) NOT IN",
		"p2.CREATE_DATE_TIME > CASE p.PLANNING_ID",
		"CONVERT(datetime, @at0, 120)",
		"SELECT TOP 1",
	} {
		if frag == "SELECT TOP 1" {
			if !strings.Contains(apply[:where], frag) {
				t.Fatal(frag)
			}
			continue
		}
		if !strings.Contains(body, frag) {
			t.Fatalf("missing %s\n%s", frag, body)
		}
	}
}
