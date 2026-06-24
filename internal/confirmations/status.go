package confirmations

import "strings"

// Allowed confirmation statuses from MAX reminder buttons (PR #3b-bot).
var allowedStatuses = map[string]struct{}{
	"confirmed":  {},
	"declined":   {},
	"reschedule": {},
}

func ValidStatus(status string) bool {
	_, ok := allowedStatuses[strings.ToLower(strings.TrimSpace(status))]
	return ok
}

func NormalizeStatus(status string) string {
	return strings.ToLower(strings.TrimSpace(status))
}
