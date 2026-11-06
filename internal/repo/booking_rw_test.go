package repo

import (
	"os"
	"regexp"
	"strings"
	"testing"
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
