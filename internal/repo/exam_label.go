package repo

import (
	"regexp"
	"strings"
)

// serviceRawSelectSQL is the exam label from the appointment (SQL Server 2008).
// Prefer PL_EXAM.WEB_NAME; otherwise NAME. Cleaned in Go (CleanServiceName).
const serviceRawSelectSQL = `
ISNULL(NULLIF(LTRIM(RTRIM(ISNULL(ex.WEB_NAME, ''))), ''), ISNULL(ex.NAME, ''))`

var (
	reServicePR   = regexp.MustCompile(`(?i)\s*\(р\)`)
	reServiceMin  = regexp.MustCompile(`(?i)\s*\d+\s*мин\.?`)
	reServiceRub  = regexp.MustCompile(`(?i)\s*-\s*\d[\d\s]*руб\.?`)
	reWaitlist    = regexp.MustCompile(`(?i)ожидан`)
)

// CleanServiceName strips registrar suffixes from PL_EXAM names.
func CleanServiceName(s string) string {
	s = strings.TrimSpace(s)
	s = reServicePR.ReplaceAllString(s, "")
	s = reServiceMin.ReplaceAllString(s, "")
	s = reServiceRub.ReplaceAllString(s, "")
	s = strings.TrimSpace(s)
	s = strings.Trim(s, "-–— \t")
	return strings.Join(strings.Fields(s), " ")
}

// IsWaitlistService reports names like «Лист ожидания».
func IsWaitlistService(raw string) bool {
	return reWaitlist.MatchString(raw)
}
