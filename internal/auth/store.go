package auth

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"time"
)

type RefreshStore struct {
	db *sql.DB
}

func NewRefreshStore(db *sql.DB) *RefreshStore { return &RefreshStore{db: db} }

func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func (s *RefreshStore) Put(ctx context.Context, jti string, patientID int64, tokenHash string, expiresAt time.Time) error {
	_, err := s.db.ExecContext(ctx, `
INSERT INTO refresh_tokens(jti, patient_id, token_hash, expires_at)
VALUES(@jti, @pid, @hash, @exp)`,
		sql.Named("jti", jti),
		sql.Named("pid", patientID),
		sql.Named("hash", tokenHash),
		sql.Named("exp", expiresAt.UTC()),
	)
	if err != nil {
		return fmt.Errorf("refresh put: %w", err)
	}
	return nil
}

func (s *RefreshStore) Get(ctx context.Context, jti string) (patientID int64, tokenHash string, expiresAt time.Time, revokedAt sql.NullTime, err error) {
	err = s.db.QueryRowContext(ctx, `
SELECT patient_id, token_hash, expires_at, revoked_at
FROM refresh_tokens
WHERE jti=@jti`,
		sql.Named("jti", jti),
	).Scan(&patientID, &tokenHash, &expiresAt, &revokedAt)
	return
}

func (s *RefreshStore) Revoke(ctx context.Context, jti string, now time.Time) error {
	_, err := s.db.ExecContext(ctx, `UPDATE refresh_tokens SET revoked_at=@t WHERE jti=@jti AND revoked_at IS NULL`,
		sql.Named("t", now.UTC()),
		sql.Named("jti", jti),
	)
	return err
}
