package repo

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
)

const pairDriverName = "booking_pair_fixture"

var (
	pairEmpty bool
	pairVals  []driver.Value
)

type pairDrv struct{}
type pairConn struct{}
type pairRows struct {
	done bool
}
type pairEmptyRows struct{}

func (pairDrv) Open(string) (driver.Conn, error)     { return pairConn{}, nil }
func (pairConn) Prepare(string) (driver.Stmt, error) { return nil, errors.New("unused") }
func (pairConn) Close() error                        { return nil }
func (pairConn) Begin() (driver.Tx, error)           { return nil, errors.New("unused") }
func (pairConn) QueryContext(context.Context, string, []driver.NamedValue) (driver.Rows, error) {
	if pairEmpty {
		return pairEmptyRows{}, nil
	}
	return &pairRows{}, nil
}
func (pairEmptyRows) Columns() []string         { return pairCols }
func (pairEmptyRows) Close() error              { return nil }
func (pairEmptyRows) Next([]driver.Value) error { return io.EOF }
func (r *pairRows) Columns() []string           { return pairCols }
func (r *pairRows) Close() error                { return nil }
func (r *pairRows) Next(dest []driver.Value) error {
	if r.done {
		return io.EOF
	}
	r.done = true
	copy(dest, pairVals)
	return nil
}

var pairCols = []string{
	"web", "archive", "accept", "branch", "code", "med", "doctor",
	"on_line", "exam_web", "tele", "exam_arch", "duree", "web_name", "name",
}

func init() { sql.Register(pairDriverName, pairDrv{}) }

func activePairVals() []driver.Value {
	return []driver.Value{
		int64(1), int64(0), nil, int64(496), "Куркино 2", int64(160), "Тестовый Врач",
		int64(1), int64(1), int64(0), int64(0), "30", "#rus#Тест", "ТЕСТ ЛК",
	}
}

func TestLoadBookingPair_EachInactiveReason(t *testing.T) {
	db, err := sql.Open(pairDriverName, "")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	pairEmpty = false
	pairVals = activePairVals()
	got, err := LoadBookingPair(context.Background(), db, 1119, 10000688)
	if err != nil {
		t.Fatal(err)
	}
	if got.DurationMin != 30 || got.BranchID != 496 || got.Service != "Тест" || got.DoctorName != "Тестовый Врач" {
		t.Fatalf("active = %+v", got)
	}

	cases := []struct {
		name   string
		mutate func([]driver.Value)
		empty  bool
		reason string
	}{
		{"line_not_found", nil, true, "line_not_found"},
		{"web_access", func(v []driver.Value) { v[0] = int64(0) }, false, "web_access"},
		{"archive", func(v []driver.Value) { v[1] = int64(1) }, false, "archive"},
		{"web_accept", func(v []driver.Value) { v[2] = int64(1) }, false, "web_accept"},
		{"web_accept_2", func(v []driver.Value) { v[2] = int64(2) }, false, "web_accept"},
		{"exam_not_on_line", func(v []driver.Value) { v[7] = int64(0) }, false, "exam_not_on_line"},
		{"waitlist_exam", nil, false, "waitlist_exam"},
		{"exam_not_web", func(v []driver.Value) { v[8] = int64(0); v[9] = int64(0) }, false, "exam_not_web"},
		{"exam_tele_ok", func(v []driver.Value) { v[8] = int64(0); v[9] = int64(1) }, false, ""},
		{"exam_archive", func(v []driver.Value) { v[10] = int64(1) }, false, "exam_archive"},
		{"duration", func(v []driver.Value) { v[11] = "bad" }, false, "duration"},
		{"branch", func(v []driver.Value) { v[3] = int64(7) }, false, "branch"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pairEmpty = tc.empty
			pairVals = activePairVals()
			exam := int64(10000688)
			if tc.name == "waitlist_exam" {
				exam = 3118
			}
			if tc.mutate != nil {
				tc.mutate(pairVals)
			}
			_, err := LoadBookingPair(context.Background(), db, 1119, exam)
			if tc.reason == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			var inactive InactivePairError
			if !errors.As(err, &inactive) || inactive.Reason != tc.reason {
				t.Fatalf("err = %v, want %s", err, tc.reason)
			}
		})
	}
}

func TestBookingPairSQL_ReadOnly(t *testing.T) {
	b, err := os.ReadFile("booking_pairs.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	if strings.Contains(src, "PL_SUBJ.NAME") || strings.Contains(src, "ps.NAME") {
		t.Fatal("line name must not be selected")
	}
	if SQLLooksMutating(src) {
		t.Fatal("pair SQL must be read-only")
	}
}
