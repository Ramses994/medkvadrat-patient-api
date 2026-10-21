package repo

import (
	"strings"
)

// doctorNameSelectSQL is the display name for a schedule line (SQL Server 2008).
// If the line has a doctor (PL_SUBJ.MEDECINS_ID): MEDECINS.NOM + PRENOM.
// Otherwise empty — never send PL_SUBJ.NAME (it can contain phones and notes).
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
