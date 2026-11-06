package repo

import (
	"context"
	"database/sql"

	"github.com/medkvadrat/medkvadrat-patient-api/internal/booking"
)

func LoadAdmission(ctx context.Context, db *sql.DB, planningID int64) (booking.Admission, error) {
	var out booking.Admission
	rows, err := db.QueryContext(ctx, `
SELECT DIR_ANSW_ID, PATDIREC_ID, CAST(ISNULL(ANSW_STATE, 0) AS int),
  CASE WHEN FM_BILL_ID IS NULL THEN 1 ELSE 0 END
FROM DIR_ANSW
WHERE PLANNING_ID = @id`, sql.Named("id", planningID))
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var a booking.Answer
		var billNull int
		var dir sql.NullInt64
		if err := rows.Scan(&a.ID, &dir, &a.AnswState, &billNull); err != nil {
			rows.Close()
			return out, err
		}
		a.BillNull = billNull == 1
		if dir.Valid {
			a.PatdirecID = dir.Int64
			a.HasPatdirec = true
		}
		out.Answers = append(out.Answers, a)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return out, err
	}
	rows.Close()

	drows, err := db.QueryContext(ctx, `
SELECT PATDIREC_ID, CAST(ISNULL(CANCELLED, 0) AS int), CAST(ISNULL(DIR_STATE, 0) AS int), PLANNING_CR_ID
FROM PATDIREC
WHERE PLANNING_CR_ID = @id`, sql.Named("id", planningID))
	if err != nil {
		return out, err
	}
	for drows.Next() {
		var d booking.Direction
		var cr sql.NullInt64
		if err := drows.Scan(&d.ID, &d.Cancelled, &d.DirState, &cr); err != nil {
			drows.Close()
			return out, err
		}
		if cr.Valid {
			v := cr.Int64
			d.PlanningCR = &v
		}
		out.Directions = append(out.Directions, d)
	}
	if err := drows.Err(); err != nil {
		drows.Close()
		return out, err
	}
	drows.Close()

	if len(out.Answers) == 1 && out.Answers[0].HasPatdirec {
		dir, err := loadDirection(ctx, db, out.Answers[0].PatdirecID)
		if err != nil {
			return out, err
		}
		out.OwnDirection = dir
		var n int
		if err := db.QueryRowContext(ctx, `
SELECT COUNT(*) FROM DIR_ANSW WHERE PATDIREC_ID = @dir AND DIR_ANSW_ID <> @answ`,
			sql.Named("dir", out.Answers[0].PatdirecID),
			sql.Named("answ", out.Answers[0].ID),
		).Scan(&n); err != nil {
			return out, err
		}
		out.OtherAnswersOnDir = n
	}
	if err := db.QueryRowContext(ctx, `
SELECT COUNT(*) FROM DM_DOC WHERE PLANNING_ID = @id AND VERIFIED = 1`,
		sql.Named("id", planningID),
	).Scan(&out.VerifiedDocs); err != nil {
		return out, err
	}
	return out, nil
}

func loadDirection(ctx context.Context, db *sql.DB, id int64) (*booking.Direction, error) {
	var d booking.Direction
	var cr sql.NullInt64
	err := db.QueryRowContext(ctx, `
SELECT PATDIREC_ID, CAST(ISNULL(CANCELLED, 0) AS int), CAST(ISNULL(DIR_STATE, 0) AS int), PLANNING_CR_ID
FROM PATDIREC WHERE PATDIREC_ID = @id`, sql.Named("id", id)).Scan(&d.ID, &d.Cancelled, &d.DirState, &cr)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if cr.Valid {
		v := cr.Int64
		d.PlanningCR = &v
	}
	return &d, nil
}

func LoadSnap(ctx context.Context, db *sql.DB, planningID int64, answID, dirID *int64) (booking.Snap, error) {
	var snap booking.Snap
	err := db.QueryRowContext(ctx, `
SELECT CAST(ISNULL(CANCELLED, 0) AS int), CAST(ISNULL(STATUS, 0) AS int)
FROM PLANNING WHERE PLANNING_ID = @id`, sql.Named("id", planningID)).Scan(&snap.PlanningCancelled, &snap.PlanningStatus)
	if err == sql.ErrNoRows {
		snap.PlanningFound = false
	} else if err != nil {
		return booking.Snap{}, err
	} else {
		snap.PlanningFound = true
	}
	if answID != nil {
		snap.AnswExpected = true
		var state int
		var planning, bill sql.NullInt64
		err := db.QueryRowContext(ctx, `
SELECT CAST(ISNULL(ANSW_STATE, 0) AS int), PLANNING_ID, FM_BILL_ID
FROM DIR_ANSW WHERE DIR_ANSW_ID = @id`, sql.Named("id", *answID)).Scan(&state, &planning, &bill)
		if err == sql.ErrNoRows {
			snap.AnswFound = false
		} else if err != nil {
			return booking.Snap{}, err
		} else {
			snap.AnswFound = true
			snap.AnswState = state
			if planning.Valid {
				v := planning.Int64
				snap.AnswPlanning = &v
			}
			if bill.Valid {
				v := bill.Int64
				snap.AnswBill = &v
			}
		}
	}
	if dirID != nil {
		snap.DirExpected = true
		err := db.QueryRowContext(ctx, `
SELECT CAST(ISNULL(CANCELLED, 0) AS int), CAST(ISNULL(DIR_STATE, 0) AS int)
FROM PATDIREC WHERE PATDIREC_ID = @id`, sql.Named("id", *dirID)).Scan(&snap.DirCancelled, &snap.DirState)
		if err == sql.ErrNoRows {
			snap.DirFound = false
		} else if err != nil {
			return booking.Snap{}, err
		} else {
			snap.DirFound = true
		}
	}
	snap.A, err = idList(ctx, db, `
SELECT DIR_ANSW_ID FROM DIR_ANSW WHERE PLANNING_ID = @planning
UNION
SELECT DIR_ANSW_ID FROM DIR_ANSW WHERE @has_dir = 1 AND PATDIREC_ID = @dir`,
		sql.Named("planning", planningID),
		sql.Named("has_dir", hasID(dirID)),
		sql.Named("dir", idOrZero(dirID)),
	)
	if err != nil {
		return booking.Snap{}, err
	}
	snap.P, err = idList(ctx, db, `SELECT PATDIREC_ID FROM PATDIREC WHERE PLANNING_CR_ID = @planning`, sql.Named("planning", planningID))
	if err != nil {
		return booking.Snap{}, err
	}
	snap.D, err = idList(ctx, db, `SELECT DM_DOC_ID FROM DM_DOC WHERE PLANNING_ID = @planning AND VERIFIED = 1`, sql.Named("planning", planningID))
	if err != nil {
		return booking.Snap{}, err
	}
	snap.ReadOK = true
	return snap, nil
}

func hasID(id *int64) int {
	if id == nil {
		return 0
	}
	return 1
}

func idOrZero(id *int64) int64 {
	if id == nil {
		return 0
	}
	return *id
}

func idList(ctx context.Context, db *sql.DB, q string, args ...any) ([]int64, error) {
	rows, err := db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
