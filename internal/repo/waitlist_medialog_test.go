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
