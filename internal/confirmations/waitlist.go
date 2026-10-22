package confirmations

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	WaitlistReasonDeclined   = "declined"
	WaitlistReasonReschedule = "reschedule"
	WaitlistStatusNew        = "new"
	WaitlistStatusContacted  = "contacted"
	WaitlistStatusRebooked   = "rebooked"
	WaitlistStatusClosed     = "closed"
)

var allowedOutcomes = map[string]struct{}{
	"no_answer": {},
	"call_back": {},
	"rebooked":  {},
	"refused":   {},
	"other":     {},
}

var ErrInvalidWaitlistTransition = errors.New("INVALID_WAITLIST_TRANSITION")
var ErrUnknownWaitlistOutcome = errors.New("UNKNOWN_WAITLIST_OUTCOME")
var ErrWaitlistNotFound = errors.New("WAITLIST_NOT_FOUND")

// DashboardItem is a waitlist row plus Medialog fields for the staff page.
type DashboardItem struct {
	ID              int64
	PlanningID      int64
	PatientID       int64
	Reason          string
	Status          string
	Outcome         string
	CreatedAt       string
	CreatedAtMSK    string
	BranchID        int
	PatientName     string
	PatientPhone    string
	PatientBirth    string
	OrigDateStart   string
	OrigDoctor      string
	OrigService     string
	MedialogState   string
	FutureLabel     string
	FutureDateStart string
	FutureDoctor    string
	FutureService   string
}

type DashboardPage struct {
	Items     []DashboardItem
	Total     int
	OpenTotal int
	Page      int
	PerPage   int
}

// WaitlistRow is a gateway.db waitlist record (no FIO/phone/doctor).
type WaitlistRow struct {
	ID           int64
	PlanningID   int64
	PatientID    int64
	ApptVersion  string
	ApptStart    string
	Reason       string
	Source       string
	Status       string
	Outcome      string
	CreatedAt    string
	UpdatedAt    string
	CreatedAtMSK string
	BranchID     int
	UpdatedBy    string
}

func ValidOutcome(outcome string) bool {
	_, ok := allowedOutcomes[strings.ToLower(strings.TrimSpace(outcome))]
	return ok
}

func waitlistReason(status string) (string, bool) {
	switch NormalizeStatus(status) {
	case WaitlistReasonDeclined, WaitlistReasonReschedule:
		return NormalizeStatus(status), true
	default:
		return "", false
	}
}

func insertWaitlistTx(ctx context.Context, tx *sql.Tx, row WaitlistRow) error {
	_, err := tx.ExecContext(ctx, `
INSERT INTO waitlist (
  planning_id, patient_id, appt_version, appt_start, reason, source, status,
  created_at, updated_at, created_at_msk, branch_id
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		row.PlanningID, row.PatientID, row.ApptVersion, row.ApptStart, row.Reason, row.Source, row.Status,
		row.CreatedAt, row.UpdatedAt, row.CreatedAtMSK, row.BranchID,
	)
	if err != nil {
		return fmt.Errorf("insert waitlist: %w", err)
	}
	return nil
}

func CountWaitlist(ctx context.Context, db *sql.DB, planningID int64, apptVersion string) (int, error) {
	var n int
	err := db.QueryRowContext(ctx, `
SELECT COUNT(*) FROM waitlist WHERE planning_id = ? AND appt_version = ?`, planningID, apptVersion).Scan(&n)
	return n, err
}

func CountWaitlistByPlanning(ctx context.Context, db *sql.DB, planningID int64) (int, error) {
	var n int
	err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM waitlist WHERE planning_id = ?`, planningID).Scan(&n)
	return n, err
}

type WaitlistListFilter struct {
	OpenOnly bool
	BranchID *int
	Page     int
	PerPage  int
}

func (r *Repo) ListWaitlist(ctx context.Context, f WaitlistListFilter) (rows []WaitlistRow, total int, err error) {
	if f.Page < 1 {
		f.Page = 1
	}
	if f.PerPage < 1 {
		f.PerPage = 50
	}
	if f.PerPage > 100 {
		f.PerPage = 100
	}

	where, args := waitlistWhere(f)
	countQ := `SELECT COUNT(*) FROM waitlist` + where
	if err := r.db.QueryRowContext(ctx, countQ, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count waitlist: %w", err)
	}

	q := `
SELECT id, planning_id, patient_id, appt_version, appt_start, reason, source, status,
       IFNULL(outcome,''), created_at, updated_at, created_at_msk, branch_id, IFNULL(updated_by,'')
FROM waitlist` + where + `
ORDER BY created_at DESC, id DESC
LIMIT ? OFFSET ?`
	args = append(append([]any{}, args...), f.PerPage, (f.Page-1)*f.PerPage)
	rs, err := r.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("list waitlist: %w", err)
	}
	defer rs.Close()
	for rs.Next() {
		var row WaitlistRow
		if err := rs.Scan(
			&row.ID, &row.PlanningID, &row.PatientID, &row.ApptVersion, &row.ApptStart,
			&row.Reason, &row.Source, &row.Status, &row.Outcome, &row.CreatedAt, &row.UpdatedAt,
			&row.CreatedAtMSK, &row.BranchID, &row.UpdatedBy,
		); err != nil {
			return nil, 0, err
		}
		rows = append(rows, row)
	}
	if rows == nil {
		rows = []WaitlistRow{}
	}
	return rows, total, rs.Err()
}

func (r *Repo) CountOpenWaitlist(ctx context.Context) (int, error) {
	var n int
	err := r.db.QueryRowContext(ctx, `
SELECT COUNT(*) FROM waitlist WHERE status IN ('new','contacted')`).Scan(&n)
	return n, err
}

func waitlistWhere(f WaitlistListFilter) (string, []any) {
	var parts []string
	var args []any
	if f.OpenOnly {
		parts = append(parts, `status IN ('new','contacted')`)
	}
	if f.BranchID != nil {
		parts = append(parts, `branch_id = ?`)
		args = append(args, *f.BranchID)
	}
	if len(parts) == 0 {
		return "", nil
	}
	return " WHERE " + strings.Join(parts, " AND "), args
}

func (r *Repo) OpenWaitlistPlanningIDs(ctx context.Context, planningIDs []int64) (map[int64]bool, error) {
	out := map[int64]bool{}
	if len(planningIDs) == 0 {
		return out, nil
	}
	var b strings.Builder
	b.WriteString(`SELECT planning_id FROM waitlist WHERE status IN ('new','contacted') AND planning_id IN (`)
	args := make([]any, len(planningIDs))
	for i, id := range planningIDs {
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString("?")
		args[i] = id
	}
	b.WriteString(")")
	rs, err := r.db.QueryContext(ctx, b.String(), args...)
	if err != nil {
		return nil, fmt.Errorf("open waitlist ids: %w", err)
	}
	defer rs.Close()
	for rs.Next() {
		var id int64
		if err := rs.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rs.Err()
}

func (r *Repo) UpdateWaitlist(ctx context.Context, id int64, status, outcome, updatedBy string, now time.Time) (WaitlistRow, error) {
	status = strings.ToLower(strings.TrimSpace(status))
	outcome = strings.ToLower(strings.TrimSpace(outcome))
	if !ValidOutcome(outcome) {
		return WaitlistRow{}, ErrUnknownWaitlistOutcome
	}

	row, err := r.getWaitlist(ctx, id)
	if err != nil {
		return WaitlistRow{}, err
	}
	if !waitlistTransitionOK(row.Status, status) {
		return WaitlistRow{}, ErrInvalidWaitlistTransition
	}

	updated := FormatWaitlistUTC(now)
	_, err = r.db.ExecContext(ctx, `
UPDATE waitlist SET status = ?, outcome = ?, updated_at = ?, updated_by = ? WHERE id = ?`,
		status, outcome, updated, updatedBy, id)
	if err != nil {
		return WaitlistRow{}, fmt.Errorf("update waitlist: %w", err)
	}
	row.Status = status
	row.Outcome = outcome
	row.UpdatedAt = updated
	row.UpdatedBy = updatedBy
	return row, nil
}

func waitlistTransitionOK(from, to string) bool {
	switch from {
	case WaitlistStatusNew:
		return to == WaitlistStatusContacted || to == WaitlistStatusRebooked || to == WaitlistStatusClosed
	case WaitlistStatusContacted:
		return to == WaitlistStatusRebooked || to == WaitlistStatusClosed
	default:
		return false
	}
}

func (r *Repo) getWaitlist(ctx context.Context, id int64) (WaitlistRow, error) {
	var row WaitlistRow
	err := r.db.QueryRowContext(ctx, `
SELECT id, planning_id, patient_id, appt_version, appt_start, reason, source, status,
       IFNULL(outcome,''), created_at, updated_at, created_at_msk, branch_id, IFNULL(updated_by,'')
FROM waitlist WHERE id = ?`, id).Scan(
		&row.ID, &row.PlanningID, &row.PatientID, &row.ApptVersion, &row.ApptStart,
		&row.Reason, &row.Source, &row.Status, &row.Outcome, &row.CreatedAt, &row.UpdatedAt,
		&row.CreatedAtMSK, &row.BranchID, &row.UpdatedBy,
	)
	if err == sql.ErrNoRows {
		return WaitlistRow{}, ErrWaitlistNotFound
	}
	return row, err
}
