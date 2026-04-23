package otp

import (
	"context"
)

type Patient struct {
	PatientID   int64
	FullName    string
	Email       string
	MaskedEmail string
	Phone       string
}

type Channel interface {
	Send(ctx context.Context, patient Patient, code string) error
	Name() string
}
