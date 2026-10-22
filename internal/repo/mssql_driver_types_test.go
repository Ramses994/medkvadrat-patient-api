package repo

import (
	"context"
	"database/sql/driver"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

func reminderCols() []string {
	return []string{
		"PLANNING_ID", "PATIENTS_ID", "PHONE", "FULL_NAME", "DOCTOR_NAME",
		"EXAM_WEB_NAME", "EXAM_NAME", "IS_WAITLIST", "NO_SMS_NOTIFY", "MESSENGER_NOTIFY",
		"PL_SUBJ_ID", "PL_EXAM_ID", "DATE_START_FULL", "BRANCH_ID", "BRANCH_CODE",
		"DATE_CONSULTATION", "STATUS",
	}
}

func reminderDriverRow(planningID int64, createdSuffix bool) []driver.Value {
	row := []driver.Value{
		planningID,
		int64(1),
		[]byte(""),
		[]byte("Пациент"),
		[]byte("Иванов И."),
		[]byte("#rus#Прием врачом-терапевтом"),
		[]byte("Прием"),
		false,       // IS_WAITLIST bit
		false,       // NO_SMS_NOTIFY bit — this used to break Scan into int
		true,        // MESSENGER_NOTIFY bit
		int64(10),
		int64(20),
		[]byte("2026-06-26 19:30:00"),
		int64(3),
		[]byte("Куркино"),
		[]byte("2026-06-26 19:30"),
		int64(0),
	}
	if createdSuffix {
		row = append(row, []byte("2026-06-23 12:00:00.123"))
	}
	return row
}

func TestDueReminders_ScanDriverTypes(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	rows := sqlmock.NewRows(reminderCols()).AddRow(reminderDriverRow(10, false)...)
	mock.ExpectQuery(`(?s)FROM PLANNING p`).WillReturnRows(rows)

	from := time.Date(2026, 6, 26, 0, 0, 0, 0, moscowLocation())
	to := from.Add(24 * time.Hour)
	got, err := DueReminders(context.Background(), db, from, to, []int64{1}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("len=%d", len(got))
	}
	if got[0].Service != "Прием врачом-терапевтом" {
		t.Fatalf("service=%q", got[0].Service)
	}
	if got[0].NoSMSNotify || got[0].IsWaitlist {
		t.Fatalf("%+v", got[0])
	}
	if got[0].MessengerNotify == nil || *got[0].MessengerNotify != 1 {
		t.Fatalf("notify=%v", got[0].MessengerNotify)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestNewBookings_ScanDriverTypesAndPageBounds(t *testing.T) {
	now := time.Date(2026, 6, 23, 12, 0, 0, 0, moscowLocation())
	cols := append(reminderCols(), "CREATED_AT")

	t.Run("200_has_more_false", func(t *testing.T) {
		db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		rs := sqlmock.NewRows(cols)
		for i := 1; i <= 200; i++ {
			rs.AddRow(reminderDriverRow(int64(i), true)...)
		}
		mock.ExpectQuery(`(?s)SELECT TOP`).WillReturnRows(rs)
		page, err := NewBookings(context.Background(), db, now, []int64{1}, nil, 200, nil)
		if err != nil {
			t.Fatal(err)
		}
		if page.HasMore || len(page.Rows) != 200 {
			t.Fatalf("has_more=%v n=%d", page.HasMore, len(page.Rows))
		}
		if page.NextCursor == nil || page.NextCursor.PlanningID != 200 {
			t.Fatalf("cursor=%+v", page.NextCursor)
		}
	})

	t.Run("201_has_more_true", func(t *testing.T) {
		db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		rs := sqlmock.NewRows(cols)
		for i := 1; i <= 201; i++ {
			row := reminderDriverRow(int64(i), true)
			row[len(row)-1] = []byte("2026-06-23 12:00:00." + pad3(i))
			rs.AddRow(row...)
		}
		mock.ExpectQuery(`(?s)SELECT TOP`).WillReturnRows(rs)
		page, err := NewBookings(context.Background(), db, now, []int64{1}, nil, 200, nil)
		if err != nil {
			t.Fatal(err)
		}
		if !page.HasMore || len(page.Rows) != 200 {
			t.Fatalf("has_more=%v n=%d", page.HasMore, len(page.Rows))
		}
		if page.NextCursor == nil || page.NextCursor.PlanningID != 200 {
			t.Fatalf("next_cursor must be last returned row, got %+v", page.NextCursor)
		}
	})
}

func pad3(n int) string {
	s := []byte("000")
	v := n % 1000
	s[2] = byte('0' + v%10)
	s[1] = byte('0' + (v/10)%10)
	s[0] = byte('0' + (v/100)%10)
	return string(s)
}

func TestBookingByPlanningID_ScanCancelledBit(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	cols := append(reminderCols(), "CREATED_AT", "CANCELLED")
	row := append(reminderDriverRow(55, true), true)
	mock.ExpectQuery(`(?s)WHERE p.PLANNING_ID = @id`).WillReturnRows(sqlmock.NewRows(cols).AddRow(row...))
	got, err := BookingByPlanningID(context.Background(), db, 55, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Cancelled || got.PlanningID != 55 {
		t.Fatalf("%+v", got)
	}
}

func TestNotifySQL_CastsBitColumns(t *testing.T) {
	sql, _ := dueRemindersBaseSQL(nil)
	for _, frag := range []string{
		"CAST(ISNULL(ex.NO_SMS_NOTIFY, 0) AS int)",
		"CAST(r.MESSENGER_NOTIFY AS int)",
		"CAST(ISNULL(p.STATUS,-1) AS int)",
	} {
		if !strings.Contains(sql, frag) {
			t.Errorf("due reminders missing %s", frag)
		}
	}
}
