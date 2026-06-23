package confirmations

import "testing"

func TestValidStatus(t *testing.T) {
	for _, s := range []string{"confirmed", "declined", "reschedule", "CONFIRMED"} {
		if !ValidStatus(s) {
			t.Fatalf("expected valid: %q", s)
		}
	}
	for _, s := range []string{"", "yes", "cancelled", "pending"} {
		if ValidStatus(s) {
			t.Fatalf("expected invalid: %q", s)
		}
	}
}

func TestNormalizeStatus(t *testing.T) {
	if got := NormalizeStatus(" Reschedule "); got != "reschedule" {
		t.Fatalf("got %q", got)
	}
}
