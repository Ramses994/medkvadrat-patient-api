package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// staffUsersDDL has no table-wide UNIQUE(login): logins are unique only among
// rows with deleted_at IS NULL (idx_staff_users_login_live).
const staffUsersDDL = `
CREATE TABLE IF NOT EXISTS %s (
  id                   INTEGER PRIMARY KEY AUTOINCREMENT,
  login                TEXT NOT NULL,
  full_name            TEXT NOT NULL,
  password_hash        TEXT NOT NULL,
  role                 TEXT NOT NULL,
  branches             TEXT NOT NULL,
  active               INTEGER NOT NULL DEFAULT 1,
  must_change_password INTEGER NOT NULL DEFAULT 0,
  created_at           TEXT NOT NULL,
  updated_at           TEXT NOT NULL,
  last_login_at        TEXT,
  session_version      INTEGER NOT NULL DEFAULT 1,
  deleted_at           TEXT
);`

const staffUsersColumns = `id, login, full_name, password_hash, role, branches, active, must_change_password,
  created_at, updated_at, last_login_at, session_version, deleted_at`

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
  used_at           DATETIME,
  format            INTEGER NOT NULL DEFAULT 0,
  issued_mode       TEXT NOT NULL DEFAULT '',
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
  issued_mode     TEXT NOT NULL DEFAULT '',
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
		`
CREATE TABLE IF NOT EXISTS waitlist (
  id              INTEGER PRIMARY KEY AUTOINCREMENT,
  planning_id     INTEGER NOT NULL,
  patient_id      INTEGER NOT NULL,
  appt_version    TEXT NOT NULL,
  appt_start      TEXT NOT NULL,
  reason          TEXT NOT NULL,
  source          TEXT NOT NULL DEFAULT 'max',
  status          TEXT NOT NULL,
  outcome         TEXT,
  created_at      TEXT NOT NULL,
  updated_at      TEXT NOT NULL,
  created_at_msk  TEXT NOT NULL,
  branch_id       INTEGER NOT NULL DEFAULT 0,
  updated_by      TEXT
);`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_waitlist_planning_version ON waitlist(planning_id, appt_version);`,
		`CREATE INDEX IF NOT EXISTS idx_waitlist_status_created ON waitlist(status, created_at);`,
		`CREATE INDEX IF NOT EXISTS idx_waitlist_branch_status ON waitlist(branch_id, status);`,
		`
CREATE TABLE IF NOT EXISTS lk_write_ops (
  op_id TEXT PRIMARY KEY,
  patient_id INTEGER NOT NULL,
  action TEXT NOT NULL,
  request_id TEXT NOT NULL,
  intent_json TEXT NOT NULL,
  context_json TEXT NOT NULL DEFAULT '',
  state TEXT NOT NULL,
  code TEXT NOT NULL DEFAULT '',
  planning_id INTEGER,
  started_at TEXT NOT NULL,
  started_at_msk TEXT NOT NULL,
  finished_at TEXT,
  resolved_by TEXT,
  resolve_note_code TEXT,
  unresolved_logged_at TEXT,
  UNIQUE(patient_id, action, request_id)
);`,
		`CREATE INDEX IF NOT EXISTS idx_lk_write_ops_patient_state ON lk_write_ops(patient_id, state);`,
		`
CREATE TABLE IF NOT EXISTS lk_health (
  id INTEGER PRIMARY KEY,
  checked_at TEXT NOT NULL
);`,
		`
CREATE TABLE IF NOT EXISTS waitlist_mis_work (
  planning_id INTEGER PRIMARY KEY,
  status      TEXT NOT NULL,
  outcome     TEXT,
  updated_by  TEXT,
  updated_at  TEXT NOT NULL
);`,
		fmt.Sprintf(staffUsersDDL, "staff_users"),
		`
CREATE TABLE IF NOT EXISTS staff_login_log (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  login      TEXT NOT NULL,
  ip         TEXT NOT NULL,
  success    INTEGER NOT NULL,
  created_at TEXT NOT NULL
);`,
		`CREATE INDEX IF NOT EXISTS idx_staff_login_log_login ON staff_login_log(login, id);`,
	}

	for i, s := range stmts {
		if _, err := db.ExecContext(ctx, s); err != nil {
			return fmt.Errorf("migrate stmt %d: %w", i, err)
		}
	}
	if err := ensureColumn(ctx, db, "appointment_confirmations", "appt_start", "TEXT"); err != nil {
		return err
	}
	if err := ensureColumn(ctx, db, "appointment_confirmations", "appt_version", "TEXT"); err != nil {
		return err
	}
	if err := ensureColumn(ctx, db, "staff_users", "session_version", "INTEGER NOT NULL DEFAULT 1"); err != nil {
		return err
	}
	if err := ensureColumn(ctx, db, "staff_users", "deleted_at", "TEXT"); err != nil {
		return err
	}
	if err := staffLoginUniqueAmongLive(ctx, db); err != nil {
		return err
	}
	if err := ensureColumn(ctx, db, "waitlist", "version", "INTEGER NOT NULL DEFAULT 1"); err != nil {
		return err
	}
	if err := ensureColumn(ctx, db, "waitlist_mis_work", "version", "INTEGER NOT NULL DEFAULT 1"); err != nil {
		return err
	}
	if err := ensureColumn(ctx, db, "otp_requests", "used_at", "DATETIME"); err != nil {
		return err
	}
	if err := ensureColumn(ctx, db, "otp_requests", "format", "INTEGER NOT NULL DEFAULT 0"); err != nil {
		return err
	}
	if err := ensureColumn(ctx, db, "otp_requests", "issued_mode", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := ensureColumn(ctx, db, "refresh_tokens", "issued_mode", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if _, err := db.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS staff_admin_log (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  actor_id INTEGER NOT NULL,
  actor_login TEXT NOT NULL,
  target_id INTEGER NOT NULL,
  change TEXT NOT NULL,
  created_at TEXT NOT NULL
)`); err != nil {
		return fmt.Errorf("staff admin log: %w", err)
	}
	if _, err := db.ExecContext(ctx, `
UPDATE staff_users SET branches = '*'
WHERE role IN ('admin', 'manager') AND branches <> '*'`); err != nil {
		return fmt.Errorf("normalize staff branches: %w", err)
	}
	if _, err := db.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS bot_consents (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  patient_id INTEGER NOT NULL,
  max_user_id INTEGER NOT NULL,
  kind TEXT NOT NULL CHECK (kind IN ('service', 'ads')),
  text_version TEXT NOT NULL,
  accepted_at TEXT NULL,
  revoked_at TEXT NULL,
  source TEXT NOT NULL DEFAULT 'bot'
)`); err != nil {
		return fmt.Errorf("bot consents: %w", err)
	}
	if _, err := db.ExecContext(ctx, `
CREATE INDEX IF NOT EXISTS idx_bot_consents_patient_kind ON bot_consents(patient_id, kind)`); err != nil {
		return fmt.Errorf("bot consents index: %w", err)
	}
	if _, err := db.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS bot_consent_revisions (
  patient_id INTEGER NOT NULL,
  kind TEXT NOT NULL,
  revision INTEGER NOT NULL,
  PRIMARY KEY (patient_id, kind)
)`); err != nil {
		return fmt.Errorf("bot consent revisions: %w", err)
	}
	if _, err := PurgeClosedWaitlist(ctx, db, time.Now().UTC()); err != nil {
		return err
	}
	return nil
}

// staffLoginUniqueAmongLive rebuilds an older staff_users whose login column
// carries an inline UNIQUE (SQLite cannot drop it in place), then adds the
// partial unique index. Ids, the AUTOINCREMENT counter and every column survive.
func staffLoginUniqueAmongLive(ctx context.Context, db *sql.DB) error {
	var inline int
	if err := db.QueryRowContext(ctx, `
SELECT COUNT(*) FROM sqlite_master
WHERE type = 'index' AND tbl_name = 'staff_users' AND name LIKE 'sqlite_autoindex_staff_users_%'`).Scan(&inline); err != nil {
		return fmt.Errorf("staff login index: %w", err)
	}
	if inline > 0 {
		if err := rebuildStaffUsers(ctx, db); err != nil {
			return fmt.Errorf("rebuild staff_users: %w", err)
		}
	}
	if _, err := db.ExecContext(ctx, `
CREATE UNIQUE INDEX IF NOT EXISTS idx_staff_users_login_live ON staff_users(login) WHERE deleted_at IS NULL`); err != nil {
		return fmt.Errorf("staff login index: %w", err)
	}
	return nil
}

func rebuildStaffUsers(ctx context.Context, db *sql.DB) error {
	conn, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = conn.ExecContext(context.Background(), "ROLLBACK")
		}
	}()
	var seq sql.NullInt64
	if err := conn.QueryRowContext(ctx, `SELECT MAX(seq) FROM sqlite_sequence WHERE name = 'staff_users'`).Scan(&seq); err != nil {
		return err
	}
	stmts := []string{
		`DROP TABLE IF EXISTS staff_users_rebuild`,
		fmt.Sprintf(staffUsersDDL, "staff_users_rebuild"),
		fmt.Sprintf(`INSERT INTO staff_users_rebuild (%s) SELECT %s FROM staff_users`, staffUsersColumns, staffUsersColumns),
		`DROP TABLE staff_users`,
		`ALTER TABLE staff_users_rebuild RENAME TO staff_users`,
	}
	for _, s := range stmts {
		if _, err := conn.ExecContext(ctx, s); err != nil {
			return err
		}
	}
	if seq.Valid {
		if _, err := conn.ExecContext(ctx, `
DELETE FROM sqlite_sequence WHERE name = 'staff_users' AND seq < ?`, seq.Int64); err != nil {
			return err
		}
		if _, err := conn.ExecContext(ctx, `
INSERT INTO sqlite_sequence (name, seq)
SELECT 'staff_users', ? WHERE NOT EXISTS (SELECT 1 FROM sqlite_sequence WHERE name = 'staff_users')`, seq.Int64); err != nil {
			return err
		}
	}
	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		return err
	}
	committed = true
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
