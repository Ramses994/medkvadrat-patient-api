package repo

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"
)

const waitlistExamID int64 = 3118

// BookingOption is one active pilot pair shown to the patient.
type BookingOption struct {
	PlSubjID    int64
	PlExamID    int64
	BranchID    int
	BranchCode  string
	MedecinsID  int64
	DoctorName  string
	Service     string
	DurationMin int
}

// InactivePairError is a configured pair that failed a pilot check. Reason is a code, not patient data.
type InactivePairError struct {
	Reason string
}

func (e InactivePairError) Error() string { return "inactive pair: " + e.Reason }

func pilotBranch(id int) bool {
	return id == 3 || id == 106 || id == 496
}

// LoadBookingPair reads one line+exam and returns the first failed pilot check.
func LoadBookingPair(ctx context.Context, db *sql.DB, plSubjID, plExamID int64) (BookingOption, error) {
	const q = `
SELECT
  CAST(ISNULL(ps.WEB_ACCESS, 0) AS int),
  CAST(ISNULL(ps.ARCHIVE, 0) AS int),
  ps.WEB_ACCEPT,
  ISNULL(ps.FM_INTORG_ID, 0),
  ISNULL(o.CODE, ''),
  ps.MEDECINS_ID,
  LTRIM(RTRIM(ISNULL(m.NOM,'') + ' ' + ISNULL(m.PRENOM,'') + ' ' + ISNULL(m.PATRONYME,''))),
  CASE WHEN pse.PL_SUBJ_ID IS NULL THEN 0 ELSE 1 END,
  CAST(ISNULL(ex.WEB_ACCESS, 0) AS int),
  CAST(ISNULL(ex.isTeleExam, 0) AS int),
  CAST(ISNULL(ex.ARCHIVE, 0) AS int),
  CASE
    WHEN ex.PL_EXAM_ID IS NULL THEN ''
    WHEN ex.DUREE IS NOT NULL AND ex.DUREE = FLOOR(ex.DUREE) AND ex.DUREE > 0 AND ex.DUREE < 10000
      THEN CONVERT(varchar(10), CONVERT(int, ex.DUREE))
    ELSE 'bad'
  END,
  ISNULL(ex.WEB_NAME, ''),
  ISNULL(ex.NAME, '')
FROM PL_SUBJ ps
LEFT JOIN FM_ORG o ON o.FM_ORG_ID = ps.FM_INTORG_ID
LEFT JOIN MEDECINS m ON m.MEDECINS_ID = ps.MEDECINS_ID
LEFT JOIN PL_SUBJ_EXAM pse ON pse.PL_SUBJ_ID = ps.PL_SUBJ_ID AND pse.PL_EXAM_ID = @exam
LEFT JOIN PL_EXAM ex ON ex.PL_EXAM_ID = pse.PL_EXAM_ID
WHERE ps.PL_SUBJ_ID = @subj`

	var (
		webAccess, archive, examOnLine, examWeb, examTele, examArchive int
		webAccept, medecins                                            sql.NullInt64
		branchID                                                       int
		branchCode, doctor, duree, webName, examName                   string
	)
	err := db.QueryRowContext(ctx, q, sql.Named("subj", plSubjID), sql.Named("exam", plExamID)).Scan(
		&webAccess, &archive, &webAccept, &branchID, &branchCode, &medecins, &doctor,
		&examOnLine, &examWeb, &examTele, &examArchive, &duree, &webName, &examName,
	)
	if err == sql.ErrNoRows {
		return BookingOption{}, InactivePairError{Reason: "line_not_found"}
	}
	if err != nil {
		return BookingOption{}, fmt.Errorf("query booking pair: %w", err)
	}
	opt, reason := classifyBookingPair(plSubjID, plExamID, webAccess, archive, webAccept, branchID, branchCode, medecins, doctor, examOnLine, examWeb, examTele, examArchive, duree, webName, examName)
	if reason != "" {
		return BookingOption{}, InactivePairError{Reason: reason}
	}
	return opt, nil
}

func classifyBookingPair(plSubjID, plExamID int64, webAccess, archive int, webAccept sql.NullInt64, branchID int, branchCode string, medecins sql.NullInt64, doctor string, examOnLine, examWeb, examTele, examArchive int, duree, webName, examName string) (BookingOption, string) {
	if webAccess != 1 {
		return BookingOption{}, "web_access"
	}
	if archive != 0 {
		return BookingOption{}, "archive"
	}
	if webAccept.Valid && (webAccept.Int64 == 1 || webAccept.Int64 == 2) {
		return BookingOption{}, "web_accept"
	}
	if examOnLine != 1 {
		return BookingOption{}, "exam_not_on_line"
	}
	if plExamID == waitlistExamID {
		return BookingOption{}, "waitlist_exam"
	}
	if examWeb != 1 && examTele != 1 {
		return BookingOption{}, "exam_not_web"
	}
	if examArchive != 0 {
		return BookingOption{}, "exam_archive"
	}
	minutes, err := strconv.Atoi(strings.TrimSpace(duree))
	if err != nil || minutes <= 0 {
		return BookingOption{}, "duration"
	}
	if !pilotBranch(branchID) {
		return BookingOption{}, "branch"
	}
	var medID int64
	if medecins.Valid {
		medID = medecins.Int64
	}
	return BookingOption{
		PlSubjID:    plSubjID,
		PlExamID:    plExamID,
		BranchID:    branchID,
		BranchCode:  strings.TrimSpace(branchCode),
		MedecinsID:  medID,
		DoctorName:  strings.Join(strings.Fields(doctor), " "),
		Service:     DisplayService(webName, examName),
		DurationMin: minutes,
	}, ""
}
