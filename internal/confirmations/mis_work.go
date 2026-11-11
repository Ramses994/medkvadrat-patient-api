package confirmations

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// MisWork is the staff state of a Medialog waitlist placeholder, keyed by planning_id.
type MisWork struct {
	PlanningID int64
	Status     string
	Outcome    string
	UpdatedBy  string
	UpdatedAt  string
	Version    int
}

func (r *Repo) MisWorkMap(ctx context.Context, planningIDs []int64) (map[int64]MisWork, error) {
	out := map[int64]MisWork{}
	if len(planningIDs) == 0 {
		return out, nil
	}
	q := `
SELECT planning_id, status, IFNULL(outcome,''), IFNULL(updated_by,''), updated_at, IFNULL(version,1)
FROM waitlist_mis_work WHERE planning_id IN (` + placeholders(len(planningIDs)) + `)`
	args := make([]any, len(planningIDs))
	for i, id := range planningIDs {
		args[i] = id
	}
	rs, err := r.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("mis work: %w", err)
	}
	defer rs.Close()
	for rs.Next() {
		var row MisWork
		if err := rs.Scan(&row.PlanningID, &row.Status, &row.Outcome, &row.UpdatedBy, &row.UpdatedAt, &row.Version); err != nil {
			return nil, err
		}
		out[row.PlanningID] = row
	}
	return out, rs.Err()
}

// UpdateMisWork stores status for a registry waitlist row.
// A missing row is treated as status new. The write is conditional on that status.
func (r *Repo) UpdateMisWork(ctx context.Context, planningID int64, status, outcome, updatedBy string, now time.Time, guard SQLGuard) (MisWork, error) {
	status = strings.ToLower(strings.TrimSpace(status))
	outcome = strings.ToLower(strings.TrimSpace(outcome))
	if planningID <= 0 {
		return MisWork{}, ErrWaitlistNotFound
	}
	if !ValidOutcome(outcome) {
		return MisWork{}, ErrUnknownWaitlistOutcome
	}
	current, ok, err := r.misWork(ctx, planningID)
	if err != nil {
		return MisWork{}, err
	}
	from := WaitlistStatusNew
	if ok {
		from = current.Status
	}
	if r.AfterMisWorkRead != nil {
		r.AfterMisWorkRead()
	}
	if !waitlistTransitionOK(from, status) {
		return MisWork{}, ErrInvalidWaitlistTransition
	}
	updated := FormatWaitlistUTC(now)
	var out MisWork
	err = r.withImmediate(ctx, ctx, guard, func(conn *sql.Conn) error {
		if !ok {
			_, err = conn.ExecContext(ctx, `
INSERT INTO waitlist_mis_work (planning_id, status, outcome, updated_by, updated_at)
VALUES (?, ?, ?, ?, ?)`, planningID, status, outcome, updatedBy, updated)
			if err != nil {
				if strings.Contains(strings.ToLower(err.Error()), "constraint") || strings.Contains(strings.ToLower(err.Error()), "unique") {
					return ErrWaitlistChanged
				}
				return fmt.Errorf("insert mis work: %w", err)
			}
			out = MisWork{PlanningID: planningID, Status: status, Outcome: outcome, UpdatedBy: updatedBy, UpdatedAt: updated}
			return nil
		}
		res, err := conn.ExecContext(ctx, `
UPDATE waitlist_mis_work SET status = ?, outcome = ?, updated_by = ?, updated_at = ?
WHERE planning_id = ? AND status = ?`,
			status, outcome, updatedBy, updated, planningID, from)
		if err != nil {
			return fmt.Errorf("update mis work: %w", err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if n == 0 {
			return ErrWaitlistChanged
		}
		out = MisWork{PlanningID: planningID, Status: status, Outcome: outcome, UpdatedBy: updatedBy, UpdatedAt: updated}
		return nil
	})
	if err != nil {
		return MisWork{}, err
	}
	return out, nil
}

func (r *Repo) misWork(ctx context.Context, planningID int64) (MisWork, bool, error) {
	return scanMisWork(ctx, r.db, planningID)
}

func scanMisWork(ctx context.Context, q rowQuery, planningID int64) (MisWork, bool, error) {
	var row MisWork
	err := q.QueryRowContext(ctx, `
SELECT planning_id, status, IFNULL(outcome,''), IFNULL(updated_by,''), updated_at, IFNULL(version,1)
FROM waitlist_mis_work WHERE planning_id = ?`, planningID).Scan(
		&row.PlanningID, &row.Status, &row.Outcome, &row.UpdatedBy, &row.UpdatedAt, &row.Version,
	)
	if err == sql.ErrNoRows {
		return MisWork{}, false, nil
	}
	if err != nil {
		return MisWork{}, false, err
	}
	return row, true, nil
}
