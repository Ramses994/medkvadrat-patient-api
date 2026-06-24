package repo

import (
	"strings"
	"testing"
	"time"
)

func TestParseConsultationWallClock(t *testing.T) {
	tm, err := ParseConsultationWallClock("2026-06-26 19:30")
	if err != nil {
		t.Fatal(err)
	}
	if tm.Location().String() != moscowLocation().String() {
		t.Fatalf("location=%v", tm.Location())
	}
	if tm.Hour() != 19 || tm.Minute() != 30 {
		t.Fatalf("got %s", tm.Format("15:04"))
	}
	if tm.Format("2006-01-02 15:04") != "2026-06-26 19:30" {
		t.Fatalf("formatted=%s", tm.Format("2006-01-02 15:04"))
	}
}

func TestParseConsultationWallClock_TruncatesSeconds(t *testing.T) {
	tm, err := ParseConsultationWallClock("2026-06-26 19:30:00")
	if err != nil {
		t.Fatal(err)
	}
	if tm.Hour() != 19 || tm.Minute() != 30 {
		t.Fatalf("got %s", tm.Format("15:04"))
	}
}

func TestParseConsultationWallClock_NotShiftedToUTC(t *testing.T) {
	tm, err := ParseConsultationWallClock("2026-06-26 19:30")
	if err != nil {
		t.Fatal(err)
	}
	// Must not become 22:30 when formatted for API output.
	if tm.In(time.UTC).Hour() == 22 {
		t.Fatalf("wall-clock was shifted: %v", tm)
	}
}

func TestParsePatientIDsCSV(t *testing.T) {
	ids, err := ParsePatientIDsCSV("1,1587578")
	if err != nil || len(ids) != 2 || ids[0] != 1 || ids[1] != 1587578 {
		t.Fatalf("got %v err=%v", ids, err)
	}
}

func TestParsePatientIDsCSV_Empty(t *testing.T) {
	for _, in := range []string{"", "  ", ",,"} {
		ids, err := ParsePatientIDsCSV(in)
		if err != nil || ids != nil {
			t.Fatalf("%q: ids=%v err=%v", in, ids, err)
		}
	}
}

func TestParsePatientIDsCSV_Invalid(t *testing.T) {
	for _, in := range []string{"abc", "1,x", "0", "-5"} {
		if _, err := ParsePatientIDsCSV(in); err == nil {
			t.Fatalf("expected error for %q", in)
		}
	}
}

func TestPatientFilterClause_NoInjection(t *testing.T) {
	clause, args := patientFilterClause([]int64{1, 1587578})
	if strings.Contains(clause, "1587578") {
		t.Fatalf("ids must not appear in SQL text: %s", clause)
	}
	if len(args) != 3 {
		t.Fatalf("args=%d", len(args))
	}
}

func TestPatientFilterClause_Empty(t *testing.T) {
	clause, args := patientFilterClause(nil)
	if !strings.Contains(clause, "@hasPatients") {
		t.Fatalf("clause=%s", clause)
	}
	if len(args) != 1 {
		t.Fatalf("args=%d", len(args))
	}
}
