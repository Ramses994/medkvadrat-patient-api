package config

import (
	"strings"
	"testing"
	"time"
)

func TestParseWebOrigins(t *testing.T) {
	got, err := ParseWebOrigins("http://Cabinet.Example.Test:8083, https://app.example.test")
	if err != nil || len(got) != 2 || got[0] != "http://cabinet.example.test:8083" || got[1] != "https://app.example.test" {
		t.Fatalf("got=%v err=%v", got, err)
	}
	if !OriginAllowed("HTTP://cabinet.example.test:8083", got) {
		t.Fatal("case-insensitive match")
	}
	if OriginAllowed("https://cabinet.example.test:8083", got) {
		t.Fatal("scheme mismatch")
	}
	for _, bad := range []string{"cabinet.example.test", "http://x/path", "ftp://x"} {
		if _, err := ParseWebOrigins(bad); err == nil || !strings.Contains(err.Error(), "LK_WEB_ORIGINS") {
			t.Fatalf("bad %q: %v", bad, err)
		}
	}
}

func TestLoad_WebSessionIdleAndOrigins(t *testing.T) {
	for _, kv := range [][2]string{
		{"ENV", "dev"}, {"DB_SERVER", "db"}, {"DB_NAME", "db"}, {"DB_USER", "u"}, {"DB_PASSWORD", "p"},
		{"API_PORT", ":8080"}, {"AUTH_MODE", "dev"}, {"LK_WEB_IDLE_MIN", ""}, {"LK_WEB_ORIGINS", "http://cabinet.example.test:8083"},
	} {
		t.Setenv(kv[0], kv[1])
	}
	cfg, err := Load()
	if err != nil || cfg.Auth.WebIdleMin != 20*time.Minute || len(cfg.Auth.WebOrigins) != 1 {
		t.Fatalf("err=%v idle=%v origins=%v", err, cfg.Auth.WebIdleMin, cfg.Auth.WebOrigins)
	}
	t.Setenv("LK_WEB_ORIGINS", "http://bad/path")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "LK_WEB_ORIGINS") {
		t.Fatalf("path must fail load: %v", err)
	}
}
