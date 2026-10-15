package repo

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestBookMe_NullPatientID_NoMutatingSQL(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	mock.ExpectBegin()
	rows := sqlmock.NewRows([]string{"PL_SUBJ_ID", "DATE_CONS", "HEURE", "DUREE", "PATIENTS_ID"}).
		AddRow(1, time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC), 900, 15, nil)
	mock.ExpectQuery("SELECT PL_SUBJ_ID").WillReturnRows(rows)
	mock.ExpectRollback()

	_, err = BookMe(context.Background(), db, &PlanningRepo{}, &MotconsuRepo{}, 42, 1, 306, time.Now())
	if err == nil || err.Error() != "SLOT_NOT_FREE" {
		t.Fatalf("err=%v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestBookMe_NullPatientID_NoExec(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.MonitorPingsOption(true))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	mock.ExpectBegin()
	rows := sqlmock.NewRows([]string{"PL_SUBJ_ID", "DATE_CONS", "HEURE", "DUREE", "PATIENTS_ID"}).
		AddRow(1, time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC), 900, 15, sql.NullInt64{})
	mock.ExpectQuery("SELECT PL_SUBJ_ID").WillReturnRows(rows)
	mock.ExpectRollback()

	_, _ = BookMe(context.Background(), db, &PlanningRepo{}, &MotconsuRepo{}, 42, 1, 306, time.Now())
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
