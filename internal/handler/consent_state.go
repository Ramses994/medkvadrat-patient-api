package handler

import "context"

// ConsentReader loads service-consent state for many patients in one call.
type ConsentReader interface {
	ServiceStates(ctx context.Context, patientIDs []int64) (map[int64]string, error)
}

func consentStates(ctx context.Context, src ConsentReader, patientIDs []int64) (map[int64]string, error) {
	if src == nil || len(patientIDs) == 0 {
		return map[int64]string{}, nil
	}
	return src.ServiceStates(ctx, patientIDs)
}

func consentState(states map[int64]string, patientID int64) string {
	switch states[patientID] {
	case "active", "revoked":
		return states[patientID]
	default:
		return "none"
	}
}
