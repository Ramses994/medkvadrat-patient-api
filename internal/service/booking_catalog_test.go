package service

import "testing"

func TestDateSpan_FourteenDays(t *testing.T) {
	days, err := dateSpan("2026-11-06", "2026-11-19", 14)
	if err != nil || len(days) != 14 {
		t.Fatalf("14 days: %v %d", err, len(days))
	}
	if _, err := dateSpan("2026-11-06", "2026-11-20", 14); err == nil {
		t.Fatal("15 days must be rejected")
	}
}
