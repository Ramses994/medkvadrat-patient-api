package repo

import (
	"fmt"
	"strings"
)

// MaskPhoneDisplay masks a phone for GET /api/me/profile: +7 *** ***-45-67 (last 4 digits).
func MaskPhoneDisplay(phone string) string {
	n := NormalizePhone(phone)
	if n == "" {
		digits := digitsOnly(phone)
		if len(digits) >= 4 {
			n = digits
		} else {
			return ""
		}
	}
	last4 := n[len(n)-4:]
	return fmt.Sprintf("+7 *** ***-%s-%s", last4[:2], last4[2:])
}

// MaskEmailDisplay masks an email for GET /api/me/profile: first local letter + *** + full domain.
func MaskEmailDisplay(email string) string {
	email = strings.TrimSpace(email)
	at := strings.LastIndex(email, "@")
	if at <= 0 || at == len(email)-1 {
		return ""
	}
	local := email[:at]
	domain := email[at:]
	r := []rune(local)
	if len(r) == 0 {
		return ""
	}
	return string(r[0]) + "***" + domain
}

func digitsOnly(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}
