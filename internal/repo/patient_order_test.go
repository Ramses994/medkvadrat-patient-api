package repo

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestPhoneSearchOrdersByPatientID(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	r := &PatientRepo{db: db}
	ctx := context.Background()

	mock.ExpectQuery(`ORDER BY PATIENTS_ID`).WillReturnRows(sqlmock.NewRows([]string{
		"PATIENTS_ID", "FULL_NAME", "PHONE", "BIRTH_DATE",
	}))
	if _, err := r.SearchByPhone(ctx, "9265906994"); err != nil {
		t.Fatal(err)
	}

	mock.ExpectQuery(`ORDER BY PATIENTS_ID`).WillReturnRows(sqlmock.NewRows([]string{
		"PATIENTS_ID", "FULL_NAME", "EMAIL", "NOT_SEND_MAILING_SMS", "SEND_AUTO_EMAIL",
	}))
	if _, err := r.OTPPatientsByPhone(ctx, "9265906994"); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
