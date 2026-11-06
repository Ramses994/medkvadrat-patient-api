package confirmations

import (
	"context"
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
func (r *Repo) ApplyWaitlist(ctx context.Context, id int64, version int, outcome, action, updatedBy string, now time.Time) (WaitlistRow, error) {
	action = strings.ToLower(strings.TrimSpace(action))
	outcome = strings.ToLower(strings.TrimSpace(outcome))
	updated := FormatWaitlistUTC(now)
	var (
		resErr error
		status string
	)
	if action == "reopen" {
		res, err := r.db.ExecContext(ctx, `
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
			return r.getWaitlist(ctx, id)
		}
		resErr = ErrWaitlistChanged
	} else if action != "" {
		return WaitlistRow{}, ErrUnknownWaitlistOutcome
	} else {
		status, resErr = StatusForOutcome(outcome)
		if resErr != nil {
			return WaitlistRow{}, resErr
		}
		res, err := r.db.ExecContext(ctx, `
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
			return r.getWaitlist(ctx, id)
		}
		resErr = ErrWaitlistChanged
	}
	fresh, err := r.getWaitlist(ctx, id)
	if err != nil {
		return WaitlistRow{}, err
	}
	return fresh, resErr
}

// ApplyMisWork is the same versioned save for a registry placeholder.
// A missing row is version 1 and status new.
func (r *Repo) ApplyMisWork(ctx context.Context, planningID int64, version int, outcome, action, updatedBy string, now time.Time) (MisWork, error) {
	if planningID <= 0 {
		return MisWork{}, ErrWaitlistNotFound
	}
	action = strings.ToLower(strings.TrimSpace(action))
	outcome = strings.ToLower(strings.TrimSpace(outcome))
	updated := FormatWaitlistUTC(now)
	current, ok, err := r.misWork(ctx, planningID)
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
		res, err := r.db.ExecContext(ctx, `
UPDATE waitlist_mis_work SET status = ?, outcome = '', updated_by = ?, updated_at = ?, version = version + 1
WHERE planning_id = ? AND version = ? AND status IN ('closed','rebooked')`,
			WaitlistStatusContacted, updatedBy, updated, planningID, version)
		if err != nil {
			return MisWork{}, fmt.Errorf("reopen mis work: %w", err)
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			fresh, _, _ := r.misWork(ctx, planningID)
			return fresh, ErrWaitlistChanged
		}
		fresh, _, err := r.misWork(ctx, planningID)
		return fresh, err
	}
	if action != "" {
		return MisWork{}, ErrUnknownWaitlistOutcome
	}
	status, err := StatusForOutcome(outcome)
	if err != nil {
		return MisWork{}, err
	}
	if current.Version != version || current.Status == WaitlistStatusClosed || current.Status == WaitlistStatusRebooked {
		return current, ErrWaitlistChanged
	}
	if !ok {
		_, err = r.db.ExecContext(ctx, `
INSERT INTO waitlist_mis_work (planning_id, status, outcome, updated_by, updated_at, version)
VALUES (?, ?, ?, ?, ?, 2)`, planningID, status, outcome, updatedBy, updated)
		if err != nil {
			if strings.Contains(strings.ToLower(err.Error()), "constraint") || strings.Contains(strings.ToLower(err.Error()), "unique") {
				fresh, _, _ := r.misWork(ctx, planningID)
				return fresh, ErrWaitlistChanged
			}
			return MisWork{}, fmt.Errorf("insert mis work: %w", err)
		}
		fresh, _, err := r.misWork(ctx, planningID)
		return fresh, err
	}
	res, err := r.db.ExecContext(ctx, `
UPDATE waitlist_mis_work SET status = ?, outcome = ?, updated_by = ?, updated_at = ?, version = version + 1
WHERE planning_id = ? AND version = ? AND status NOT IN ('closed','rebooked')`,
		status, outcome, updatedBy, updated, planningID, version)
	if err != nil {
		return MisWork{}, fmt.Errorf("apply mis work: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		fresh, _, _ := r.misWork(ctx, planningID)
		if fresh.PlanningID == 0 {
			fresh = current
		}
		return fresh, ErrWaitlistChanged
	}
	fresh, _, err := r.misWork(ctx, planningID)
	return fresh, err
}
