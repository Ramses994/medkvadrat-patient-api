package service

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/confirmations"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/store"
	_ "modernc.org/sqlite"
)

// Transferred from the 05.11.2026 auditor repro. No production connection is opened.
func TestAuditStaleReminderDoesNotCreateWaitlist(t *testing.T) {
	ctx := context.Background()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	sq, err := sql.Open("sqlite", "file:audit_stale_waitlist?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	defer sq.Close()
	if err := store.Migrate(ctx, sq); err != nil {
		t.Fatal(err)
	}
	mock.ExpectQuery(`(?s)FROM PLANNING p`).WithArgs(int64(1)).WillReturnRows(
		sqlmock.NewRows([]string{"PATIENTS_ID", "DATE_START", "PL_SUBJ_ID", "PL_EXAM_ID", "BRANCH_ID"}).AddRow(
			int64(1), "2026-11-07 15:00:00", int64(1119), int64(10000688), int64(496),
		),
	)
	svc := &Services{MSSQL: db, SQLite: sq}
	_, err = svc.UpsertConfirmation(ctx, 1, 1, "declined", "max", "old-version")
	if !errors.Is(err, confirmations.ErrStaleReminder) {
		t.Fatalf("want STALE_REMINDER, got %v", err)
	}
	for _, table := range []string{"waitlist", "appointment_confirmations"} {
		var n int
		if err := sq.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 0 {
			t.Fatalf("%s unexpectedly changed: count=%d", table, n)
		}
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
