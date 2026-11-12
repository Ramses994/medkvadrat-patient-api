package service

import (
	"context"
	"errors"
	"testing"

	"github.com/medkvadrat/medkvadrat-patient-api/internal/apperr"
)

func TestOTPRequest_InvalidPhoneDoesNotQuery(t *testing.T) {
	s := &Services{}
	for _, phone := range []string{"", "abc"} {
		_, err := s.OTPRequest(context.Background(), phone, "127.0.0.1")
		var ae *apperr.AppError
		if !errors.As(err, &ae) || ae.Status != 400 || ae.Code != "VALIDATION" {
			t.Fatalf("phone %q: %v", phone, err)
		}
		if ae.Message != "Введите телефон в формате +7 999 123-45-67" {
			t.Fatalf("phone %q message=%q", phone, ae.Message)
		}
	}
}

func TestPatientSearch_InvalidPhoneDoesNotQuery(t *testing.T) {
	s := &Services{}
	for _, phone := range []string{"", "abc"} {
		_, err := s.PatientSearch(context.Background(), phone)
		var ae *apperr.AppError
		if !errors.As(err, &ae) || ae.Status != 400 || ae.Code != "VALIDATION" {
			t.Fatalf("phone %q: %v", phone, err)
		}
	}
}
