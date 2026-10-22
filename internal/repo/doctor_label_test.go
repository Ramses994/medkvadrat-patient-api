package repo

import (
	"strings"
	"testing"
)

func TestDoctorNameSQL_UsesMedecinsNeverLineName(t *testing.T) {
	if !strings.Contains(doctorNameSelectSQL, "MEDECINS") {
		t.Fatal("expected MEDECINS")
	}
	if strings.Contains(doctorNameSelectSQL, "ps.NAME") {
		t.Fatal("must not fall back to PL_SUBJ.NAME")
	}
}
