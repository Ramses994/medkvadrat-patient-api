package repo

import "testing"

func TestApptVersion_ChangesWithStartLineOrExam(t *testing.T) {
	base := ApptVersion("2026-06-26 19:30:00", 1, 2)
	if ApptVersion("2026-06-27 10:00:00", 1, 2) == base {
		t.Fatal("DATE_START change must change version")
	}
	if ApptVersion("2026-06-26 19:30:00", 9, 2) == base {
		t.Fatal("PL_SUBJ_ID change must change version")
	}
	if ApptVersion("2026-06-26 19:30:00", 1, 8) == base {
		t.Fatal("PL_EXAM_ID change must change version")
	}
	if len(base) != 12 {
		t.Fatalf("len=%d", len(base))
	}
}
