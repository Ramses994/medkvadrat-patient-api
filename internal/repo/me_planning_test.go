package repo

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestPlanningVisitState(t *testing.T) {
	loc := time.FixedZone("MSK", 3*3600)
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, loc)
	if got := PlanningVisitState(0, 0, "2026-10-22 15:00", now); got != "scheduled" {
		t.Fatalf("future: %s", got)
	}
	if got := PlanningVisitState(1, 0, "2026-10-22 15:00", now); got != "warned_absent" {
		t.Fatalf("cancelled: %s", got)
	}
	if got := PlanningVisitState(0, 1, "2026-10-01 09:00", now); got != "attended" {
		t.Fatalf("arrived: %s", got)
	}
	if got := PlanningVisitState(0, 0, "2026-10-01 09:00", now); got != "not_marked" {
		t.Fatalf("past unmarked: %s", got)
	}
}

func TestPlanningBranch(t *testing.T) {
	name, addr, ok := PlanningBranch(3, "Куркино")
	if !ok || name != "Куркино" || addr == nil || *addr != "г. Москва, ул. Ландышевая, 14к1" {
		t.Fatalf("known: %q %v %v", name, addr, ok)
	}
	name, addr, ok = PlanningBranch(999, "Филиал X")
	if !ok || name != "Филиал X" || addr != nil {
		t.Fatalf("unknown: %q %v", name, addr)
	}
	if _, _, ok = PlanningBranch(0, ""); ok {
		t.Fatal("missing branch must be absent")
	}
}

func TestPlanningSQL_NoSubjectName_StringNow(t *testing.T) {
	b, err := os.ReadFile("me_planning.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	if strings.Contains(src, "PL_SUBJ.NAME") || strings.Contains(src, "ps.NAME") {
		t.Fatal("PL_SUBJ.NAME must not be selected")
	}
	if !strings.Contains(src, "CONVERT(datetime, @now, 126)") {
		t.Fatal("now bound must be CONVERT style 126")
	}
	if strings.Contains(src, `sql.Named("now", now)`) {
		t.Fatal("@now must be a wall-clock string, not time.Time")
	}
	if !strings.Contains(src, "m.PATRONYME") {
		t.Fatal("doctor name includes patronymic")
	}
	if SQLLooksMutating(src) {
		t.Fatal("planning read SQL must be SELECT-only")
	}
}
