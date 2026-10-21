package handler

import "testing"

func TestDisplayDoctorName_EmptyWhenNoDoctor(t *testing.T) {
	if got := displayDoctorName(""); got != "" {
		t.Fatalf("got %q", got)
	}
}
