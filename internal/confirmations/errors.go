package confirmations

import "errors"

var (
	ErrAppointmentNotFound = errors.New("APPOINTMENT_NOT_FOUND")
	ErrPatientMismatch     = errors.New("PATIENT_MISMATCH")
	ErrAlreadyAnswered     = errors.New("ALREADY_ANSWERED")
)

// AlreadyAnsweredError carries the stored status when a repeat answer is rejected.
type AlreadyAnsweredError struct {
	Record Record
}

func (e *AlreadyAnsweredError) Error() string { return ErrAlreadyAnswered.Error() }

func (e *AlreadyAnsweredError) Unwrap() error { return ErrAlreadyAnswered }
