package confirmations

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/medkvadrat/medkvadrat-patient-api/internal/store"
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
var ErrWaitlistChanged = errors.New("WAITLIST_CHANGED")
var ErrWaitlistMoved = errors.New("WAITLIST_MOVED")
var ErrWaitlistOverloaded = errors.New("WAITLIST_OVERLOADED")

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
	Source          string
	Comment         string
	CreatorName     string
	UpdatedBy       string
	UpdatedAt       string
	ApptStart       string
	Version         int
	Enriched        bool
}

type DashboardPage struct {
	Items     []DashboardItem
	Total     int
	OpenTotal int
	Page      int
	PerPage   int
	Banner    string
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
	Version      int
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
	OpenOnly             bool
	Status               string
	BranchID             *int
	BranchIDs            []int
	Source               string
	Page                 int
	PerPage              int
	IncludeOtherBranches bool
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
	off := int64(f.Page-1) * int64(f.PerPage)
	if off < 0 || off >= int64(total) {
		return []WaitlistRow{}, total, nil
	}

	q := `
SELECT id, planning_id, patient_id, appt_version, appt_start, reason, source, status,
       IFNULL(outcome,''), created_at, updated_at, created_at_msk, branch_id, IFNULL(updated_by,''), IFNULL(version,1)
FROM waitlist` + where + `
ORDER BY created_at DESC, id DESC
LIMIT ? OFFSET ?`
	args = append(append([]any{}, args...), f.PerPage, off)
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
			&row.CreatedAtMSK, &row.BranchID, &row.UpdatedBy, &row.Version,
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

func (r *Repo) CountWaitlist(ctx context.Context, f WaitlistListFilter) (int, error) {
	where, args := waitlistWhere(f)
	var n int
	err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM waitlist`+where, args...).Scan(&n)
	return n, err
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
	switch f.Status {
	case WaitlistStatusNew, WaitlistStatusContacted, WaitlistStatusRebooked, WaitlistStatusClosed:
		parts = append(parts, `status = ?`)
		args = append(args, f.Status)
	default:
		if f.OpenOnly {
			parts = append(parts, `status IN ('new','contacted')`)
		}
	}
	if f.BranchID != nil {
		parts = append(parts, `branch_id = ?`)
		args = append(args, *f.BranchID)
	} else if len(f.BranchIDs) > 0 {
		in := `branch_id IN (` + placeholders(len(f.BranchIDs)) + `)`
		for _, id := range f.BranchIDs {
			args = append(args, id)
		}
		if f.IncludeOtherBranches {
			in = "(" + in + " OR branch_id NOT IN (106,3,496))"
		}
		parts = append(parts, in)
	}
	if len(parts) == 0 {
		return "", nil
	}
	return " WHERE " + strings.Join(parts, " AND "), args
}

func placeholders(n int) string {
	return strings.TrimRight(strings.Repeat("?,", n), ",")
}

// ListWaitlistMatching returns every row that matches the filter, capped at 500.
func (r *Repo) ListWaitlistMatching(ctx context.Context, f WaitlistListFilter) ([]WaitlistRow, error) {
	where, args := waitlistWhere(f)
	q := `
SELECT id, planning_id, patient_id, appt_version, appt_start, reason, source, status,
       IFNULL(outcome,''), created_at, updated_at, created_at_msk, branch_id, IFNULL(updated_by,''), IFNULL(version,1)
FROM waitlist` + where + `
ORDER BY created_at DESC, id DESC`
	rs, err := r.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("list waitlist matching: %w", err)
	}
	defer rs.Close()
	var rows []WaitlistRow
	for rs.Next() {
		var row WaitlistRow
		if err := rs.Scan(
			&row.ID, &row.PlanningID, &row.PatientID, &row.ApptVersion, &row.ApptStart,
			&row.Reason, &row.Source, &row.Status, &row.Outcome, &row.CreatedAt, &row.UpdatedAt,
			&row.CreatedAtMSK, &row.BranchID, &row.UpdatedBy, &row.Version,
		); err != nil {
			return nil, err
		}
		rows = append(rows, row)
	}
	if rows == nil {
		rows = []WaitlistRow{}
	}
	return rows, rs.Err()
}

// ListWaitlistHead returns the newest matching rows, at most limit.
// The dashboard merges this with an equally capped registry selection.
func (r *Repo) ListWaitlistHead(ctx context.Context, f WaitlistListFilter, limit int) ([]WaitlistRow, error) {
	if limit < 1 {
		limit = 1
	}
	where, args := waitlistWhere(f)
	q := `
SELECT id, planning_id, patient_id, appt_version, appt_start, reason, source, status,
       IFNULL(outcome,''), created_at, updated_at, created_at_msk, branch_id, IFNULL(updated_by,''), IFNULL(version,1)
FROM waitlist` + where + `
ORDER BY created_at DESC, id DESC
LIMIT ?`
	args = append(args, limit)
	rs, err := r.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("list waitlist head: %w", err)
	}
	defer rs.Close()
	var rows []WaitlistRow
	for rs.Next() {
		var row WaitlistRow
		if err := rs.Scan(
			&row.ID, &row.PlanningID, &row.PatientID, &row.ApptVersion, &row.ApptStart,
			&row.Reason, &row.Source, &row.Status, &row.Outcome, &row.CreatedAt, &row.UpdatedAt,
			&row.CreatedAtMSK, &row.BranchID, &row.UpdatedBy, &row.Version,
		); err != nil {
			return nil, err
		}
		rows = append(rows, row)
	}
	if rows == nil {
		rows = []WaitlistRow{}
	}
	return rows, rs.Err()
}

// ListDashboardRows returns waitlist rows for an in-memory dashboard page.
// It does not filter by branch: the snapshot decides who can see a row.
// More than cap rows is an error, not a silent cut.
func (r *Repo) ListDashboardRows(ctx context.Context, f WaitlistListFilter, now time.Time, cap int) ([]WaitlistRow, error) {
	if cap < 1 {
		cap = 1
	}
	where, args := dashboardWhere(f, now)
	q := `
SELECT id, planning_id, patient_id, appt_version, appt_start, reason, source, status,
       IFNULL(outcome,''), created_at, updated_at, created_at_msk, branch_id, IFNULL(updated_by,''), IFNULL(version,1)
FROM waitlist` + where + `
ORDER BY created_at DESC, id DESC
LIMIT ?`
	args = append(args, cap+1)
	rs, err := r.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("list dashboard waitlist: %w", err)
	}
	defer rs.Close()
	var rows []WaitlistRow
	for rs.Next() {
		var row WaitlistRow
		if err := rs.Scan(
			&row.ID, &row.PlanningID, &row.PatientID, &row.ApptVersion, &row.ApptStart,
			&row.Reason, &row.Source, &row.Status, &row.Outcome, &row.CreatedAt, &row.UpdatedAt,
			&row.CreatedAtMSK, &row.BranchID, &row.UpdatedBy, &row.Version,
		); err != nil {
			return nil, err
		}
		rows = append(rows, row)
	}
	if err := rs.Err(); err != nil {
		return nil, err
	}
	if len(rows) > cap {
		return nil, ErrWaitlistOverloaded
	}
	if rows == nil {
		rows = []WaitlistRow{}
	}
	return rows, nil
}

func dashboardWhere(f WaitlistListFilter, now time.Time) (string, []any) {
	cutoff := store.WaitlistClosedCutoff(now)
	switch f.Status {
	case WaitlistStatusNew, WaitlistStatusContacted:
		return " WHERE status = ?", []any{f.Status}
	case WaitlistStatusRebooked, WaitlistStatusClosed:
		return " WHERE status = ? AND updated_at >= ?", []any{f.Status, cutoff}
	}
	if f.OpenOnly {
		return " WHERE status IN ('new','contacted')", nil
	}
	return " WHERE (status IN ('new','contacted') OR (status IN ('rebooked','closed') AND updated_at >= ?))", []any{cutoff}
}

// ListOpenBranchRows reads open MAX rows for one dashboard generation.
// More than cap rows is ErrWaitlistOverloaded and must stop the Medialog read.
func (r *Repo) ListOpenBranchRows(ctx context.Context, cap int) ([]WaitlistRow, error) {
	if cap < 1 {
		cap = 1
	}
	rs, err := r.db.QueryContext(ctx, `
SELECT id, planning_id, patient_id, branch_id, IFNULL(created_at_msk, '')
FROM waitlist
WHERE status IN ('new','contacted')
ORDER BY id
LIMIT ?`, cap+1)
	if err != nil {
		return nil, err
	}
	defer rs.Close()
	var rows []WaitlistRow
	for rs.Next() {
		var row WaitlistRow
		if err := rs.Scan(&row.ID, &row.PlanningID, &row.PatientID, &row.BranchID, &row.CreatedAtMSK); err != nil {
			return nil, err
		}
		rows = append(rows, row)
	}
	if err := rs.Err(); err != nil {
		return nil, err
	}
	if len(rows) > cap {
		return nil, ErrWaitlistOverloaded
	}
	if rows == nil {
		rows = []WaitlistRow{}
	}
	return rows, nil
}

func (r *Repo) SetWaitlistBranch(ctx context.Context, id int64, branchID int) error {
	_, err := r.db.ExecContext(ctx, `UPDATE waitlist SET branch_id = ? WHERE id = ? AND branch_id <> ?`, branchID, id, branchID)
	return err
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

func (r *Repo) UpdateWaitlist(ctx context.Context, id int64, status, outcome, updatedBy string, now time.Time, guard SQLGuard) (WaitlistRow, error) {
	status = strings.ToLower(strings.TrimSpace(status))
	outcome = strings.ToLower(strings.TrimSpace(outcome))
	if !ValidOutcome(outcome) {
		return WaitlistRow{}, ErrUnknownWaitlistOutcome
	}
	row, err := r.getWaitlist(ctx, id)
	if err != nil {
		return WaitlistRow{}, err
	}
	if r.AfterWaitlistRead != nil {
		r.AfterWaitlistRead()
	}
	if !waitlistTransitionOK(row.Status, status) {
		return WaitlistRow{}, ErrInvalidWaitlistTransition
	}
	updated := FormatWaitlistUTC(now)
	var out WaitlistRow
	err = r.withImmediate(ctx, ctx, guard, func(conn *sql.Conn) error {
		res, err := conn.ExecContext(ctx, `
UPDATE waitlist SET status = ?, outcome = ?, updated_at = ?, updated_by = ?, version = version + 1
WHERE id = ? AND status = ? AND version = ?`,
			status, outcome, updated, updatedBy, id, row.Status, row.Version)
		if err != nil {
			return fmt.Errorf("update waitlist: %w", err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if n == 0 {
			if _, getErr := scanWaitlist(ctx, conn, id); getErr != nil {
				return getErr
			}
			return ErrWaitlistChanged
		}
		row.Status = status
		row.Outcome = outcome
		row.UpdatedAt = updated
		row.UpdatedBy = updatedBy
		row.Version++
		out = row
		return nil
	})
	if err != nil {
		return WaitlistRow{}, err
	}
	return out, nil
}

func waitlistTransitionOK(from, to string) bool {
	switch from {
	case WaitlistStatusNew:
		return to == WaitlistStatusContacted || to == WaitlistStatusRebooked || to == WaitlistStatusClosed
	case WaitlistStatusContacted:
		return to == WaitlistStatusRebooked || to == WaitlistStatusClosed
	case WaitlistStatusRebooked:
		return to == WaitlistStatusClosed
	case WaitlistStatusClosed:
		return to == WaitlistStatusContacted
	default:
		return false
	}
}

func (r *Repo) GetWaitlist(ctx context.Context, id int64) (WaitlistRow, error) {
	return r.getWaitlist(ctx, id)
}

type rowQuery interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func (r *Repo) getWaitlist(ctx context.Context, id int64) (WaitlistRow, error) {
	return scanWaitlist(ctx, r.db, id)
}

func scanWaitlist(ctx context.Context, q rowQuery, id int64) (WaitlistRow, error) {
	var row WaitlistRow
	err := q.QueryRowContext(ctx, `
SELECT id, planning_id, patient_id, appt_version, appt_start, reason, source, status,
       IFNULL(outcome,''), created_at, updated_at, created_at_msk, branch_id, IFNULL(updated_by,''), IFNULL(version,1)
FROM waitlist WHERE id = ?`, id).Scan(
		&row.ID, &row.PlanningID, &row.PatientID, &row.ApptVersion, &row.ApptStart,
		&row.Reason, &row.Source, &row.Status, &row.Outcome, &row.CreatedAt, &row.UpdatedAt,
		&row.CreatedAtMSK, &row.BranchID, &row.UpdatedBy, &row.Version,
	)
	if err == sql.ErrNoRows {
		return WaitlistRow{}, ErrWaitlistNotFound
	}
	return row, err
}
