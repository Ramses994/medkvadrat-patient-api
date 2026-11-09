package repo

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestFindReconcile_ReadErrorIsNotMissing(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	mock.ExpectQuery("PLANNING_ID").WillReturnError(errors.New("mssql down"))
	id, ok, err := FindReconcile(context.Background(), db, 1, 1119, 10000688, 2058, "2026-11-08T10:00:00", "2026-11-08T09:00:00")
	if err == nil || ok || id != 0 {
		t.Fatalf("id=%d ok=%v err=%v", id, ok, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestFindReconcile_NoRowAndNull(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	mock.ExpectQuery("PLANNING_ID").WillReturnError(sql.ErrNoRows)
	id, ok, err := FindReconcile(context.Background(), db, 1, 1119, 10000688, 2058, "2026-11-08T10:00:00", "2026-11-08T09:00:00")
	if err != nil || ok || id != 0 {
		t.Fatalf("missing id=%d ok=%v err=%v", id, ok, err)
	}
	mock.ExpectQuery("PLANNING_ID").WillReturnRows(sqlmock.NewRows([]string{"PLANNING_ID"}).AddRow(nil))
	id, ok, err = FindReconcile(context.Background(), db, 1, 1119, 10000688, 2058, "2026-11-08T10:00:00", "2026-11-08T09:00:00")
	if err != nil || ok || id != 0 {
		t.Fatalf("null id=%d ok=%v err=%v", id, ok, err)
	}
}
