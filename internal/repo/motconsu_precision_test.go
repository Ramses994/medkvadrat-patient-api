package repo

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

// Transferred from the 2026-11-06 audit fixture motconsu-precision/poll_precision_test.go.
// The fake driver returns the cursor row only when the bound is strictly earlier, which is
// what KRN_MODIFY_DATE > bound does once the fraction survives.

const precisionDriver = "audit_motconsu_precision"

var (
	capturedQuery string
	capturedBound driver.Value
)

var precisionRowAt = time.Date(2026, 11, 6, 12, 30, 15, 123*int(time.Millisecond), time.UTC)

type precisionDriverImpl struct{}
type precisionConn struct{}
type precisionEmptyRows struct{}
type precisionRows struct {
	modify time.Time
	done   bool
}

func (precisionDriverImpl) Open(string) (driver.Conn, error) { return precisionConn{}, nil }
func (precisionConn) Prepare(string) (driver.Stmt, error)    { return nil, errors.New("unused") }
func (precisionConn) Close() error                           { return nil }
func (precisionConn) Begin() (driver.Tx, error)              { return nil, errors.New("unused") }

func (precisionConn) QueryContext(_ context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	capturedQuery = query
	if len(args) == 0 {
		return nil, errors.New("no args")
	}
	capturedBound = args[0].Value
	s, ok := args[0].Value.(string)
	if !ok {
		return &precisionRows{modify: precisionRowAt}, nil
	}
	parsed, err := time.Parse(mssqlCursorLayout, s)
	if err != nil || parsed.Before(precisionRowAt) {
		return &precisionRows{modify: precisionRowAt}, nil
	}
	return precisionEmptyRows{}, nil
}

func (precisionEmptyRows) Columns() []string {
	return []string{"MOTCONSU_ID", "PATIENTS_ID", "MEDECINS_ID", "DATE_CONSULTATION", "KRN_MODIFY_DATE"}
}
func (precisionEmptyRows) Close() error              { return nil }
func (precisionEmptyRows) Next([]driver.Value) error { return io.EOF }

func (r *precisionRows) Columns() []string { return precisionEmptyRows{}.Columns() }
func (r *precisionRows) Close() error      { return nil }
func (r *precisionRows) Next(dest []driver.Value) error {
	if r.done {
		return io.EOF
	}
	r.done = true
	dest[0] = int64(1)
	dest[1] = int64(2)
	dest[2] = int64(3)
	dest[3] = r.modify
	dest[4] = r.modify
	return nil
}

func init() { sql.Register(precisionDriver, precisionDriverImpl{}) }

func TestPollAfterPreservesSubsecondCursor(t *testing.T) {
	db, err := sql.Open(precisionDriver, "")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	cursor := precisionRowAt
	rows, err := New(db).Motconsu.PollAfter(context.Background(), cursor)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Fatalf("cursor row returned again: %+v", rows)
	}
	bound, ok := capturedBound.(string)
	if !ok {
		t.Fatalf("bound type=%T, expected wall-clock string", capturedBound)
	}
	if bound != "2026-11-06T12:30:15.1230000" {
		t.Fatalf("bound = %s", bound)
	}
	parsed, err := time.Parse("2006-01-02T15:04:05.999999999", bound)
	if err != nil {
		t.Fatal(err)
	}
	if !parsed.Equal(cursor) {
		t.Fatalf("cursor precision lost: input=%s SQL bound=%s; the cursor row itself qualifies for KRN_MODIFY_DATE > bound again", cursor.Format("2006-01-02T15:04:05.000"), bound)
	}
	if !strings.Contains(capturedQuery, "CONVERT(datetime2(7), @lastDate, 126)") {
		t.Fatalf("query = %s", capturedQuery)
	}

	earlier := cursor.Add(-time.Millisecond)
	rows, err = New(db).Motconsu.PollAfter(context.Background(), earlier)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || !rows[0].ModifyDate.Equal(precisionRowAt) {
		t.Fatalf("earlier cursor should see the row, got %+v", rows)
	}

	if _, err = New(db).Motconsu.ChangesSince(context.Background(), cursor); err != nil {
		t.Fatal(err)
	}
	if capturedBound != "2026-11-06T12:30:15.1230000" {
		t.Fatalf("since bound = %v", capturedBound)
	}
	if !strings.Contains(capturedQuery, "CONVERT(datetime2(7), @since, 126)") {
		t.Fatalf("changes query = %s", capturedQuery)
	}
}
