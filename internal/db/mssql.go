package db

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	_ "github.com/denisenkom/go-mssqldb"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/config"
)

func OpenMSSQL(cfg config.MSSQLConfig) (*sql.DB, error) {
	return openMSSQLWithCreds(cfg, cfg.User, cfg.Password)
}

// OpenMSSQLRW opens a write connection when DB_RW_USER is configured.
func OpenMSSQLRW(cfg config.MSSQLConfig) (*sql.DB, error) {
	user := cfg.RWUser
	if user == "" {
		return nil, nil
	}
	return openMSSQLWithCreds(cfg, user, cfg.RWPassword)
}

func openMSSQLWithCreds(cfg config.MSSQLConfig, user, password string) (*sql.DB, error) {
	connString := fmt.Sprintf(
		"server=%s;port=%s;database=%s;user id=%s;password=%s;encrypt=%s;trustservercertificate=%t;app name=%s",
		cfg.Server,
		cfg.Port,
		cfg.Database,
		user,
		password,
		cfg.Encrypt,
		cfg.TrustServerCertificate,
		escapeAppName(cfg.AppName),
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

func escapeAppName(name string) string {
	// ODBC app name must not contain ';'
	return strings.ReplaceAll(name, ";", "")
}
