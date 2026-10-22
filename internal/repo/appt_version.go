package repo

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

// ApptVersion is a short hex of DATE_START (seconds) + PL_SUBJ_ID + PL_EXAM_ID.
func ApptVersion(dateStart string, plSubjID, plExamID int64) string {
	dateStart = strings.TrimSpace(dateStart)
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s|%d|%d", dateStart, plSubjID, plExamID)))
	return hex.EncodeToString(sum[:])[:12]
}
