package handler

import (
	"strings"
	"testing"
)

func TestDisplayDoctorName_StripsPhoneFromLine(t *testing.T) {
	got := displayDoctorName("Иванов И.И. - 8-925-514-41-48")
	if strings.ContainsAny(got, "0123456789") {
		t.Fatalf("phone leaked in doctor_name: %q", got)
	}
	if !strings.HasPrefix(got, "Иванов") {
		t.Fatalf("got %q", got)
	}
}
