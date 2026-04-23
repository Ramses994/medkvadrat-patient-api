package repo

import (
	"context"
	"database/sql"
)

type HealthRepo struct {
	db *sql.DB
}

func (r *HealthRepo) Ping(ctx context.Context) error {
	return r.db.PingContext(ctx)
}
