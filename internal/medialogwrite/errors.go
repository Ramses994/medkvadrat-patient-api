package medialogwrite

import "errors"

// ErrWriteNotConfigured is returned when DB_RW_USER is not configured.
var ErrWriteNotConfigured = errors.New("WRITE_NOT_CONFIGURED")

// ErrNoPlanning is returned when MOTCONSU has no PLANNING_ID (cancel not supported).
var ErrNoPlanning = errors.New("no planning")
