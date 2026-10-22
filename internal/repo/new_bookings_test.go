package repo

import (
	"os"
	"strings"
	"testing"
)

func TestRequirePatientIDList_Limits(t *testing.T) {
	ids := make([]int64, 500)
	for i := range ids {
		ids[i] = int64(i + 1)
	}
	if _, err := RequirePatientIDList(ids, 500); err != nil {
		t.Fatal(err)
	}
	ids = append(ids, 501)
	if _, err := RequirePatientIDList(ids, 500); err == nil {
		t.Fatal("expected too many")
	}
	if _, err := RequirePatientIDList(nil, 500); err == nil {
		t.Fatal("expected empty")
	}
}

func TestNewBookingsQuery_KeepsMillisecondsAndTieBreak(t *testing.T) {
	b, err := os.ReadFile("new_bookings.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	if !strings.Contains(src, "CONVERT(varchar(23), p.CREATE_DATE_TIME, 121)") {
		t.Fatal("need style 121 milliseconds")
	}
	if !strings.Contains(src, "PLANNING_ID > @pid") {
		t.Fatal("need planning_id tie-break")
	}
	if !strings.Contains(src, "ORDER BY p.CREATE_DATE_TIME, p.PLANNING_ID") {
		t.Fatal("need stable order")
	}
}
