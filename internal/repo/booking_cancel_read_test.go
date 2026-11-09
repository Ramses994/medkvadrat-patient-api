package repo

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

// The cancel snapshot reads the actual answer -> direction link and the
// direction owner (cursor-lk-2-fixes-4.md, item 2); they are never inferred.
func TestLoadSnap_ReadsActualLinks(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	answ, dir := int64(7), int64(22)
	mock.ExpectQuery(`PATIENTS_ID, 0\), ISNULL\(PL_SUBJ_ID, 0\)\s+FROM PLANNING WHERE PLANNING_ID`).
		WillReturnRows(sqlmock.NewRows([]string{"c", "s", "pat", "subj"}).AddRow(0, 0, 1, 1119))
	mock.ExpectQuery(`SELECT CAST\(ISNULL\(ANSW_STATE, 0\) AS int\), PLANNING_ID, FM_BILL_ID, PATDIREC_ID\s+FROM DIR_ANSW WHERE DIR_ANSW_ID`).
		WillReturnRows(sqlmock.NewRows([]string{"st", "pl", "bill", "pd"}).AddRow(0, 5, nil, 33))
	mock.ExpectQuery(`SELECT CAST\(ISNULL\(CANCELLED, 0\) AS int\), CAST\(ISNULL\(DIR_STATE, 0\) AS int\), PLANNING_CR_ID\s+FROM PATDIREC WHERE PATDIREC_ID`).
		WillReturnRows(sqlmock.NewRows([]string{"c", "s", "cr"}).AddRow(0, 0, 99))
	mock.ExpectQuery(`DIR_ANSW_ID FROM DIR_ANSW`).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(7))
	mock.ExpectQuery(`PATDIREC_ID FROM PATDIREC`).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(22))
	mock.ExpectQuery(`DM_DOC_ID FROM DM_DOC`).WillReturnRows(sqlmock.NewRows([]string{"id"}))
	snap, err := LoadSnap(context.Background(), db, 5, &answ, &dir)
	if err != nil || !snap.ReadOK {
		t.Fatalf("snap %+v err=%v", snap, err)
	}
	if snap.AnswPatdirec == nil || *snap.AnswPatdirec != 33 {
		t.Fatalf("answer link %v", snap.AnswPatdirec)
	}
	if snap.DirPlanningCR == nil || *snap.DirPlanningCR != 99 {
		t.Fatalf("direction owner %v", snap.DirPlanningCR)
	}
	if snap.PlanningPatientID != 1 || snap.PlanningPlSubjID != 1119 {
		t.Fatalf("visit owner %d line %d", snap.PlanningPatientID, snap.PlanningPlSubjID)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
