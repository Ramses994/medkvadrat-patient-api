package repo

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestSearchByPhone_ReadsBirthDateFromNE_LE(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherFunc(
		func(_, actual string) error {
			if strings.Contains(actual, "NULL AS BIRTH_DATE") || !strings.Contains(actual, "NE_LE") {
				t.Fatalf("query = %s", actual)
			}
			return nil
		},
	)))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	r := &PatientRepo{db: db}
	when := time.Date(1985, 1, 2, 15, 4, 0, 0, time.FixedZone("MSK", 3*3600))
	mock.ExpectQuery("NE_LE").WillReturnRows(sqlmock.NewRows([]string{
		"PATIENTS_ID", "FULL_NAME", "PHONE", "BIRTH_DATE",
	}).AddRow(1, "Иванов Иван", "+79990000000", when))

	got, err := r.SearchByPhone(context.Background(), "9990000000")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].BirthDate == nil || got[0].BirthDate.Format("2006-01-02") != "1985-01-02" {
		t.Fatalf("%+v", got)
	}

	mock.ExpectQuery("NE_LE").WillReturnRows(sqlmock.NewRows([]string{
		"PATIENTS_ID", "FULL_NAME", "PHONE", "BIRTH_DATE",
	}).AddRow(2, "Без Даты", "+79990000000", nil))
	empty, err := r.SearchByPhone(context.Background(), "9990000000")
	if err != nil || len(empty) != 1 || empty[0].BirthDate != nil {
		t.Fatalf("%+v err=%v", empty, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
