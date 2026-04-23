package otp

import (
	"context"
	"log/slog"
)

type DevChannel struct {
	Logger *slog.Logger
}

func (c DevChannel) Name() string { return "dev" }

func (c DevChannel) Send(ctx context.Context, patient Patient, code string) error {
	c.Logger.Info("otp dev send", "patient_id", patient.PatientID, "masked_email", patient.MaskedEmail, "code", code)
	return nil
}
