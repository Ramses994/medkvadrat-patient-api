package staffsession

import (
	"testing"
	"time"
)

func TestSignValid(t *testing.T) {
	secret := []byte("test-secret-32-bytes-long!!!!!!")
	now := time.Date(2026, 6, 25, 12, 0, 0, 0, time.UTC)
	exp := now.Add(12 * time.Hour)
	val := Sign(secret, exp)
	if !Valid(secret, val, now) {
		t.Fatal("expected valid")
	}
	if Valid(secret, val, exp.Add(time.Second)) {
		t.Fatal("expected expired")
	}
}

func TestValidTampered(t *testing.T) {
	secret := []byte("test-secret-32-bytes-long!!!!!!")
	now := time.Now().UTC()
	val := Sign(secret, now.Add(time.Hour))
	// flip last hex nibble
	bad := val[:len(val)-1] + "0"
	if bad == val {
		bad = val[:len(val)-1] + "1"
	}
	if Valid(secret, bad, now) {
		t.Fatal("tampered cookie must fail")
	}
	if Valid([]byte("other-secret"), val, now) {
		t.Fatal("wrong secret must fail")
	}
	if Valid(secret, "not-a-cookie", now) {
		t.Fatal("garbage must fail")
	}
}

func TestPasswordMatch(t *testing.T) {
	if !PasswordMatch("secret", "secret") {
		t.Fatal("equal")
	}
	if PasswordMatch("secret", "wrong") {
		t.Fatal("mismatch")
	}
	if PasswordMatch("secret", "secre") {
		t.Fatal("different length")
	}
}

func TestClientIP(t *testing.T) {
	if got := ClientIP("192.168.1.5:54321"); got != "192.168.1.5" {
		t.Fatalf("got %q", got)
	}
	if got := ClientIP("[::1]:80"); got != "::1" {
		t.Fatalf("got %q", got)
	}
}
