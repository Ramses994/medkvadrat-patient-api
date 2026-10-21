package repo

import (
	"strings"
	"testing"
)

func TestDoctorNameSQL_UsesMedecinsNeverLineName(t *testing.T) {
	if !strings.Contains(doctorNameSelectSQL, "MEDECINS") ||
		!strings.Contains(doctorNameSelectSQL, "NOM") {
		t.Fatal("doctor name SQL must use MEDECINS")
	}
	if strings.Contains(doctorNameSelectSQL, "ps.NAME") {
		t.Fatal("doctor name SQL must not fall back to PL_SUBJ.NAME")
	}
}
