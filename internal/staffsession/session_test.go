package staffsession

import (
	"testing"
	"time"
)

func TestSignValid(t *testing.T) {
	secret := []byte("0123456789abcdef0123456789abcdef")
	now := time.Date(2026, 6, 25, 12, 0, 0, 0, time.UTC)
	exp := now.Add(12 * time.Hour)
	val, err := Sign(secret, exp)
	if err != nil {
		t.Fatal(err)
	}
	ok, err := Valid(secret, val, now)
	if err != nil || !ok {
		t.Fatal("expected valid")
	}
	ok, err = Valid(secret, val, exp.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("expected expired")
	}
}

func TestValidTampered(t *testing.T) {
	secret := []byte("0123456789abcdef0123456789abcdef")
	now := time.Now().UTC()
	val, err := Sign(secret, now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	// flip last hex nibble
	bad := val[:len(val)-1] + "0"
	if bad == val {
		bad = val[:len(val)-1] + "1"
	}
	if ok, _ := Valid(secret, bad, now); ok {
		t.Fatal("tampered cookie must fail")
	}
	if ok, _ := Valid([]byte("fedcba9876543210fedcba9876543210"), val, now); ok {
		t.Fatal("wrong secret must fail")
	}
	if ok, _ := Valid(secret, "not-a-cookie", now); ok {
		t.Fatal("garbage must fail")
	}
}

func TestSignValid_WeakSecret(t *testing.T) {
	now := time.Now().UTC()
	if _, err := Sign(nil, now.Add(time.Hour)); err != ErrWeakSecret {
		t.Fatalf("empty secret: %v", err)
	}
	if _, err := Sign([]byte("short"), now.Add(time.Hour)); err != ErrWeakSecret {
		t.Fatalf("short secret: %v", err)
	}
	ok, err := Valid(nil, "1|ab", now)
	if ok || err != ErrWeakSecret {
		t.Fatalf("Valid empty secret ok=%v err=%v", ok, err)
	}
	ok, err = Valid([]byte("x"), "1|ab", now)
	if ok || err != ErrWeakSecret {
		t.Fatalf("Valid short secret ok=%v err=%v", ok, err)
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

func TestUpdatedBy(t *testing.T) {
	if got := UpdatedBy("123|abcdef12ffff"); got != "staffabcdef12" {
		t.Fatalf("got %q", got)
	}
	if got := UpdatedBy("bad"); got != "staff" {
		t.Fatalf("got %q", got)
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
