package repo

import (
	"context"
	"database/sql"
)

type CreatedRow struct {
	Found       bool
	PatientID   int64
	PlSubjID    int64
	PlExamID    int64
	DateStart   string
	CreatorID   int64
	Cancelled   int
	Status      int
	NotAccepted int
}

type OwnerRow struct {
	Found     bool
	PatientID int64
	PlSubjID  int64
	OrgID     int
	DateStart string
	Cancelled int
	Status    int
}

func FutureLKCount(ctx context.Context, db *sql.DB, patientID, authorID int64, nowMSK string) (int, error) {
	var n int
	err := db.QueryRowContext(ctx, `
SELECT COUNT(*)
FROM PLANNING
WHERE PATIENTS_ID = @pid
  AND MEDECINS_CREATOR_ID = @author
  AND ISNULL(CANCELLED, 0) = 0
  AND ISNULL(STATUS, 0) = 0
  AND DATE_START >= CONVERT(datetime, @now, 126)`,
		sql.Named("pid", patientID),
		sql.Named("author", authorID),
		sql.Named("now", nowMSK),
	).Scan(&n)
	return n, err
}

func DoctorDayBusy(ctx context.Context, db *sql.DB, patientID, medecinsID, plSubjID int64, day string) (bool, error) {
	var n int
	var err error
	if medecinsID > 0 {
		err = db.QueryRowContext(ctx, `
SELECT COUNT(*)
FROM PLANNING p
INNER JOIN PL_SUBJ ps ON ps.PL_SUBJ_ID = p.PL_SUBJ_ID
WHERE p.PATIENTS_ID = @pid
  AND ps.MEDECINS_ID = @doc
  AND ISNULL(p.CANCELLED, 0) = 0
  AND ISNULL(p.STATUS, 0) = 0
  AND CONVERT(date, p.DATE_START) = CONVERT(date, @day, 23)`,
			sql.Named("pid", patientID),
			sql.Named("doc", medecinsID),
			sql.Named("day", day),
		).Scan(&n)
	} else {
		err = db.QueryRowContext(ctx, `
SELECT COUNT(*)
FROM PLANNING p
WHERE p.PATIENTS_ID = @pid
  AND p.PL_SUBJ_ID = @subj
  AND ISNULL(p.CANCELLED, 0) = 0
  AND ISNULL(p.STATUS, 0) = 0
  AND CONVERT(date, p.DATE_START) = CONVERT(date, @day, 23)`,
			sql.Named("pid", patientID),
			sql.Named("subj", plSubjID),
			sql.Named("day", day),
		).Scan(&n)
	}
	return n > 0, err
}

func ReadCreated(ctx context.Context, db *sql.DB, planningID int64) (CreatedRow, error) {
	var row CreatedRow
	var start string
	err := db.QueryRowContext(ctx, `
SELECT PATIENTS_ID, PL_SUBJ_ID, PL_EXAM_ID,
  CONVERT(varchar(19), DATE_START, 120),
  ISNULL(MEDECINS_CREATOR_ID, 0),
  CAST(ISNULL(CANCELLED, 0) AS int),
  CAST(ISNULL(STATUS, 0) AS int),
  CAST(ISNULL(NOT_ACCEPTED, 0) AS int)
FROM PLANNING
WHERE PLANNING_ID = @id`, sql.Named("id", planningID)).Scan(
		&row.PatientID, &row.PlSubjID, &row.PlExamID, &start, &row.CreatorID,
		&row.Cancelled, &row.Status, &row.NotAccepted,
	)
	if err == sql.ErrNoRows {
		return CreatedRow{}, nil
	}
	if err != nil {
		return CreatedRow{}, err
	}
	row.Found = true
	row.DateStart = wallClock(start)
	return row, nil
}

func FindReconcile(ctx context.Context, db *sql.DB, patientID, plSubjID, plExamID, authorID int64, start, since string) (int64, bool, error) {
	var id sql.NullInt64
	err := db.QueryRowContext(ctx, `
SELECT TOP 1 PLANNING_ID
FROM PLANNING
WHERE PATIENTS_ID = @pid
  AND PL_SUBJ_ID = @subj
  AND PL_EXAM_ID = @exam
  AND DATE_START = CONVERT(datetime, @start, 126)
  AND MEDECINS_CREATOR_ID = @author
  AND ISNULL(CANCELLED, 0) = 0
  AND ISNULL(STATUS, 0) = 0
  AND CREATE_DATE_TIME >= CONVERT(datetime, @since, 126)
ORDER BY PLANNING_ID`,
		sql.Named("pid", patientID),
		sql.Named("subj", plSubjID),
		sql.Named("exam", plExamID),
		sql.Named("start", start),
		sql.Named("author", authorID),
		sql.Named("since", since),
	).Scan(&id)
	if err == sql.ErrNoRows || !id.Valid {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return id.Int64, true, nil
}

func LoadOwner(ctx context.Context, db *sql.DB, planningID int64) (OwnerRow, error) {
	var row OwnerRow
	var start string
	err := db.QueryRowContext(ctx, `
SELECT p.PATIENTS_ID, p.PL_SUBJ_ID, ISNULL(ps.FM_INTORG_ID, 0),
  CONVERT(varchar(19), p.DATE_START, 120),
  CAST(ISNULL(p.CANCELLED, 0) AS int),
  CAST(ISNULL(p.STATUS, 0) AS int)
FROM PLANNING p
LEFT JOIN PL_SUBJ ps ON ps.PL_SUBJ_ID = p.PL_SUBJ_ID
WHERE p.PLANNING_ID = @id`, sql.Named("id", planningID)).Scan(
		&row.PatientID, &row.PlSubjID, &row.OrgID, &start, &row.Cancelled, &row.Status,
	)
	if err == sql.ErrNoRows {
		return OwnerRow{}, nil
	}
	if err != nil {
		return OwnerRow{}, err
	}
	row.Found = true
	row.DateStart = wallClock(start)
	return row, nil
}

func wallClock(raw string) string {
	if len(raw) >= 19 && raw[10] == ' ' {
		return raw[:10] + "T" + raw[11:19]
	}
	return raw
}
