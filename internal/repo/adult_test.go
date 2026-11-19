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

// TestFilterAdultOTPPatients_MoscowMidnightUTC: UTC clock across Moscow midnight on the 18th birthday.
func TestFilterAdultOTPPatients_MoscowMidnightUTC(t *testing.T) {
	birth := time.Date(2008, 11, 19, 0, 0, 0, 0, time.UTC)
	pats := []OTPPatient{{PatientID: 1, BirthDate: &birth}}

	// 21:00Z on the eve = 00:00 MSK on the birthday → already 18.
	atMidnight := time.Date(2026, 11, 18, 21, 0, 0, 0, time.UTC)
	if got := FilterAdultOTPPatients(pats, atMidnight); len(got) != 1 {
		t.Fatalf("21:00Z: want adult, got %d", len(got))
	}

	// 20:59Z = 23:59 MSK previous calendar day → still 17.
	beforeMidnight := time.Date(2026, 11, 18, 20, 59, 0, 0, time.UTC)
	if got := FilterAdultOTPPatients(pats, beforeMidnight); len(got) != 0 {
		t.Fatalf("20:59Z: want child, got %d", len(got))
	}
}
