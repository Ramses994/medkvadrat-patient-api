package repo

import "strings"

// NormalizePhone returns the canonical 7XXXXXXXXXX form used by the cabinet.
// Ten digits gain a leading 7. An 11-digit number starting with 8 becomes 7.
// Anything else, including an empty value, is not a phone.
func NormalizePhone(phone string) string {
	d := strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, phone)
	if len(d) == 11 && strings.HasPrefix(d, "8") {
		d = "7" + d[1:]
	}
	if len(d) == 10 {
		d = "7" + d
	}
	if len(d) != 11 || !strings.HasPrefix(d, "7") {
		return ""
	}
	return d
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
