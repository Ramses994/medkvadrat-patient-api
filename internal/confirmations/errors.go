package confirmations

import "errors"

var (
	ErrAppointmentNotFound = errors.New("APPOINTMENT_NOT_FOUND")
	ErrPatientMismatch     = errors.New("PATIENT_MISMATCH")
)
