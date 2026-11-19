package repo

import (
	"strings"
	"testing"
)

func TestDoctorDisplayName(t *testing.T) {
	cases := []struct {
		nom, prenom, patronyme, want string
	}{
		{"Шемет", "Татьяна", "Анатольевна", "Шемет Татьяна Анатольевна"},
		{"Шемет", "Татьяна Анатольевна", "Анатольевна", "Шемет Татьяна Анатольевна"},
		{"Шемет", "Татьяна", "", "Шемет Татьяна"},
		{"Шемет", "", "Анатольевна", "Шемет Анатольевна"},
		{"шемет", "татьяна анатольевна", "АНАТОЛЬЕВНА", "шемет татьяна анатольевна"},
		{"  Шемет  ", "  Татьяна   Ивановна  ", "Ивановна", "Шемет Татьяна Ивановна"},
		{"Гаевой", "Пётр", "Петрович", "Гаевой Пётр Петрович"},
		{"Гаевой", "Петр Петрович", "Пётрович", "Гаевой Петр Петрович"},
		{"", "Татьяна", "Анатольевна", "Татьяна Анатольевна"},
		{"", "", "", ""},
	}
	for _, tc := range cases {
		if got := DoctorDisplayName(tc.nom, tc.prenom, tc.patronyme); got != tc.want {
			t.Fatalf("DoctorDisplayName(%q,%q,%q)=%q want %q", tc.nom, tc.prenom, tc.patronyme, got, tc.want)
		}
	}
}

func TestDoctorNameSQL_UsesMedecinsNeverLineName(t *testing.T) {
	sql := doctorNamePartsExprs("ps")
	if !strings.Contains(sql, "MEDECINS") || !strings.Contains(sql, "NOM") || !strings.Contains(sql, "PATRONYME") {
		t.Fatal("doctor name SQL must use MEDECINS NOM/PRENOM/PATRONYME")
	}
	if strings.Contains(sql, "ps.NAME") {
		t.Fatal("doctor name SQL must not fall back to PL_SUBJ.NAME")
	}
	if strings.Contains(sql, "+ ' ' +") || strings.Contains(sql, "+' '+") {
		t.Fatal("doctor name parts must not be concatenated in SQL")
	}
}
