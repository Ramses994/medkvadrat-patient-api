package slottoken

import (
	"encoding/base64"
	"strings"
	"testing"
	"time"
)

const testSecret = "0123456789abcdef0123456789abcdef"

func sample(now time.Time) Payload {
	return Payload{
		PatientID: 1, PlSubjID: 1119, PlExamID: 10000688,
		Start: "2026-11-06T23:30:00", DurationMin: 30, BranchID: 496,
	}
}

func TestSlotToken_AcceptsExactClaim(t *testing.T) {
	now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	tok, err := Issue(testSecret, sample(now), now)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Verify(testSecret, tok, now.Add(4*time.Minute), 1, 1119, 10000688)
	if err != nil {
		t.Fatal(err)
	}
	if got.Start != "2026-11-06T23:30:00" || got.DurationMin != 30 {
		t.Fatalf("%+v", got)
	}
}

func TestSlotToken_Rejects(t *testing.T) {
	now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	tok, err := Issue(testSecret, sample(now), now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Issue("short", sample(now), now); err != ErrSecret {
		t.Fatalf("short secret: %v", err)
	}
	if _, err := Verify(testSecret, tok, now.Add(6*time.Minute), 1, 1119, 10000688); err != ErrExpired {
		t.Fatalf("expired: %v", err)
	}
	if _, err := Verify(testSecret, tok, now, 2, 1119, 10000688); err != ErrPatient {
		t.Fatalf("patient: %v", err)
	}
	if _, err := Verify(testSecret, tok, now, 1, 1119, 10000689); err != ErrPair {
		t.Fatalf("exam: %v", err)
	}
	body, sig, _ := strings.Cut(tok, ".")
	raw, err := base64.RawURLEncoding.DecodeString(sig)
	if err != nil {
		t.Fatal(err)
	}
	raw[0] ^= 0x01
	forged := body + "." + base64.RawURLEncoding.EncodeToString(raw)
	if _, err := Verify(testSecret, forged, now, 1, 1119, 10000688); err != ErrToken {
		t.Fatalf("forged: %v", err)
	}
}
