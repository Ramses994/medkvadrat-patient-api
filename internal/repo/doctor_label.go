package repo

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// DoctorDisplayName builds a doctor label from MEDECINS.NOM / PRENOM / PATRONYME.
// Empty parts are skipped. If PRENOM already ends with PATRONYME (case-insensitive,
// ё/е treated equal), the patronymic is not appended again.
func DoctorDisplayName(nom, prenom, patronyme string) string {
	nom = collapseSpaces(nom)
	prenom = collapseSpaces(prenom)
	patronyme = collapseSpaces(patronyme)
	parts := make([]string, 0, 3)
	if nom != "" {
		parts = append(parts, nom)
	}
	if prenom != "" {
		parts = append(parts, prenom)
	}
	if patronyme != "" && !prenomEndsWithPatronym(prenom, patronyme) {
		parts = append(parts, patronyme)
	}
	return strings.Join(parts, " ")
}

func collapseSpaces(s string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(s)), " ")
}

func prenomEndsWithPatronym(prenom, patronyme string) bool {
	if prenom == "" || patronyme == "" {
		return false
	}
	p := foldYo(strings.ToLower(prenom))
	pat := foldYo(strings.ToLower(patronyme))
	if !strings.HasSuffix(p, pat) {
		return false
	}
	rest := p[:len(p)-len(pat)]
	if rest == "" {
		return true
	}
	r, _ := utf8.DecodeLastRuneInString(rest)
	return unicode.IsSpace(r)
}

func foldYo(s string) string {
	return strings.ReplaceAll(s, "ё", "е")
}

// doctorFieldSQL is one MEDECINS field when the planning line has a doctor.
// subjAlias is the PL_SUBJ table alias (usually "ps").
func doctorFieldSQL(subjAlias, field string) string {
	return fmt.Sprintf(
		`CASE WHEN %s.MEDECINS_ID IS NOT NULL THEN ISNULL((SELECT ISNULL(mdc.%s,'') FROM MEDECINS mdc WHERE mdc.MEDECINS_ID = %s.MEDECINS_ID), '') ELSE '' END`,
		subjAlias, field, subjAlias,
	)
}

// doctorNamePartsExprs returns NOM, PRENOM, PATRONYME as three SELECT expressions (no aliases).
func doctorNamePartsExprs(subjAlias string) string {
	return doctorFieldSQL(subjAlias, "NOM") + ", " +
		doctorFieldSQL(subjAlias, "PRENOM") + ", " +
		doctorFieldSQL(subjAlias, "PATRONYME")
}

// doctorNamePartsAliased returns three aliased columns (prefix+"NOM"/PRENOM/PATRONYME).
func doctorNamePartsAliased(subjAlias, prefix string) string {
	return doctorFieldSQL(subjAlias, "NOM") + " AS " + prefix + "NOM, " +
		doctorFieldSQL(subjAlias, "PRENOM") + " AS " + prefix + "PRENOM, " +
		doctorFieldSQL(subjAlias, "PATRONYME") + " AS " + prefix + "PATRONYME"
}

func trimDoctorName(s string) string {
	return collapseSpaces(s)
}
