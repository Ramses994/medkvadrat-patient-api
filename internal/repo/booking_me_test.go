package repo

import (
	"database/sql"
	"testing"
)

func TestSlotBookableForPatient(t *testing.T) {
	if err := slotBookableForPatient(sql.NullInt64{}, 1); err == nil || err.Error() != "SLOT_NOT_FREE" {
		t.Fatalf("null patient: %v", err)
	}
	if err := slotBookableForPatient(sql.NullInt64{Int64: 2, Valid: true}, 1); err == nil || err.Error() != "SLOT_TAKEN" {
		t.Fatalf("other patient: %v", err)
	}
	if err := slotBookableForPatient(sql.NullInt64{Int64: 1, Valid: true}, 1); err != nil {
		t.Fatalf("same patient: %v", err)
	}
}
