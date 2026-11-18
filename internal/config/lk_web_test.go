package config

import (
	"testing"
	"time"
)

func TestLoad_WebSessionDefaults(t *testing.T) {
	for _, kv := range [][2]string{
		{"ENV", "dev"}, {"DB_SERVER", "db"}, {"DB_NAME", "db"}, {"DB_USER", "u"}, {"DB_PASSWORD", "p"},
		{"API_PORT", ":8080"}, {"AUTH_MODE", "dev"}, {"LK_COOKIE_SECURE", ""}, {"LK_WEB_REFRESH_TTL_HOURS", ""},
		{"JWT_REFRESH_TTL_DAYS", ""},
	} {
		t.Setenv(kv[0], kv[1])
	}
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Auth.CookieSecure || cfg.Auth.WebRefreshTTL != 12*time.Hour || cfg.JWT.RefreshTTLDays != 30 {
		t.Fatalf("secure=%v web ttl=%v mobile days=%d", cfg.Auth.CookieSecure, cfg.Auth.WebRefreshTTL, cfg.JWT.RefreshTTLDays)
	}

	t.Setenv("LK_COOKIE_SECURE", "false")
	t.Setenv("LK_WEB_REFRESH_TTL_HOURS", "4")
	cfg, err = Load()
	if err != nil || cfg.Auth.CookieSecure || cfg.Auth.WebRefreshTTL != 4*time.Hour {
		t.Fatalf("err=%v secure=%v ttl=%v", err, cfg.Auth.CookieSecure, cfg.Auth.WebRefreshTTL)
	}

	t.Setenv("LK_COOKIE_SECURE", "maybe")
	t.Setenv("LK_WEB_REFRESH_TTL_HOURS", "0")
	cfg, err = Load()
	if err != nil || !cfg.Auth.CookieSecure || cfg.Auth.WebRefreshTTL != 12*time.Hour {
		t.Fatalf("unreadable values: err=%v secure=%v ttl=%v", err, cfg.Auth.CookieSecure, cfg.Auth.WebRefreshTTL)
	}
}
