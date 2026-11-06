package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

const waitlistRetainClosed = 180 * 24 * time.Hour

// PurgeClosedWaitlist deletes rebooked/closed rows older than 180 days.
// Returns the number of deleted rows (IDs only — nothing to log from the rows).
func PurgeClosedWaitlist(ctx context.Context, db *sql.DB, now time.Time) (int64, error) {
	if db == nil {
		return 0, nil
	}
	cutoff := now.UTC().Add(-waitlistRetainClosed).Format("2006-01-02T15:04:05Z")
	res, err := db.ExecContext(ctx, `
DELETE FROM waitlist
WHERE status IN ('rebooked', 'closed')
  AND updated_at < ?`, cutoff)
	if err != nil {
		return 0, fmt.Errorf("purge waitlist: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	return n, nil
}
