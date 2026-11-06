package repo

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestMotconsuQueriesUseDatetimeString(t *testing.T) {
	b, err := os.ReadFile("motconsu.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	for _, want := range []string{
		"CONVERT(datetime2(7), @since, 126)",
		"CONVERT(datetime2(7), @lastDate, 126)",
		"mssqlCursorBound(since)",
		"mssqlCursorBound(last)",
	} {
		if !strings.Contains(src, want) {
			t.Fatalf("missing %s", want)
		}
	}
	for _, gone := range []string{
		`sql.Named("since", since)`,
		`sql.Named("lastDate", last)`,
	} {
		if strings.Contains(src, gone) {
			t.Fatalf("still passes time.Time: %s", gone)
		}
	}
}

func TestMotconsuBoundKeepsWallClockDigits(t *testing.T) {
	// schedule since и scan KRN_MODIFY_DATE несут цифры naive datetime с меткой UTC.
	utc := time.Date(2026, 11, 5, 12, 30, 15, 0, time.UTC)
	got := utc.Format(mssqlReminderBoundLayout)
	if got != "2026-11-05T12:30:15" {
		t.Fatalf("bound = %s", got)
	}
	shifted := utc.In(moscowLocation()).Format(mssqlReminderBoundLayout)
	if shifted == got {
		t.Fatal("Moscow conversion must differ; the query must not apply it")
	}
	cursor := mssqlCursorBound(utc)
	if cursor != "2026-11-05T12:30:15.0000000" {
		t.Fatalf("cursor = %s", cursor)
	}
	if mssqlCursorBound(utc.In(moscowLocation())) == cursor {
		t.Fatal("cursor must keep the value's own digits")
	}
}

func TestMSSQLCursorLayoutKeepsDatetimeTick(t *testing.T) {
	tick := time.Date(2026, 11, 6, 12, 30, 15, 123333300, time.UTC)
	if got := mssqlCursorBound(tick); got != "2026-11-06T12:30:15.1233333" {
		t.Fatalf("datetime tick lost: %s", got)
	}
	ms := time.Date(2026, 11, 6, 12, 30, 15, 123*int(time.Millisecond), time.UTC)
	if got := mssqlCursorBound(ms); got != "2026-11-06T12:30:15.1230000" {
		t.Fatalf("cursor = %s", got)
	}
}
