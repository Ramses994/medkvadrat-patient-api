package repo

import (
	"testing"
	"time"
)

func TestIsAdult(t *testing.T) {
	loc := moscowLocation()
	today := time.Date(2026, 11, 18, 15, 0, 0, 0, loc)
	day := func(y int, m time.Month, d int) *time.Time {
		tt := time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
		return &tt
	}
	if IsAdult(nil, today) {
		t.Fatal("nil birth must hide")
	}
	if !IsAdult(day(2000, 1, 1), today) {
		t.Fatal("adult")
	}
	if IsAdult(day(2015, 1, 1), today) {
		t.Fatal("child")
	}
	if !IsAdult(day(2008, 11, 18), today) {
		t.Fatal("exactly 18 today")
	}
	if IsAdult(day(2008, 11, 19), today) {
		t.Fatal("turns 18 tomorrow")
	}
}
