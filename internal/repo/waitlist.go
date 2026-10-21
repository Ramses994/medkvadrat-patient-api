package repo

import (
	"database/sql"
	"fmt"
	"regexp"
	"strings"
)

const defaultWaitlistExamID int64 = 3118

var reWaitlist = regexp.MustCompile(`(?i)ожидан`)

func NormalizeWaitlistExamIDs(ids []int64) []int64 {
	if len(ids) == 0 {
		return []int64{defaultWaitlistExamID}
	}
	return ids
}

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
CASE WHEN ISNULL(ex.NAME,'') LIKE N'%ожидан%' OR ex.PL_EXAM_ID IN (` + in.String() + `) THEN 1 ELSE 0 END AS IS_WAITLIST`
	return frag, args
}

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

// DashboardConfirmationCounts counts confirmation statuses, skipping waitlist rows.
func DashboardConfirmationCounts(branches []DashboardBranchSchedule) map[string]int {
	counts := map[string]int{"confirmed": 0, "declined": 0, "reschedule": 0, "pending": 0}
	for _, b := range branches {
		for _, a := range b.Appointments {
			if a.IsWaitlist {
				continue
			}
			key := "pending"
			if a.Confirmation != nil && *a.Confirmation != "" {
				key = *a.Confirmation
			}
			counts[key]++
		}
	}
	return counts
}
