package auth

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"time"
)

// Client is who holds a refresh token: the mobile client gets it in the body, the web cabinet in a cookie.
// A token is accepted only from the client it was issued to.
type Client string

const (
	ClientMobile Client = ""
	ClientWeb    Client = "web"
)

type RefreshStore struct {
	db *sql.DB
}

func NewRefreshStore(db *sql.DB) *RefreshStore { return &RefreshStore{db: db} }

func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

type Execer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

// RefreshRow is a stored refresh token. Mode is the AUTH_MODE it was issued in; rows written before it have "".
type RefreshRow struct {
	PatientID int64
	TokenHash string
	ExpiresAt time.Time
	RevokedAt sql.NullTime
	Mode      string
	Client    Client
}

func (s *RefreshStore) Put(ctx context.Context, jti string, patientID int64, tokenHash, mode string, expiresAt time.Time) error {
	return InsertRefresh(ctx, s.db, jti, patientID, tokenHash, mode, ClientMobile, expiresAt)
}

func InsertRefresh(ctx context.Context, exec Execer, jti string, patientID int64, tokenHash, mode string, client Client, expiresAt time.Time) error {
	_, err := exec.ExecContext(ctx, `
INSERT INTO refresh_tokens(jti, patient_id, token_hash, expires_at, issued_mode, client)
VALUES(@jti, @pid, @hash, @exp, @mode, @client)`,
		sql.Named("jti", jti),
		sql.Named("pid", patientID),
		sql.Named("hash", tokenHash),
		sql.Named("exp", expiresAt.UTC()),
		sql.Named("mode", mode),
		sql.Named("client", string(client)),
	)
	if err != nil {
		return fmt.Errorf("refresh put: %w", err)
	}
	return nil
}

func (s *RefreshStore) Get(ctx context.Context, jti string) (RefreshRow, error) {
	var row RefreshRow
	var client string
	err := s.db.QueryRowContext(ctx, `
SELECT patient_id, token_hash, expires_at, revoked_at, issued_mode, client
FROM refresh_tokens
WHERE jti=@jti`,
		sql.Named("jti", jti),
	).Scan(&row.PatientID, &row.TokenHash, &row.ExpiresAt, &row.RevokedAt, &row.Mode, &client)
	row.Client = Client(client)
	return row, err
}

func (s *RefreshStore) Revoke(ctx context.Context, jti string, now time.Time) error {
	_, err := s.db.ExecContext(ctx, `UPDATE refresh_tokens SET revoked_at=@t WHERE jti=@jti AND revoked_at IS NULL`,
		sql.Named("t", now.UTC()),
		sql.Named("jti", jti),
	)
	return err
}

// RevokeOnce revokes a live token and reports whether this call did it, so one token rotates once.
func RevokeOnce(ctx context.Context, exec Execer, jti string, now time.Time) (bool, error) {
	res, err := exec.ExecContext(ctx, `UPDATE refresh_tokens SET revoked_at=@t WHERE jti=@jti AND revoked_at IS NULL`,
		sql.Named("t", now.UTC()),
		sql.Named("jti", jti),
	)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}
