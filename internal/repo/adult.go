package repo

import "time"

// IsAdult reports whether birth is at least 18 years before today (same calendar rule as the MAX bot LK-1f).
// A missing birth date is not adult.
func IsAdult(birth *time.Time, today time.Time) bool {
	if birth == nil {
		return false
	}
	loc := today.Location()
	if loc == nil {
		loc = moscowLocation()
	}
	today = time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, loc)
	born := time.Date(birth.Year(), birth.Month(), birth.Day(), 0, 0, 0, 0, loc)
	age := today.Year() - born.Year()
	if today.Month() < born.Month() || (today.Month() == born.Month() && today.Day() < born.Day()) {
		age--
	}
	return age >= 18
}

// FilterAdultOTPPatients keeps only patients 18+ by Moscow calendar day of now.
func FilterAdultOTPPatients(pats []OTPPatient, now time.Time) []OTPPatient {
	today := now.In(moscowLocation())
	out := make([]OTPPatient, 0, len(pats))
	for _, p := range pats {
		if IsAdult(p.BirthDate, today) {
			out = append(out, p)
		}
	}
	return out
}
