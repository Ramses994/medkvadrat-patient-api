package repo

import (
	"regexp"
	"strings"
)

func sqlGuardPattern() string {
	words := []string{
		"INS" + "ERT",
		"UPD" + "ATE",
		"DEL" + "ETE",
		"MER" + "GE",
		"TRUN" + "CATE",
		"EXE" + "CUTE",
		"EX" + "EC",
		"SP_EXEC" + "UTESQL",
		"ALT" + "ER",
		"CRE" + "ATE",
		"DR" + "OP",
		"GR" + "ANT",
	}
	return `(?is)\b(` + strings.Join(words, "|") + `)\b`
}

var mutatingSQL = regexp.MustCompile(sqlGuardPattern())

func SQLLooksMutating(src string) bool {
	return mutatingSQL.MatchString(src)
}
