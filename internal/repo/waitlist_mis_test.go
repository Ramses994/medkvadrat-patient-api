package repo

import (
	"strings"
	"testing"
	"time"
)

func TestWaitlistMisSQL_FiltersExamWindowAndActive(t *testing.T) {
	q, args := waitlistMisSQL([]int64{3118})
	for _, want := range []string{
		"p.PL_EXAM_ID IN",
		"ISNULL(p.CANCELLED, 0) = 0",
		"ISNULL(p.STATUS, 0) = 0",
		"CONVERT(datetime, @misFrom, 126)",
		"CONVERT(datetime, @misTo, 126)",
		"CONVERT(varchar(19), p.DATE_START, 126)",
		"CONVERT(varchar(19), p.CREATE_DATE_TIME, 126)",
		"pt.NE_LE",
		"p.COMMENTAIRE",
		"MEDECINS_CREATOR_ID",
		"FM_INTORG_ID IN (106, 3, 496)",
		"SUBSTRING(ISNULL(p.COMMENTAIRE, ''), 1, 200)",
	} {
		if !strings.Contains(q, want) {
			t.Fatalf("sql missing %s", want)
		}
	}
	if len(args) != 1 {
		t.Fatalf("exam args: %d", len(args))
	}
	if strings.Contains(strings.ToUpper(q), "INSERT") || strings.Contains(strings.ToUpper(q), "UPDATE") || strings.Contains(strings.ToUpper(q), "DELETE") {
		t.Fatal("registry waitlist query must be read-only")
	}
}

func TestWaitlistMisWindow_SevenBackSixtyAhead(t *testing.T) {
	loc := moscowLocation()
	now := time.Date(2026, 11, 6, 13, 39, 0, 0, loc)
	from, to := WaitlistMisWindow(now, 7, 60)
	if from != "2026-10-30T00:00:00" {
		t.Fatalf("from %s", from)
	}
	if to != "2027-01-06T00:00:00" {
		t.Fatalf("to %s", to)
	}
}
