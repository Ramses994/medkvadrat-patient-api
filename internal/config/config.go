package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

type Config struct {
	Env          string
	LogLevel     string
	APIToken     string
	PollInterval time.Duration

	HTTP  HTTPConfig
	MSSQL MSSQLConfig
}

type HTTPConfig struct {
	ListenAddr string
}

type MSSQLConfig struct {
	Server   string
	Port     string
	Database string
	User     string
	Password string

	Encrypt                string
	TrustServerCertificate bool
}

func Load() (Config, error) {
	// Local dev convenience. In containers env is expected to be injected.
	_ = godotenv.Load()

	cfg := Config{
		Env:          strings.TrimSpace(getEnv("ENV", "dev")),
		LogLevel:     strings.TrimSpace(getEnv("LOG_LEVEL", "info")),
		APIToken:     strings.TrimSpace(os.Getenv("API_TOKEN")),
		PollInterval: mustDuration(getEnv("POLL_INTERVAL", "5s")),
		HTTP: HTTPConfig{
			ListenAddr: strings.TrimSpace(getEnv("API_PORT", ":8080")),
		},
		MSSQL: MSSQLConfig{
			Server:   strings.TrimSpace(os.Getenv("DB_SERVER")),
			Port:     strings.TrimSpace(getEnv("DB_PORT", "1433")),
			Database: strings.TrimSpace(os.Getenv("DB_NAME")),
			User:     strings.TrimSpace(os.Getenv("DB_USER")),
			Password: os.Getenv("DB_PASSWORD"),
			// Keep current default (LAN/dev). We will tighten in a later iteration.
			Encrypt:                strings.TrimSpace(getEnv("DB_ENCRYPT", "disable")),
			TrustServerCertificate: mustBool(getEnv("DB_TRUST_SERVER_CERT", "false")),
		},
	}

	if err := cfg.validate(); err != nil {
		// Avoid leaking secrets; print only which vars are missing.
		fmt.Fprintln(os.Stderr, err.Error())
		return Config{}, err
	}
	return cfg, nil
}

func (c Config) validate() error {
	var missing []string
	if c.MSSQL.Server == "" {
		missing = append(missing, "DB_SERVER")
	}
	if c.MSSQL.Database == "" {
		missing = append(missing, "DB_NAME")
	}
	if c.MSSQL.User == "" {
		missing = append(missing, "DB_USER")
	}
	if c.MSSQL.Password == "" {
		missing = append(missing, "DB_PASSWORD")
	}
	if c.HTTP.ListenAddr == "" {
		missing = append(missing, "API_PORT")
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing required env vars: %s", strings.Join(missing, ", "))
	}

	// In non-dev env, API_TOKEN must be set to protect endpoints.
	if c.Env != "dev" && c.APIToken == "" {
		return errors.New("missing required env var: API_TOKEN (required when ENV != dev)")
	}
	return nil
}

func getEnv(key, def string) string {
	if v := os.Getenv(key); strings.TrimSpace(v) != "" {
		return v
	}
	return def
}

func mustDuration(s string) time.Duration {
	d, err := time.ParseDuration(strings.TrimSpace(s))
	if err != nil {
		return 5 * time.Second
	}
	return d
}

func mustBool(s string) bool {
	v, err := strconv.ParseBool(strings.TrimSpace(s))
	if err != nil {
		return false
	}
	return v
}
