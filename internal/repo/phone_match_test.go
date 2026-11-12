package repo

import (
	"context"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestPhoneMatch_ExactNormalizedNumber(t *testing.T) {
	const want = "79265906994"
	cases := []struct {
		name   string
		mobile string
		tel    string
		work   string
		match  bool
	}{
		{name: "foreign prefix", mobile: "+1 926 590-69-94", match: false},
		{name: "parentheses", mobile: "8 (926) 590-69-94", match: true},
		{name: "list in one field", tel: "89265906994, 84951234567", match: true},
		{name: "extra digit", mobile: "792659069941", match: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := HasPhone(tc.mobile, tc.tel, tc.work, want); got != tc.match {
				t.Fatalf("match=%v want %v", got, tc.match)
			}
		})
	}
	if HasPhone("8 (926) 590-69-94", "", "", "") {
		t.Fatal("empty query matched")
	}
}

func TestPhoneSearch_KeepsOnlyExactMatches(t *testing.T) {
	assertFiltered(t, true)
	assertFiltered(t, false)
}

func assertFiltered(t *testing.T, search bool) {
	t.Helper()
	var gotSQL string
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherFunc(
		func(_, actual string) error {
			gotSQL = actual
			return nil
		},
	)))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	r := &PatientRepo{db: db}
	if search {
		mock.ExpectQuery("PATIENTS").WillReturnRows(sqlmock.NewRows([]string{
			"PATIENTS_ID", "FULL_NAME", "PHONE", "BIRTH_DATE", "MOBIL_TELEFON", "TEL", "RAB_TEL",
		}).AddRow(1, "Чужой", "+1 926 590-69-94", nil, "+1 926 590-69-94", "", "").
			AddRow(2, "Скобки", "8 (926) 590-69-94", nil, "8 (926) 590-69-94", "", "").
			AddRow(3, "Список", "89265906994, 84951234567", nil, "", "89265906994, 84951234567", "").
			AddRow(4, "Длинный", "792659069941", nil, "792659069941", "", ""))
		got, err := r.SearchByPhone(context.Background(), "79265906994")
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 2 || got[0].PatientID != 2 || got[1].PatientID != 3 {
			t.Fatalf("%+v", got)
		}
		if got[0].Phone != "8 (926) 590-69-94" {
			t.Fatalf("display phone=%q", got[0].Phone)
		}
	} else {
		mock.ExpectQuery("PATIENTS").WillReturnRows(sqlmock.NewRows([]string{
			"PATIENTS_ID", "FULL_NAME", "EMAIL", "NOT_SEND_MAILING_SMS", "SEND_AUTO_EMAIL", "MOBIL_TELEFON", "TEL", "RAB_TEL",
		}).AddRow(1, "Чужой", "", 0, 0, "+1 926 590-69-94", "", "").
			AddRow(2, "Скобки", "", 0, 0, "8 (926) 590-69-94", "", "").
			AddRow(3, "Список", "", 0, 0, "", "89265906994, 84951234567", "").
			AddRow(4, "Длинный", "", 0, 0, "792659069941", "", ""))
		got, err := r.OTPPatientsByPhone(context.Background(), "79265906994")
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 2 || got[0].PatientID != 2 || got[1].PatientID != 3 {
			t.Fatalf("%+v", got)
		}
	}
	if !strings.Contains(gotSQL, "),'.','')") && !strings.Contains(gotSQL, ").',''") {
		if !strings.Contains(gotSQL, "'.',''") || !strings.Contains(gotSQL, "')',''") {
			t.Fatalf("replace chain = %s", gotSQL)
		}
	}
	if !strings.Contains(gotSQL, "LIKE") || !strings.Contains(gotSQL, "ORDER BY PATIENTS_ID") {
		t.Fatalf("query = %s", gotSQL)
	}
	if search && !strings.Contains(gotSQL, "NE_LE") {
		t.Fatalf("birth column missing: %s", gotSQL)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
