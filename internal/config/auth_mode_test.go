package config

import (
	"strings"
	"testing"
)

func TestAuthModeWarning(t *testing.T) {
	if got := AuthModeWarning("dev"); got != "AUTH_MODE=dev: вход без проверки, только для локальной отладки" {
		t.Fatalf("dev warning=%q", got)
	}
	for _, mode := range []string{"pilot", "prod"} {
		if got := AuthModeWarning(mode); got != "" {
			t.Fatalf("%s warning=%q", mode, got)
		}
	}
}

func TestLoad_PilotNeedsPilotCode(t *testing.T) {
	for _, kv := range [][2]string{
		{"ENV", "dev"}, {"DB_SERVER", "db"}, {"DB_NAME", "db"}, {"DB_USER", "u"}, {"DB_PASSWORD", "p"},
		{"API_PORT", ":8080"}, {"OTP_HMAC_SECRET", "otp-key"}, {"JWT_SECRET", "jwt-key"}, {"AUTH_MODE", "pilot"},
	} {
		t.Setenv(kv[0], kv[1])
	}
	for _, code := range []string{"", "000000", "Short12345a", "pilot-code-2026"} {
		t.Setenv("AUTH_PILOT_CODE", code)
		_, err := Load()
		if err == nil || !strings.Contains(err.Error(), "AUTH_PILOT_CODE") {
			t.Fatalf("code of length %d: err=%v", len(code), err)
		}
		if code != "" && strings.Contains(err.Error(), code) {
			t.Fatal("error repeats the code")
		}
	}
	t.Setenv("AUTH_PILOT_CODE", "Pilot2026code")
	cfg, err := Load()
	if err != nil || cfg.Auth.PilotCode != "Pilot2026code" {
		t.Fatalf("valid code: err=%v", err)
	}
	t.Setenv("AUTH_MODE", "prod")
	t.Setenv("AUTH_PILOT_CODE", "")
	if _, err := Load(); err != nil {
		t.Fatalf("prod without pilot code: %v", err)
	}
}
