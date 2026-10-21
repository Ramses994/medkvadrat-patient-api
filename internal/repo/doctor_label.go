package repo

import (
	"regexp"
	"strings"
)

// doctorNameSelectSQL is the display name for a schedule line (SQL Server 2008).
// If the line has a doctor (PL_SUBJ.MEDECINS_ID): MEDECINS.NOM + PRENOM.
// Otherwise PL_SUBJ.NAME (rooms); phones and registrar notes are stripped in Go.
const doctorNameSelectSQL = `
LTRIM(RTRIM(CASE
  WHEN ps.MEDECINS_ID IS NOT NULL THEN ISNULL((
    SELECT ISNULL(mdc.NOM, '') + ' ' + ISNULL(mdc.PRENOM, '')
    FROM MEDECINS mdc
    WHERE mdc.MEDECINS_ID = ps.MEDECINS_ID
  ), '')
  ELSE ISNULL(ps.NAME, '')
END))`

// Russian mobile: +7/7/8 then 9 and nine more digits, separators allowed.
var phoneLikeSeq = regexp.MustCompile(`(?:\+?7|8)[\s().\-]*9(?:[\s().\-]*\d){9}`)

// SanitizeScheduleLabel drops registrar notes after " - " and phone-like digit runs.
func SanitizeScheduleLabel(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.Index(s, " - "); i >= 0 {
		s = strings.TrimSpace(s[:i])
	}
	s = phoneLikeSeq.ReplaceAllString(s, " ")
	return strings.Join(strings.Fields(s), " ")
}
