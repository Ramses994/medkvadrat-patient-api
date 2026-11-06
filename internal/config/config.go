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

	MedialogWriteEnabled       bool
	MedialogWriteAllowPatients []int64
	MedialogWriteAllowPlSubj   []int64
	MotconsuPollEnabled        bool
	CORSAllowedOrigins         []string
	WaitlistExamIDs            []int64
	BookingPairs               []BookingPair
	SlotTokenSecret            string
	BookingMinHoursAhead       int
	BookingMaxDays             int
	BookingMaxFuture           int
	LKAuthorMedecinsID         int64
	LKAuthorMeddepID           int64
	MedialogRWTimeout          time.Duration
	WaitlistMisDaysBack        int
	WaitlistMisDaysAhead       int

	HTTP      HTTPConfig
	MSSQL     MSSQLConfig
	Auth      AuthConfig
	OTP       OTPConfig
	JWT       JWTConfig
	SMTP      SMTPConfig
	SQLite    SQLiteConfig
	Dashboard DashboardConfig
}

// BookingPair is one pilot line and exam (LK_BOOKING_PAIRS).
type BookingPair struct {
	PlSubjID int64
	PlExamID int64
}

type DashboardConfig struct {
	Password     string
	AdminLogin   string
	Secret       string
	SessionTTL   time.Duration
	CookieSecure bool
	AllowedCIDRs []string
}

type HTTPConfig struct {
	ListenAddr string
}

type MSSQLConfig struct {
	Server     string
	Port       string
	Database   string
	User       string
	Password   string
	RWUser     string
	RWPassword string
	AppName    string

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
		Env:                        strings.TrimSpace(getEnv("ENV", "dev")),
		LogLevel:                   strings.TrimSpace(getEnv("LOG_LEVEL", "info")),
		APIToken:                   strings.TrimSpace(os.Getenv("API_TOKEN")),
		PollInterval:               mustDuration(getEnv("POLL_INTERVAL", "5s")),
		CancelMinHoursBefore:       mustInt(getEnv("CANCEL_MIN_HOURS_BEFORE", "24"), 24),
		MedialogWriteEnabled:       mustBool(getEnv("MEDIALOG_WRITE_ENABLED", "false")),
		MedialogWriteAllowPatients: mustInt64CSV(os.Getenv("MEDIALOG_WRITE_ALLOW_PATIENTS")),
		MedialogWriteAllowPlSubj:   mustInt64CSV(os.Getenv("MEDIALOG_WRITE_ALLOW_PL_SUBJ")),
		MotconsuPollEnabled:        mustBool(getEnv("MOTCONSU_POLL_ENABLED", "false")),
		CORSAllowedOrigins:         splitCSV(getEnv("CORS_ALLOWED_ORIGINS", "")),
		WaitlistExamIDs:            mustInt64CSV(os.Getenv("WAITLIST_EXAM_IDS")),
		BookingPairs:               ParseBookingPairs(os.Getenv("LK_BOOKING_PAIRS")),
		SlotTokenSecret:            os.Getenv("SLOT_TOKEN_SECRET"),
		BookingMinHoursAhead:       positiveOr(mustInt(getEnv("BOOKING_MIN_HOURS_AHEAD", "2"), 2), 2),
		BookingMaxDays:             positiveOr(mustInt(getEnv("BOOKING_MAX_DAYS", "14"), 14), 14),
		BookingMaxFuture:           positiveOr(mustInt(getEnv("BOOKING_MAX_FUTURE_PER_PATIENT", "3"), 3), 3),
		LKAuthorMedecinsID:         authorMedecinsID(),
		LKAuthorMeddepID:           authorMeddepID(),
		MedialogRWTimeout:          time.Duration(positiveOr(mustInt(getEnv("MEDIALOG_RW_TIMEOUT_SECONDS", "30"), 30), 30)) * time.Second,
		WaitlistMisDaysBack:        mustInt(getEnv("WAITLIST_MIS_DAYS_BACK", "7"), 7),
		WaitlistMisDaysAhead:       mustInt(getEnv("WAITLIST_MIS_DAYS_AHEAD", "60"), 60),
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
			Server:     strings.TrimSpace(os.Getenv("DB_SERVER")),
			Port:       strings.TrimSpace(getEnv("DB_PORT", "1433")),
			Database:   strings.TrimSpace(os.Getenv("DB_NAME")),
			User:       strings.TrimSpace(os.Getenv("DB_USER")),
			Password:   os.Getenv("DB_PASSWORD"),
			RWUser:     strings.TrimSpace(os.Getenv("DB_RW_USER")),
			RWPassword: os.Getenv("DB_RW_PASSWORD"),
			AppName:    strings.TrimSpace(getEnv("DB_APP_NAME", "medkvadrat-patient-api")),
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
		Dashboard: DashboardConfig{
			Password:     os.Getenv("DASHBOARD_PASSWORD"),
			AdminLogin:   strings.TrimSpace(getEnv("DASHBOARD_ADMIN_LOGIN", "admin")),
			Secret:       strings.TrimSpace(os.Getenv("DASHBOARD_SECRET")),
			SessionTTL:   mustDuration(getEnv("DASHBOARD_SESSION_TTL", "12h")),
			CookieSecure: mustBool(getEnv("DASHBOARD_COOKIE_SECURE", "false")),
			AllowedCIDRs: splitCSV(getEnv("DASHBOARD_ALLOWED_CIDRS", "192.168.0.0/16,127.0.0.1/32")),
		},
	}
	if len(cfg.WaitlistExamIDs) == 0 {
		cfg.WaitlistExamIDs = []int64{3118}
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

// ClampBookingMaxDays reports whether BOOKING_MAX_DAYS was above the hard cap.
// The caller logs a warning at startup and stores 14.
func ClampBookingMaxDays(n int) (int, bool) {
	if n > 14 {
		return 14, true
	}
	return n, false
}

func positiveOr(n, def int) int {
	if n <= 0 {
		return def
	}
	return n
}

func authorMedecinsID() int64 {
	v, ok := os.LookupEnv("LK_AUTHOR_MEDECINS_ID")
	if !ok || strings.TrimSpace(v) == "" {
		return 2058
	}
	n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
	if err != nil || n <= 0 {
		return 0
	}
	return n
}

// authorMeddepID defaults to the cabinet department. An explicit 0 or empty value disables booking.
func authorMeddepID() int64 {
	v, ok := os.LookupEnv("LK_AUTHOR_MEDDEP_ID")
	if !ok {
		return 10002309
	}
	if strings.TrimSpace(v) == "" || strings.TrimSpace(v) == "0" {
		return 0
	}
	n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
	if err != nil || n <= 0 {
		return 0
	}
	return n
}

// ParseBookingPairs reads "1119:10000688,..." . Malformed items are skipped.
func ParseBookingPairs(s string) []BookingPair {
	out := make([]BookingPair, 0)
	for _, part := range splitCSV(s) {
		sub, exam, ok := strings.Cut(part, ":")
		if !ok {
			continue
		}
		a, errA := strconv.ParseInt(strings.TrimSpace(sub), 10, 64)
		b, errB := strconv.ParseInt(strings.TrimSpace(exam), 10, 64)
		if errA != nil || errB != nil || a <= 0 || b <= 0 {
			continue
		}
		out = append(out, BookingPair{PlSubjID: a, PlExamID: b})
	}
	return out
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

func mustInt64CSV(s string) []int64 {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]int64, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		n, err := strconv.ParseInt(p, 10, 64)
		if err != nil || n <= 0 {
			continue
		}
		out = append(out, n)
	}
	return out
}
