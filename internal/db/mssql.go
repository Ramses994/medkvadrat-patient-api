package db

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	_ "github.com/denisenkom/go-mssqldb"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/config"
)

func OpenMSSQL(cfg config.MSSQLConfig) (*sql.DB, error) {
	connString := fmt.Sprintf(
		"server=%s;port=%s;database=%s;user id=%s;password=%s;encrypt=%s;trustservercertificate=%t",
		cfg.Server,
		cfg.Port,
		cfg.Database,
		cfg.User,
		cfg.Password,
		cfg.Encrypt,
		cfg.TrustServerCertificate,
	)

	db, err := sql.Open("sqlserver", connString)
	if err != nil {
		return nil, fmt.Errorf("sql open: %w", err)
	}

	db.SetMaxOpenConns(20)
	db.SetMaxIdleConns(10)
	db.SetConnMaxLifetime(30 * time.Minute)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("sql ping: %w", err)
	}

	return db, nil
}
