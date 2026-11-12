package consents

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"time"
)

const (
	KindService  = "service"
	KindAds      = "ads"
	StateActive  = "active"
	StateRevoked = "revoked"
	StateNone    = "none"
)

// KindState is the latest consent row for one kind.
// At is accepted_at while the row is active and revoked_at after revocation.
type KindState struct {
	State       string `json:"state"`
	TextVersion string `json:"text_version,omitempty"`
	At          string `json:"at,omitempty"`
}

// Snapshot is the body of GET and POST /api/internal/bot-consents.
type Snapshot struct {
	MessengerNotify *int      `json:"messenger_notify"`
	Service         KindState `json:"service"`
	Ads             KindState `json:"ads"`
}

// Input is one accept or revoke.
type Input struct {
	PatientID   int64
	MaxUserID   int64
	Kind        string
	TextVersion string
	Action      string
}

// Store keeps bot consents in gateway SQLite. Rows are never deleted.
type Store struct {
	db    *sql.DB
	now   func() time.Time
	Reads atomic.Int64
	// failBeforeInsert rolls the accept transaction back. Tests use it to
	// prove a version change does not leave the previous row revoked.
	failBeforeInsert func() error
}

func New(db *sql.DB) *Store {
	return &Store{db: db, now: time.Now}
}

// SetClock fixes accept and revoke timestamps. Production leaves it unset.
func (s *Store) SetClock(now func() time.Time) {
	if s != nil && now != nil {
		s.now = now
	}
}

// RowCount is the number of stored consent rows, including revoked ones.
func (s *Store) RowCount(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM bot_consents`).Scan(&n)
	return n, err
}

func (s *Store) clock() time.Time {
	if s == nil || s.now == nil {
		return time.Now()
	}
	return s.now()
}

type row struct {
	ID          int64
	TextVersion string
	AcceptedAt  sql.NullString
	RevokedAt   sql.NullString
}

func (s *Store) Current(ctx context.Context, patientID int64) (KindState, KindState, error) {
	if s == nil || s.db == nil {
		return none(), none(), errors.New("consents store is not configured")
	}
	service, err := s.latest(ctx, s.db, patientID, KindService)
	if err != nil {
		return KindState{}, KindState{}, err
	}
	ads, err := s.latest(ctx, s.db, patientID, KindAds)
	if err != nil {
		return KindState{}, KindState{}, err
	}
	return toState(service), toState(ads), nil
}

// ServiceStates returns active, revoked, or omits patients with no service row.
// One SQLite query for the whole list.
func (s *Store) ServiceStates(ctx context.Context, patientIDs []int64) (map[int64]string, error) {
	out := map[int64]string{}
	if s == nil || s.db == nil {
		return nil, errors.New("consents store is not configured")
	}
	ids := uniqueIDs(patientIDs)
	if len(ids) == 0 {
		return out, nil
	}
	holders := make([]string, len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		holders[i] = "?"
		args[i] = id
	}
	q := `
SELECT c.patient_id, c.revoked_at
FROM bot_consents c
JOIN (
  SELECT patient_id, MAX(id) AS id
  FROM bot_consents
  WHERE kind = 'service' AND patient_id IN (` + strings.Join(holders, ",") + `)
  GROUP BY patient_id
) latest ON latest.id = c.id`
	s.Reads.Add(1)
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("consent states: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var patientID int64
		var revoked sql.NullString
		if err := rows.Scan(&patientID, &revoked); err != nil {
			return nil, err
		}
		if revoked.Valid && strings.TrimSpace(revoked.String) != "" {
			out[patientID] = StateRevoked
		} else {
			out[patientID] = StateActive
		}
	}
	return out, rows.Err()
}

// AdsRows is the latest ads row per patient, active and revoked. Service rows are not returned.
func (s *Store) AdsRows(ctx context.Context) ([]AdsRow, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("consents store is not configured")
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT c.patient_id, c.accepted_at, c.revoked_at
FROM bot_consents c
JOIN (
  SELECT patient_id, MAX(id) AS id
  FROM bot_consents
  WHERE kind = 'ads'
  GROUP BY patient_id
) latest ON latest.id = c.id
ORDER BY c.patient_id`)
	if err != nil {
		return nil, fmt.Errorf("ads consents: %w", err)
	}
	defer rows.Close()
	var out []AdsRow
	for rows.Next() {
		var row AdsRow
		var accepted, revoked sql.NullString
		if err := rows.Scan(&row.PatientID, &accepted, &revoked); err != nil {
			return nil, err
		}
		if accepted.Valid {
			row.AcceptedAt = accepted.String
		}
		if revoked.Valid {
			row.RevokedAt = revoked.String
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// AdsRow is one latest ads consent. It has no name and no phone.
type AdsRow struct {
	PatientID  int64
	AcceptedAt string
	RevokedAt  string
}

func (s *Store) Apply(ctx context.Context, in Input) (KindState, KindState, error) {
	if s == nil || s.db == nil {
		return KindState{}, KindState{}, errors.New("consents store is not configured")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return KindState{}, KindState{}, err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	latest, err := s.latest(ctx, tx, in.PatientID, in.Kind)
	if err != nil {
		return KindState{}, KindState{}, err
	}
	now := s.clock().UTC().Format(time.RFC3339)
	switch in.Action {
	case "accept":
		if latest != nil && !latest.RevokedAt.Valid && latest.TextVersion == in.TextVersion {
			if err := tx.Commit(); err != nil {
				return KindState{}, KindState{}, err
			}
			committed = true
			return s.Current(ctx, in.PatientID)
		}
		if latest != nil && !latest.RevokedAt.Valid {
			if _, err := tx.ExecContext(ctx, `
UPDATE bot_consents SET revoked_at = ? WHERE id = ? AND revoked_at IS NULL`, now, latest.ID); err != nil {
				return KindState{}, KindState{}, err
			}
			if s.failBeforeInsert != nil {
				if err := s.failBeforeInsert(); err != nil {
					return KindState{}, KindState{}, err
				}
			}
		}
		if _, err := tx.ExecContext(ctx, `
INSERT INTO bot_consents (patient_id, max_user_id, kind, text_version, accepted_at, source)
VALUES (?, ?, ?, ?, ?, 'bot')`, in.PatientID, in.MaxUserID, in.Kind, in.TextVersion, now); err != nil {
			return KindState{}, KindState{}, err
		}
	case "revoke":
		if latest != nil && latest.RevokedAt.Valid && strings.TrimSpace(latest.RevokedAt.String) != "" {
			if err := tx.Commit(); err != nil {
				return KindState{}, KindState{}, err
			}
			committed = true
			return s.Current(ctx, in.PatientID)
		}
		if latest == nil {
			if _, err := tx.ExecContext(ctx, `
INSERT INTO bot_consents (patient_id, max_user_id, kind, text_version, accepted_at, revoked_at, source)
VALUES (?, ?, ?, ?, NULL, ?, 'bot')`, in.PatientID, in.MaxUserID, in.Kind, in.TextVersion, now); err != nil {
				return KindState{}, KindState{}, err
			}
		} else if _, err := tx.ExecContext(ctx, `
UPDATE bot_consents SET revoked_at = ? WHERE id = ? AND revoked_at IS NULL`, now, latest.ID); err != nil {
			return KindState{}, KindState{}, err
		}
	default:
		return KindState{}, KindState{}, fmt.Errorf("unknown action")
	}
	if err := tx.Commit(); err != nil {
		return KindState{}, KindState{}, err
	}
	committed = true
	return s.Current(ctx, in.PatientID)
}

type queryer interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func (s *Store) latest(ctx context.Context, q queryer, patientID int64, kind string) (*row, error) {
	var out row
	err := q.QueryRowContext(ctx, `
SELECT id, text_version, accepted_at, revoked_at
FROM bot_consents
WHERE patient_id = ? AND kind = ?
ORDER BY id DESC
LIMIT 1`, patientID, kind).Scan(&out.ID, &out.TextVersion, &out.AcceptedAt, &out.RevokedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &out, nil
}

func toState(r *row) KindState {
	if r == nil {
		return none()
	}
	if r.RevokedAt.Valid && strings.TrimSpace(r.RevokedAt.String) != "" {
		return KindState{State: StateRevoked, TextVersion: r.TextVersion, At: r.RevokedAt.String}
	}
	at := ""
	if r.AcceptedAt.Valid {
		at = r.AcceptedAt.String
	}
	return KindState{State: StateActive, TextVersion: r.TextVersion, At: at}
}

func none() KindState { return KindState{State: StateNone} }

func uniqueIDs(ids []int64) []int64 {
	seen := make(map[int64]struct{}, len(ids))
	out := make([]int64, 0, len(ids))
	for _, id := range ids {
		if id <= 0 {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}
