package confirmations

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// StatusForOutcome maps a call result to the row status. The client does not send status.
func StatusForOutcome(outcome string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(outcome)) {
	case "no_answer", "call_back":
		return WaitlistStatusContacted, nil
	case "rebooked":
		return WaitlistStatusRebooked, nil
	case "refused", "other":
		return WaitlistStatusClosed, nil
	default:
		return "", ErrUnknownWaitlistOutcome
	}
}

// ApplyWaitlist saves an outcome or reopens a closed row. The write matches version.
// guard runs after the write lock is held and before UPDATE. A nil guard is the non-dashboard path.
func (r *Repo) ApplyWaitlist(ctx context.Context, id int64, version int, outcome, action, updatedBy string, now time.Time, guard SQLGuard) (WaitlistRow, error) {
	action = strings.ToLower(strings.TrimSpace(action))
	outcome = strings.ToLower(strings.TrimSpace(outcome))
	if action != "" && action != "reopen" {
		return WaitlistRow{}, ErrUnknownWaitlistOutcome
	}
	status := ""
	if action == "" {
		var err error
		status, err = StatusForOutcome(outcome)
		if err != nil {
			return WaitlistRow{}, err
		}
	}
	updated := FormatWaitlistUTC(now)
	var out WaitlistRow
	err := r.withImmediate(ctx, ctx, guard, func(conn *sql.Conn) error {
		var applyErr error
		out, applyErr = applyWaitlistConn(ctx, conn, id, version, outcome, action, status, updated, updatedBy)
		return applyErr
	})
	if err != nil && out.ID == 0 {
		return WaitlistRow{}, err
	}
	return out, err
}

func applyWaitlistConn(ctx context.Context, conn *sql.Conn, id int64, version int, outcome, action, status, updated, updatedBy string) (WaitlistRow, error) {
	if action == "reopen" {
		res, err := conn.ExecContext(ctx, `
UPDATE waitlist SET status = ?, outcome = '', updated_at = ?, updated_by = ?, version = version + 1
WHERE id = ? AND version = ? AND status IN ('closed','rebooked')`,
			WaitlistStatusContacted, updated, updatedBy, id, version)
		if err != nil {
			return WaitlistRow{}, fmt.Errorf("reopen waitlist: %w", err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			return WaitlistRow{}, err
		}
		if n == 1 {
			return scanWaitlist(ctx, conn, id)
		}
		fresh, err := scanWaitlist(ctx, conn, id)
		if err != nil {
			return WaitlistRow{}, err
		}
		return fresh, ErrWaitlistChanged
	}
	res, err := conn.ExecContext(ctx, `
UPDATE waitlist SET status = ?, outcome = ?, updated_at = ?, updated_by = ?, version = version + 1
WHERE id = ? AND version = ? AND status NOT IN ('closed','rebooked')`,
		status, outcome, updated, updatedBy, id, version)
	if err != nil {
		return WaitlistRow{}, fmt.Errorf("apply waitlist: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return WaitlistRow{}, err
	}
	if n == 1 {
		return scanWaitlist(ctx, conn, id)
	}
	fresh, err := scanWaitlist(ctx, conn, id)
	if err != nil {
		return WaitlistRow{}, err
	}
	return fresh, ErrWaitlistChanged
}

// ApplyMisWork is the same versioned save for a registry placeholder.
// A missing row is version 1 and status new.
func (r *Repo) ApplyMisWork(ctx context.Context, planningID int64, version int, outcome, action, updatedBy string, now time.Time, guard SQLGuard) (MisWork, error) {
	if planningID <= 0 {
		return MisWork{}, ErrWaitlistNotFound
	}
	action = strings.ToLower(strings.TrimSpace(action))
	outcome = strings.ToLower(strings.TrimSpace(outcome))
	if action != "" && action != "reopen" {
		return MisWork{}, ErrUnknownWaitlistOutcome
	}
	if action == "" {
		if _, err := StatusForOutcome(outcome); err != nil {
			return MisWork{}, err
		}
	}
	updated := FormatWaitlistUTC(now)
	var out MisWork
	err := r.withImmediate(ctx, ctx, guard, func(conn *sql.Conn) error {
		var applyErr error
		out, applyErr = applyMisWorkConn(ctx, conn, planningID, version, outcome, action, updated, updatedBy)
		return applyErr
	})
	if err != nil && out.PlanningID == 0 {
		return MisWork{}, err
	}
	return out, err
}

func applyMisWorkConn(ctx context.Context, conn *sql.Conn, planningID int64, version int, outcome, action, updated, updatedBy string) (MisWork, error) {
	current, ok, err := scanMisWork(ctx, conn, planningID)
	if err != nil {
		return MisWork{}, err
	}
	if !ok {
		current = MisWork{PlanningID: planningID, Status: WaitlistStatusNew, Version: 1}
	}
	if action == "reopen" {
		if !ok || current.Version != version || (current.Status != WaitlistStatusClosed && current.Status != WaitlistStatusRebooked) {
			return current, ErrWaitlistChanged
		}
		res, err := conn.ExecContext(ctx, `
UPDATE waitlist_mis_work SET status = ?, outcome = '', updated_by = ?, updated_at = ?, version = version + 1
WHERE planning_id = ? AND version = ? AND status IN ('closed','rebooked')`,
			WaitlistStatusContacted, updatedBy, updated, planningID, version)
		if err != nil {
			return MisWork{}, fmt.Errorf("reopen mis work: %w", err)
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			fresh, found, _ := scanMisWork(ctx, conn, planningID)
			if !found {
				fresh = current
			}
			return fresh, ErrWaitlistChanged
		}
		return scanMisWorkFound(ctx, conn, planningID)
	}
	status, err := StatusForOutcome(outcome)
	if err != nil {
		return MisWork{}, err
	}
	if current.Version != version || current.Status == WaitlistStatusClosed || current.Status == WaitlistStatusRebooked {
		return current, ErrWaitlistChanged
	}
	if !ok {
		_, err = conn.ExecContext(ctx, `
INSERT INTO waitlist_mis_work (planning_id, status, outcome, updated_by, updated_at, version)
VALUES (?, ?, ?, ?, ?, 2)`, planningID, status, outcome, updatedBy, updated)
		if err != nil {
			if strings.Contains(strings.ToLower(err.Error()), "constraint") || strings.Contains(strings.ToLower(err.Error()), "unique") {
				fresh, found, _ := scanMisWork(ctx, conn, planningID)
				if !found {
					fresh = current
				}
				return fresh, ErrWaitlistChanged
			}
			return MisWork{}, fmt.Errorf("insert mis work: %w", err)
		}
		return scanMisWorkFound(ctx, conn, planningID)
	}
	res, err := conn.ExecContext(ctx, `
UPDATE waitlist_mis_work SET status = ?, outcome = ?, updated_by = ?, updated_at = ?, version = version + 1
WHERE planning_id = ? AND version = ? AND status NOT IN ('closed','rebooked')`,
		status, outcome, updatedBy, updated, planningID, version)
	if err != nil {
		return MisWork{}, fmt.Errorf("apply mis work: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		fresh, found, _ := scanMisWork(ctx, conn, planningID)
		if !found {
			fresh = current
		}
		return fresh, ErrWaitlistChanged
	}
	return scanMisWorkFound(ctx, conn, planningID)
}

func scanMisWorkFound(ctx context.Context, conn *sql.Conn, planningID int64) (MisWork, error) {
	row, _, err := scanMisWork(ctx, conn, planningID)
	return row, err
}
