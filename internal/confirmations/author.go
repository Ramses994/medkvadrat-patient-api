package confirmations

import "strings"

// Author is who marked a waitlist row. Login stays in updated_by for old rows;
// StaffID and Name are written on new marks and never rewritten from a later
// account that reuses the same login.
type Author struct {
	Login string
	ID    int64
	Name  string
}

// LoginOnly is an author without a staff snapshot (tests and legacy callers).
func LoginOnly(login string) Author {
	return Author{Login: login}
}

// Display prefers the name snapshot; otherwise the login, as before.
func (a Author) Display() string {
	if n := strings.TrimSpace(a.Name); n != "" {
		return n
	}
	return a.Login
}

func displayAuthor(login, name string) string {
	return Author{Login: login, Name: name}.Display()
}
