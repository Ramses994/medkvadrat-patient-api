package otp

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

type RequestRow struct {
	RequestID       string
	Phone           string
	CodeHash        string
	CandidatesJSON  string
	Attempts        int
	ExpiresAt       time.Time
	VerifiedAt      sql.NullTime
	SelectedPatient sql.NullInt64
	Whitelisted     bool
	UsedAt          sql.NullTime
	CreatedAt       time.Time
	Format          int
}

// FormatAddresses marks a row that stores one code per mailbox. Rows written before it have format 0.
const FormatAddresses = 2

var ErrOldFormat = errors.New("otp request has the old format")

// AddressGroup is one mailbox: its code hash and the profiles that share it.
type AddressGroup struct {
	CodeHash   string           `json:"code_hash"`
	Candidates []CandidatesItem `json:"candidates"`
}

var ErrConsumed = errors.New("otp request already used")

type CandidatesItem struct {
	PatientID   int64  `json:"patient_id"`
	FullName    string `json:"full_name"`
	MaskedEmail string `json:"masked_email"`
}

type Repo struct {
	db *sql.DB
}

func NewRepo(db *sql.DB) *Repo { return &Repo{db: db} }

func (r *Repo) Create(ctx context.Context, row RequestRow) error {
	_, err := r.db.ExecContext(ctx, `
INSERT INTO otp_requests(request_id, phone, code_hash, candidates, attempts, expires_at, verified_at, selected_patient, whitelisted, format)
VALUES(@rid, @phone, @hash, @cand, @att, @exp, @ver, @sel, @wl, @fmt)`,
		sql.Named("rid", row.RequestID),
		sql.Named("phone", row.Phone),
		sql.Named("hash", row.CodeHash),
		sql.Named("cand", row.CandidatesJSON),
		sql.Named("att", row.Attempts),
		sql.Named("exp", row.ExpiresAt.UTC()),
		sql.Named("ver", nullTime(row.VerifiedAt)),
		sql.Named("sel", nullInt64(row.SelectedPatient)),
		sql.Named("wl", boolToInt(row.Whitelisted)),
		sql.Named("fmt", row.Format),
	)
	if err != nil {
		return fmt.Errorf("insert otp request: %w", err)
	}
	return nil
}

func (r *Repo) Delete(ctx context.Context, requestID string) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM otp_requests WHERE request_id = @rid`, sql.Named("rid", requestID))
	return err
}

func (r *Repo) Get(ctx context.Context, requestID string) (RequestRow, error) {
	var row RequestRow
	var wlInt int
	err := r.db.QueryRowContext(ctx, `
SELECT request_id, phone, code_hash, candidates, attempts, expires_at, verified_at, selected_patient, whitelisted, created_at, used_at, format
FROM otp_requests
WHERE request_id = @rid`,
		sql.Named("rid", requestID),
	).Scan(
		&row.RequestID,
		&row.Phone,
		&row.CodeHash,
		&row.CandidatesJSON,
		&row.Attempts,
		&row.ExpiresAt,
		&row.VerifiedAt,
		&row.SelectedPatient,
		&wlInt,
		&row.CreatedAt,
		&row.UsedAt,
		&row.Format,
	)
	if err != nil {
		return RequestRow{}, err
	}
	row.Whitelisted = wlInt != 0
	return row, nil
}

func (r *Repo) IncrementAttempts(ctx context.Context, requestID string) (int, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	var attempts int
	if err := tx.QueryRowContext(ctx, `SELECT attempts FROM otp_requests WHERE request_id=@rid`, sql.Named("rid", requestID)).Scan(&attempts); err != nil {
		return 0, err
	}
	attempts++
	if _, err := tx.ExecContext(ctx, `UPDATE otp_requests SET attempts=@a WHERE request_id=@rid`, sql.Named("a", attempts), sql.Named("rid", requestID)); err != nil {
		return 0, err
	}
	return attempts, tx.Commit()
}

func (r *Repo) MarkVerified(ctx context.Context, requestID string, t time.Time) error {
	_, err := r.db.ExecContext(ctx, `UPDATE otp_requests SET verified_at=@t WHERE request_id=@rid`, sql.Named("t", t.UTC()), sql.Named("rid", requestID))
	return err
}

// LockAddress keeps only the mailbox whose code matched. A second code cannot reopen the others.
func (r *Repo) LockAddress(ctx context.Context, requestID string, group AddressGroup, t time.Time) (bool, error) {
	payload, err := json.Marshal([]AddressGroup{group})
	if err != nil {
		return false, err
	}
	res, err := r.db.ExecContext(ctx, `
UPDATE otp_requests
SET verified_at=@t, candidates=@c, code_hash=@h
WHERE request_id=@rid AND verified_at IS NULL AND used_at IS NULL`,
		sql.Named("t", t.UTC()),
		sql.Named("c", string(payload)),
		sql.Named("h", group.CodeHash),
		sql.Named("rid", requestID),
	)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

// Consume marks the request used and records the profile. It succeeds once, in the caller's transaction.
func (r *Repo) Consume(ctx context.Context, tx *sql.Tx, requestID string, patientID int64, t time.Time) error {
	res, err := tx.ExecContext(ctx, `
UPDATE otp_requests
SET used_at=@t, selected_patient=@p
WHERE request_id=@rid AND used_at IS NULL AND verified_at IS NOT NULL`,
		sql.Named("t", t.UTC()),
		sql.Named("p", patientID),
		sql.Named("rid", requestID),
	)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrConsumed
	}
	return nil
}

func (r *Repo) MarkSelectedPatient(ctx context.Context, requestID string, patientID int64) error {
	_, err := r.db.ExecContext(ctx, `UPDATE otp_requests SET selected_patient=@p WHERE request_id=@rid`, sql.Named("p", patientID), sql.Named("rid", requestID))
	return err
}

func DecodeAddresses(row RequestRow) ([]AddressGroup, error) {
	if row.Format != FormatAddresses {
		return nil, ErrOldFormat
	}
	raw := strings.TrimSpace(row.CandidatesJSON)
	if raw == "" {
		return nil, errors.New("empty candidates")
	}
	var groups []AddressGroup
	if err := json.Unmarshal([]byte(raw), &groups); err != nil {
		return nil, err
	}
	if len(groups) == 0 {
		return nil, errors.New("empty candidates")
	}
	for _, g := range groups {
		if g.CodeHash == "" || len(g.Candidates) == 0 {
			return nil, errors.New("empty address group")
		}
	}
	return groups, nil
}

func (r *Repo) DecodeCandidates(row RequestRow) ([]CandidatesItem, error) {
	if strings.TrimSpace(row.CandidatesJSON) == "" {
		return nil, errors.New("empty candidates")
	}
	var c []CandidatesItem
	if err := json.Unmarshal([]byte(row.CandidatesJSON), &c); err != nil {
		return nil, err
	}
	return c, nil
}

func nullTime(nt sql.NullTime) any {
	if nt.Valid {
		return nt.Time
	}
	return nil
}

func nullInt64(n sql.NullInt64) any {
	if n.Valid {
		return n.Int64
	}
	return nil
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
