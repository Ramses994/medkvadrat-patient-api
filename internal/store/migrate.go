package store

import (
	"context"
	"database/sql"
	"fmt"
)

func Migrate(ctx context.Context, db *sql.DB) error {
	stmts := []string{
		`
CREATE TABLE IF NOT EXISTS otp_requests (
  request_id        TEXT PRIMARY KEY,
  phone             TEXT NOT NULL,
  code_hash         TEXT NOT NULL,
  candidates        TEXT NOT NULL,
  attempts          INTEGER NOT NULL DEFAULT 0,
  expires_at        DATETIME NOT NULL,
  verified_at       DATETIME,
  selected_patient  INTEGER,
  whitelisted       INTEGER NOT NULL DEFAULT 0,
  created_at        DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);`,
		`CREATE INDEX IF NOT EXISTS idx_otp_phone_created ON otp_requests(phone, created_at);`,
		`
CREATE TABLE IF NOT EXISTS refresh_tokens (
  jti             TEXT PRIMARY KEY,
  patient_id      INTEGER NOT NULL,
  token_hash      TEXT NOT NULL,
  expires_at      DATETIME NOT NULL,
  revoked_at      DATETIME,
  created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);`,
		`CREATE INDEX IF NOT EXISTS idx_refresh_patient ON refresh_tokens(patient_id);`,
		`
CREATE TABLE IF NOT EXISTS rate_limits (
  scope       TEXT NOT NULL,
  rl_key      TEXT NOT NULL,
  window_sec  INTEGER NOT NULL,
  limit_count INTEGER NOT NULL,
  count       INTEGER NOT NULL,
  window_start INTEGER NOT NULL,
  expires_at  INTEGER NOT NULL,
  PRIMARY KEY(scope, rl_key, window_sec, limit_count)
);`,
		`CREATE INDEX IF NOT EXISTS idx_rate_expires ON rate_limits(expires_at);`,
		`
CREATE TABLE IF NOT EXISTS appointment_confirmations (
  planning_id INTEGER PRIMARY KEY,
  status      TEXT NOT NULL,
  source      TEXT NOT NULL DEFAULT 'max',
  patient_id  INTEGER,
  updated_at  DATETIME NOT NULL
);`,
		`
CREATE TABLE IF NOT EXISTS medialog_write_log (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  request_id  TEXT,
  route       TEXT NOT NULL,
  patient_id  INTEGER,
  planning_id INTEGER,
  pl_subj_id  INTEGER,
  decision    TEXT NOT NULL,
  result      TEXT,
  error_code  TEXT
);`,
		`CREATE INDEX IF NOT EXISTS idx_medialog_write_log_created ON medialog_write_log(created_at);`,
	}

	for i, s := range stmts {
		if _, err := db.ExecContext(ctx, s); err != nil {
			return fmt.Errorf("migrate stmt %d: %w", i, err)
		}
	}
	if err := ensureColumn(ctx, db, "appointment_confirmations", "appt_start", "TEXT"); err != nil {
		return err
	}
	return nil
}

func ensureColumn(ctx context.Context, db *sql.DB, table, column, decl string) error {
	rows, err := db.QueryContext(ctx, fmt.Sprintf("PRAGMA table_info(%s)", table))
	if err != nil {
		return fmt.Errorf("pragma %s: %w", table, err)
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			return err
		}
		if name == column {
			return nil
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	_, err = db.ExecContext(ctx, fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s", table, column, decl))
	if err != nil {
		return fmt.Errorf("add column %s.%s: %w", table, column, err)
	}
	return nil
}
