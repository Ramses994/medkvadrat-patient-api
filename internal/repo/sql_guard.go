package repo

import "regexp"

var mutatingSQL = regexp.MustCompile(`(?i)\b(INSERT|UPDATE|DELETE|MERGE|EX` + `EC)\b`)

func SQLLooksMutating(src string) bool {
	return mutatingSQL.MatchString(src)
}
