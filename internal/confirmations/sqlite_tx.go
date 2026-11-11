package confirmations

import (
	"context"
	"database/sql"
	"strings"
	"time"
)

// SQLGuard runs after BEGIN IMMEDIATE and before the write.
// A non-nil error rolls the transaction back.
type SQLGuard func(conn *sql.Conn) error

// WithImmediate takes the write lock, runs guard, then fn, on one connection.
// lockCtx bounds waiting for the lock. workCtx bounds the statements after it.
func (r *Repo) WithImmediate(lockCtx, workCtx context.Context, guard SQLGuard, fn func(*sql.Conn) error) error {
	return r.withImmediate(lockCtx, workCtx, guard, fn)
}

func (r *Repo) withImmediate(lockCtx, workCtx context.Context, guard SQLGuard, fn func(*sql.Conn) error) error {
	conn, err := r.db.Conn(lockCtx)
	if err != nil {
		return err
	}
	owned := true
	defer func() {
		if owned {
			conn.Close()
		}
	}()
	if err := beginImmediate(lockCtx, conn, &owned); err != nil {
		return err
	}
	committed := false
	defer func() {
		if owned && !committed {
			_, _ = conn.ExecContext(context.Background(), "ROLLBACK")
		}
	}()
	if guard != nil {
		if err := guard(conn); err != nil {
			return err
		}
	}
	if err := fn(conn); err != nil {
		return err
	}
	if _, err := conn.ExecContext(workCtx, "COMMIT"); err != nil {
		return err
	}
	committed = true
	return nil
}

// beginImmediate waits for the write lock until lockCtx ends.
// A shared-memory SQLite lock does not wake up when that context is cancelled,
// so the wait itself runs separately and a cancelled wait rolls the late lock back.
func beginImmediate(ctx context.Context, conn *sql.Conn, owned *bool) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		errCh := make(chan error, 1)
		go func() {
			_, err := conn.ExecContext(context.Background(), "BEGIN IMMEDIATE")
			errCh <- err
		}()
		select {
		case err := <-errCh:
			if err == nil {
				return nil
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if !sqliteBusy(err) {
				return err
			}
			timer := time.NewTimer(5 * time.Millisecond)
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
			}
		case <-ctx.Done():
			*owned = false
			go func() {
				if err := <-errCh; err == nil {
					_, _ = conn.ExecContext(context.Background(), "ROLLBACK")
				}
				conn.Close()
			}()
			return ctx.Err()
		}
	}
}

func sqliteBusy(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "sqlite_busy") || strings.Contains(msg, "database is locked")
}
