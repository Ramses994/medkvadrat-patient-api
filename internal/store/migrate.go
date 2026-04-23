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
	}

	for i, s := range stmts {
		if _, err := db.ExecContext(ctx, s); err != nil {
			return fmt.Errorf("migrate stmt %d: %w", i, err)
		}
	}
	return nil
}
