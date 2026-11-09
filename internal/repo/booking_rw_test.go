package repo

import (
	"context"
	"database/sql"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	mssql "github.com/denisenkom/go-mssqldb"
)

func TestRWBatchSQL(t *testing.T) {
	b, err := os.ReadFile("booking_rw.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	if regexp.MustCompile(`(?i)\b(INSERT|UPDATE|DELETE|MERGE)\b`).MatchString(src) {
		t.Fatal("RW batch must not contain INSERT, UPDATE, DELETE or MERGE")
	}
	if regexp.MustCompile(`(?i)SELECT\b[\s\S]*\bFROM\b`).MatchString(src) {
		t.Fatal("RW batch must not SELECT from a table")
	}
	if !strings.Contains(src, "SELECT @@LANGUAGE") {
		t.Fatal("language check missing")
	}
	execs := regexp.MustCompile(`(?i)\bEXEC\s+dbo\.([A-Za-z0-9_]+)`).FindAllStringSubmatch(src, -1)
	allowed := map[string]bool{"set_session_params": true, "pl_AddVisitContext": true, "pl_CancelVisit": true}
	if len(execs) == 0 {
		t.Fatal("no EXEC")
	}
	for _, m := range execs {
		if !allowed[m[1]] {
			t.Fatalf("unexpected EXEC %s", m[1])
		}
	}
	if strings.Contains(src, "time.Time") {
		t.Fatal("time.Time in RW file")
	}
	for _, needle := range []string{"CONVERT(datetime, @visit_s, 126)", "pl_AddVisitContext", "pl_CancelVisit", "set_session_params"} {
		if !strings.Contains(src, needle) {
			t.Fatalf("missing %s", needle)
		}
	}
}

func TestClassifyRWErr_UnfinishedUnlessRow(t *testing.T) {
	open := classifyRWErr(mssql.Error{Message: "the batch failed because of an open tran", Number: 50000})
	if open.Precondition || open.Finished || !open.Sent {
		t.Fatalf("foreign open tran %+v", open)
	}
	marked := classifyRWErr(mssql.Error{Message: precheckMarker, Number: 50000})
	if !marked.Precondition || marked.Finished || !marked.Sent {
		t.Fatalf("marker %+v", marked)
	}
	prior := classifyRWErr(mssql.Error{
		Message: precheckMarker, Number: 50000,
		All: []mssql.Error{{Message: "open tran", Number: 50000}, {Message: precheckMarker, Number: 50000}},
	})
	if prior.Precondition || prior.Finished {
		t.Fatalf("prior message %+v", prior)
	}
	other := classifyRWErr(mssql.Error{Message: "connection reset", Number: 11})
	if other.Finished || other.Precondition || !other.Sent {
		t.Fatalf("server error %+v", other)
	}
	none := classifyRWErr(sql.ErrNoRows)
	if none.Finished || none.HasRow {
		t.Fatalf("no row %+v", none)
	}
	if other.Err == nil {
		t.Fatal("err dropped")
	}
}

func TestExecAddVisit_FinalShape(t *testing.T) {
	t.Run("foreign open tran", func(t *testing.T) {
		res := runAdd(t, func(mock sqlmock.Sqlmock) {
			mock.ExpectQuery("pl_AddVisitContext").WillReturnError(mssql.Error{Number: 50000, Message: "open tran"})
		})
		if res.Precondition || res.Finished {
			t.Fatalf("%+v", res)
		}
	})
	t.Run("wrong columns", func(t *testing.T) {
		res := runAdd(t, func(mock sqlmock.Sqlmock) {
			rows := sqlmock.NewRows([]string{"RESULT", "PLANNING_ID", "STATE"}).AddRow("ok", int64(1), int64(0))
			mock.ExpectQuery("pl_AddVisitContext").WillReturnRows(rows)
		})
		if res.Finished || res.Precondition {
			t.Fatalf("%+v", res)
		}
	})
	t.Run("two rows", func(t *testing.T) {
		res := runAdd(t, func(mock sqlmock.Sqlmock) {
			rows := sqlmock.NewRows([]string{"PLANNING_ID", "RESULT", "STATE"}).
				AddRow(int64(1), "ok", int64(0)).
				AddRow(int64(2), "ok", int64(0))
			mock.ExpectQuery("pl_AddVisitContext").WillReturnRows(rows)
		})
		if res.Finished {
			t.Fatalf("%+v", res)
		}
	})
	t.Run("extra result set", func(t *testing.T) {
		res := runAdd(t, func(mock sqlmock.Sqlmock) {
			first := sqlmock.NewRows([]string{"PLANNING_ID", "RESULT", "STATE"}).AddRow(int64(1), "ok", int64(0))
			second := sqlmock.NewRows([]string{"PLANNING_ID", "RESULT", "STATE"}).AddRow(int64(2), "x", int64(0))
			mock.ExpectQuery("pl_AddVisitContext").WillReturnRows(first, second)
		})
		if res.Finished {
			t.Fatalf("%+v", res)
		}
	})
}

func TestExecCancelVisit_FinalShape(t *testing.T) {
	p := CancelParams{PatientID: 1, PlanningID: 5, OrgID: 496, MeddepID: 1, AuthorID: 2}
	t.Run("wrong columns", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		rows := sqlmock.NewRows([]string{"STATE"}).AddRow(int64(0))
		mock.ExpectQuery("pl_CancelVisit").WillReturnRows(rows)
		res := ExecCancelVisit(context.Background(), db, p)
		if res.Finished {
			t.Fatalf("%+v", res)
		}
	})
	t.Run("two rows", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		rows := sqlmock.NewRows([]string{"RESULT"}).AddRow("ok").AddRow("again")
		mock.ExpectQuery("pl_CancelVisit").WillReturnRows(rows)
		res := ExecCancelVisit(context.Background(), db, p)
		if res.Finished {
			t.Fatalf("%+v", res)
		}
	})
	t.Run("extra result set", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		first := sqlmock.NewRows([]string{"RESULT"}).AddRow("ok")
		second := sqlmock.NewRows([]string{"RESULT"}).AddRow("more")
		mock.ExpectQuery("pl_CancelVisit").WillReturnRows(first, second)
		res := ExecCancelVisit(context.Background(), db, p)
		if res.Finished {
			t.Fatalf("%+v", res)
		}
	})
}

func runAdd(t *testing.T, setup func(sqlmock.Sqlmock)) ProcResult {
	t.Helper()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	setup(mock)
	return ExecAddVisit(context.Background(), db, AddVisitParams{PatientID: 1, PlSubjID: 1119, PlExamID: 10000688, VisitStart: "2026-11-08T10:00:00", OrgID: 496, MeddepID: 1, AuthorID: 2})
}
