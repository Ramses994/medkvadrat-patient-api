package repo

import "strings"

// NormalizePhone returns the canonical 7XXXXXXXXXX form of a Russian number.
// A leading plus is a country code: only +7 with exactly 11 digits is a number.
// A leading 00 is an international prefix and is not a number.
// Without a plus, 11 digits starting with 7 or 8, or 10 digits, are Russian.
func NormalizePhone(phone string) string {
	s := strings.TrimSpace(phone)
	if s == "" {
		return ""
	}
	if strings.HasPrefix(s, "+") {
		body, ok := phoneDigits(s[1:])
		if !ok || len(body) != 11 || body[0] != '7' {
			return ""
		}
		return body
	}
	if strings.HasPrefix(s, "00") {
		return ""
	}
	body, ok := phoneDigits(s)
	if !ok {
		return ""
	}
	switch {
	case len(body) == 11 && (body[0] == '7' || body[0] == '8'):
		if body[0] == '8' {
			return "7" + body[1:]
		}
		return body
	case len(body) == 10:
		return "7" + body
	default:
		return ""
	}
}

func phoneDigits(s string) (string, bool) {
	var b strings.Builder
	for _, r := range s {
		switch r {
		case ' ', '\t', '\n', '\r', '-', '(', ')', '.':
			continue
		}
		if r < '0' || r > '9' {
			return "", false
		}
		b.WriteRune(r)
	}
	return b.String(), true
}

// HasPhone reports whether any of the three card fields contains the wanted number.
// A field may list several numbers separated by comma, semicolon, or slash.
// A part matches only when it normalizes to want exactly.
func HasPhone(mobile, tel, work, want string) bool {
	if want == "" {
		return false
	}
	for _, raw := range []string{mobile, tel, work} {
		for _, part := range strings.FieldsFunc(raw, func(r rune) bool {
			return r == ',' || r == ';' || r == '/'
		}) {
			if NormalizePhone(part) == want {
				return true
			}
		}
	}
	return false
}

func phoneDigitsExpr(column string) string {
	return "REPLACE(REPLACE(REPLACE(REPLACE(REPLACE(REPLACE(ISNULL(" + column + ",''),'+',''),'-',''),' ',''),'(',''),')',''),'.','')"
}

func phoneLikeWhere() string {
	return phoneDigitsExpr("MOBIL_TELEFON") + " LIKE '%' + @phone + '%'" +
		" OR " + phoneDigitsExpr("TEL") + " LIKE '%' + @phone + '%'" +
		" OR " + phoneDigitsExpr("RAB_TEL") + " LIKE '%' + @phone + '%'"
}

func phoneTail(normalized string) string {
	if len(normalized) > 10 {
		return normalized[len(normalized)-10:]
	}
	return normalized
}
