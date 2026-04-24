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
	// CancelMinHoursBefore defines the minimum time before appointment start
	// when patient cancellation is allowed.
	CancelMinHoursBefore int

	HTTP   HTTPConfig
	MSSQL  MSSQLConfig
	Auth   AuthConfig
	OTP    OTPConfig
	JWT    JWTConfig
	SMTP   SMTPConfig
	SQLite SQLiteConfig
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

type SQLiteConfig struct {
	Path string
}

type AuthConfig struct {
	Mode           string // dev | pilot | prod
	PilotWhitelist []string
}

type OTPConfig struct {
	TTLSeconds        int
	HMACSecret        string
	RateWindowSeconds int
	RatePhoneLimit    int
	RateIPLimit       int
}

type JWTConfig struct {
	Secret         string
	AccessTTLMin   int
	RefreshTTLDays int
	Issuer         string
}

type SMTPConfig struct {
	Host      string
	Port      int
	User      string
	Password  string
	FromName  string
	FromEmail string
	TLSMode   string // starttls|tls|none
}

func Load() (Config, error) {
	// Local dev convenience. In containers env is expected to be injected.
	_ = godotenv.Load()

	cfg := Config{
		Env:                  strings.TrimSpace(getEnv("ENV", "dev")),
		LogLevel:             strings.TrimSpace(getEnv("LOG_LEVEL", "info")),
		APIToken:             strings.TrimSpace(os.Getenv("API_TOKEN")),
		PollInterval:         mustDuration(getEnv("POLL_INTERVAL", "5s")),
		CancelMinHoursBefore: mustInt(getEnv("CANCEL_MIN_HOURS_BEFORE", "24"), 24),
		HTTP: HTTPConfig{
			ListenAddr: strings.TrimSpace(getEnv("API_PORT", ":8080")),
		},
		SQLite: SQLiteConfig{
			// GATEWAY_DB_PATH is preferred in Docker (/app/data/gateway.db); SQLITE_PATH is the legacy name.
			Path: firstNonEmpty(
				strings.TrimSpace(os.Getenv("GATEWAY_DB_PATH")),
				strings.TrimSpace(getEnv("SQLITE_PATH", "./gateway.db")),
			),
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
		Auth: AuthConfig{
			Mode:           strings.ToLower(strings.TrimSpace(getEnv("AUTH_MODE", "dev"))),
			PilotWhitelist: splitCSV(getEnv("AUTH_PILOT_WHITELIST", "")),
		},
		OTP: OTPConfig{
			TTLSeconds:        mustInt(getEnv("OTP_TTL_SECONDS", "300"), 300),
			HMACSecret:        strings.TrimSpace(os.Getenv("OTP_HMAC_SECRET")),
			RateWindowSeconds: mustInt(getEnv("OTP_RATE_WINDOW_SECONDS", "900"), 900),
			RatePhoneLimit:    mustInt(getEnv("OTP_RATE_PHONE_LIMIT", "3"), 3),
			RateIPLimit:       mustInt(getEnv("OTP_RATE_IP_LIMIT", "10"), 10),
		},
		JWT: JWTConfig{
			Secret:         strings.TrimSpace(os.Getenv("JWT_SECRET")),
			AccessTTLMin:   mustInt(getEnv("JWT_ACCESS_TTL_MIN", "15"), 15),
			RefreshTTLDays: mustInt(getEnv("JWT_REFRESH_TTL_DAYS", "30"), 30),
			Issuer:         strings.TrimSpace(getEnv("JWT_ISSUER", "medkvadrat-patient-api")),
		},
		SMTP: SMTPConfig{
			Host:      strings.TrimSpace(getEnv("SMTP_HOST", "")),
			Port:      mustInt(getEnv("SMTP_PORT", "587"), 587),
			User:      strings.TrimSpace(getEnv("SMTP_USER", "")),
			Password:  os.Getenv("SMTP_PASSWORD"),
			FromName:  strings.TrimSpace(getEnv("SMTP_FROM_NAME", "МедКвадрат")),
			FromEmail: strings.TrimSpace(getEnv("SMTP_FROM_EMAIL", "")),
			TLSMode:   strings.ToLower(strings.TrimSpace(getEnv("SMTP_TLS", "starttls"))),
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

	switch c.Auth.Mode {
	case "dev", "pilot", "prod":
	default:
		return fmt.Errorf("invalid AUTH_MODE: %s (allowed: dev|pilot|prod)", c.Auth.Mode)
	}

	if c.Auth.Mode != "dev" {
		if c.OTP.HMACSecret == "" {
			return errors.New("missing required env var: OTP_HMAC_SECRET (required when AUTH_MODE != dev)")
		}
		if c.JWT.Secret == "" {
			return errors.New("missing required env var: JWT_SECRET (required when AUTH_MODE != dev)")
		}
	}
	return nil
}

func mustInt(s string, def int) int {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return def
	}
	return n
}

func splitCSV(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func getEnv(key, def string) string {
	if v := os.Getenv(key); strings.TrimSpace(v) != "" {
		return v
	}
	return def
}

func firstNonEmpty(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
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
