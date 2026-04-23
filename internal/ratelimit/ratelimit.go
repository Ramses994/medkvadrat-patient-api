package ratelimit

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

type Store struct {
	db *sql.DB
}

func NewStore(db *sql.DB) *Store { return &Store{db: db} }

// Allow increments counters for the given (scope,key) within a rolling fixed window.
// Returns allowed=false when limit exceeded.
func (s *Store) Allow(ctx context.Context, scope string, key string, window time.Duration, limit int, now time.Time) (bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()

	windowSec := int(window.Seconds())
	if windowSec <= 0 {
		windowSec = 1
	}

	var count int
	var windowStartUnix int64
	err = tx.QueryRowContext(ctx, `
SELECT count, window_start
FROM rate_limits
WHERE scope=@scope AND rl_key=@key AND window_sec=@ws AND limit_count=@lim`,
		sql.Named("scope", scope),
		sql.Named("key", key),
		sql.Named("ws", windowSec),
		sql.Named("lim", limit),
	).Scan(&count, &windowStartUnix)

	if err != nil && err != sql.ErrNoRows {
		return false, fmt.Errorf("rate select: %w", err)
	}

	nowUnix := now.UTC().Unix()
	if err == sql.ErrNoRows || (nowUnix-windowStartUnix) >= int64(windowSec) {
		count = 0
		windowStartUnix = nowUnix
	}

	if count >= limit {
		return false, nil
	}
	count++

	expiresAtUnix := windowStartUnix + int64(windowSec)
	_, err = tx.ExecContext(ctx, `
INSERT INTO rate_limits(scope, rl_key, window_sec, limit_count, count, window_start, expires_at)
VALUES(@scope, @key, @ws, @lim, @cnt, @start, @exp)
ON CONFLICT(scope, rl_key, window_sec, limit_count) DO UPDATE SET
  count=excluded.count,
  window_start=excluded.window_start,
  expires_at=excluded.expires_at`,
		sql.Named("scope", scope),
		sql.Named("key", key),
		sql.Named("ws", windowSec),
		sql.Named("lim", limit),
		sql.Named("cnt", count),
		sql.Named("start", windowStartUnix),
		sql.Named("exp", expiresAtUnix),
	)
	if err != nil {
		return false, fmt.Errorf("rate upsert: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}
