package repo

import (
	"strings"
	"testing"
)

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
