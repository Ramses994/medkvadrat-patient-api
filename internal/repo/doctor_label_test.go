package repo

import (
	"strings"
	"testing"
)

func TestSanitizeScheduleLabel(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"Иванов И.И. - 8-925-514-41-48", "Иванов И.И."},
		{"Холодов Сергей Евгеньевич - 8-925-111-22-33", "Холодов Сергей Евгеньевич"},
		{"Гаевой Эдуард - приём на Воротынской!", "Гаевой Эдуард"},
		{"Кабина 1", "Кабина 1"},
		{"Процедурный кабинет", "Процедурный кабинет"},
		{"Кабина 1 8-925-514-41-48", "Кабина 1"},
		{"  ", ""},
	}
	for _, tc := range cases {
		if got := SanitizeScheduleLabel(tc.in); got != tc.want {
			t.Errorf("SanitizeScheduleLabel(%q)=%q want %q", tc.in, got, tc.want)
		}
	}
}

func TestDoctorNameSQL_UsesMedecinsWhenPresent(t *testing.T) {
	if !strings.Contains(doctorNameSelectSQL, "MEDECINS") ||
		!strings.Contains(doctorNameSelectSQL, "NOM") ||
		!strings.Contains(doctorNameSelectSQL, "PRENOM") ||
		!strings.Contains(doctorNameSelectSQL, "ps.NAME") {
		t.Fatalf("doctor name SQL must prefer MEDECINS and fall back to PL_SUBJ.NAME")
	}
}
