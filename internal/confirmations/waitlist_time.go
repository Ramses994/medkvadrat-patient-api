package confirmations

import "time"

// MoscowLocation is wall-clock Europe/Moscow (fixed offset if tzdata is missing).
func MoscowLocation() *time.Location {
	return time.FixedZone("MSK", 3*3600)
}

func FormatWaitlistUTC(now time.Time) string {
	return now.UTC().Format("2006-01-02T15:04:05Z")
}

func FormatWaitlistMSK(now time.Time) string {
	return now.In(MoscowLocation()).Format("2006-01-02 15:04:05")
}

// MedialogCreateIsNewer reports whether a Medialog CREATE_DATE_TIME wall-clock
// string is after the waitlist created_at_msk (both "YYYY-MM-DD HH:MM:SS").
func MedialogCreateIsNewer(createdAtMSK, createDateTime string) bool {
	return createDateTime > createdAtMSK
}
