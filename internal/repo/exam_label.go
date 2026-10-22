package repo

import (
	"database/sql"
	"fmt"
	"regexp"
	"strings"
)

const defaultWaitlistExamID int64 = 3118

var (
	reServicePR  = regexp.MustCompile(`(?i)\s*\(р\)`)
	reServiceMin = regexp.MustCompile(`(?i)\s*\d+\s*мин\.?`)
	reServiceRub = regexp.MustCompile(`(?i)\s*-\s*\d[\d\s]*руб\.?`)
	reLangTag    = regexp.MustCompile(`(?i)#[a-z]{3}#`)
	reWaitlist   = regexp.MustCompile(`(?i)ожидан`)
)

// NormalizeWaitlistExamIDs defaults to PL_EXAM_ID 3118 (лист ожидания).
func NormalizeWaitlistExamIDs(ids []int64) []int64 {
	if len(ids) == 0 {
		return []int64{defaultWaitlistExamID}
	}
	return ids
}

// examSelectSQL returns WEB_NAME, NAME, IS_WAITLIST (SQL Server 2008) and named args.
func examSelectSQL(waitlistIDs []int64) (string, []any) {
	ids := NormalizeWaitlistExamIDs(waitlistIDs)
	var in strings.Builder
	args := make([]any, 0, len(ids))
	for i, id := range ids {
		if i > 0 {
			in.WriteString(",")
		}
		name := fmt.Sprintf("wl%d", i)
		in.WriteString("@" + name)
		args = append(args, sql.Named(name, id))
	}
	frag := `
ISNULL(ex.WEB_NAME,'') AS EXAM_WEB_NAME,
ISNULL(ex.NAME,'') AS EXAM_NAME,
CAST(CASE WHEN ISNULL(ex.NAME,'') LIKE N'%ожидан%' OR ex.PL_EXAM_ID IN (` + in.String() + `) THEN 1 ELSE 0 END AS int) AS IS_WAITLIST`
	return frag, args
}

// PickExamLabel chooses WEB_NAME (language-tagged) or NAME, then caller cleans it.
func PickExamLabel(webName, name string) string {
	webName = strings.TrimSpace(webName)
	name = strings.TrimSpace(name)
	picked := webName
	if reLangTag.MatchString(webName) {
		picked = extractLangSegment(webName)
	}
	if picked == "" {
		picked = name
	}
	return picked
}

func extractLangSegment(s string) string {
	lower := strings.ToLower(s)
	start := -1
	if i := strings.Index(lower, "#rus#"); i >= 0 {
		start = i + len("#rus#")
	} else if loc := reLangTag.FindStringIndex(s); loc != nil {
		start = loc[1]
	}
	if start < 0 {
		return strings.TrimSpace(s)
	}
	rest := s[start:]
	if loc := reLangTag.FindStringIndex(rest); loc != nil {
		rest = rest[:loc[0]]
	}
	return strings.TrimSpace(rest)
}

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

// DisplayService is PickExamLabel then CleanServiceName.
func DisplayService(webName, name string) string {
	return CleanServiceName(PickExamLabel(webName, name))
}

// IsWaitlistExam is the Go-side of the SQL rule (name or configured id).
func IsWaitlistExam(name string, examID int64, waitlistIDs []int64) bool {
	if reWaitlist.MatchString(name) {
		return true
	}
	for _, id := range NormalizeWaitlistExamIDs(waitlistIDs) {
		if examID == id {
			return true
		}
	}
	return false
}
