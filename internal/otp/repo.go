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
	CreatedAt       time.Time
}

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
INSERT INTO otp_requests(request_id, phone, code_hash, candidates, attempts, expires_at, verified_at, selected_patient, whitelisted)
VALUES(@rid, @phone, @hash, @cand, @att, @exp, @ver, @sel, @wl)`,
		sql.Named("rid", row.RequestID),
		sql.Named("phone", row.Phone),
		sql.Named("hash", row.CodeHash),
		sql.Named("cand", row.CandidatesJSON),
		sql.Named("att", row.Attempts),
		sql.Named("exp", row.ExpiresAt.UTC()),
		sql.Named("ver", nullTime(row.VerifiedAt)),
		sql.Named("sel", nullInt64(row.SelectedPatient)),
		sql.Named("wl", boolToInt(row.Whitelisted)),
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
SELECT request_id, phone, code_hash, candidates, attempts, expires_at, verified_at, selected_patient, whitelisted, created_at
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

func (r *Repo) MarkSelectedPatient(ctx context.Context, requestID string, patientID int64) error {
	_, err := r.db.ExecContext(ctx, `UPDATE otp_requests SET selected_patient=@p WHERE request_id=@rid`, sql.Named("p", patientID), sql.Named("rid", requestID))
	return err
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
