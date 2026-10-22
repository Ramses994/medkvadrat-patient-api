package repo

import "strings"

// doctorNameSelectSQL is MEDECINS.NOM + PRENOM when the line has a doctor.
// Otherwise empty — never PL_SUBJ.NAME (phones and notes live there).
const doctorNameSelectSQL = `
LTRIM(RTRIM(CASE
  WHEN ps.MEDECINS_ID IS NOT NULL THEN ISNULL((
    SELECT ISNULL(mdc.NOM, '') + ' ' + ISNULL(mdc.PRENOM, '')
    FROM MEDECINS mdc
    WHERE mdc.MEDECINS_ID = ps.MEDECINS_ID
  ), '')
  ELSE ''
END))`

func trimDoctorName(s string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(s)), " ")
}
