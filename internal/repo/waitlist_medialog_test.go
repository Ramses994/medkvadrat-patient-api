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
}

func TestWaitlistFuturesSQL_ConvertDatetime126(t *testing.T) {
	q, _ := waitlistFutureSQL([]int64{1}, nil)
	if !strings.Contains(q, "p.DATE_START > CONVERT(datetime, @now, 126)") {
		t.Fatalf("need CONVERT 126, got %s", q)
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
