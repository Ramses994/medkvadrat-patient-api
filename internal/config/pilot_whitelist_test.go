package config

import (
	"strings"
	"testing"
)

func TestNormalizePilotWhitelist_Formats(t *testing.T) {
	got, err := normalizePilotWhitelist([]string{"+7 999 123-45-67", "79991234567"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != "79991234567" || got[1] != "79991234567" {
		t.Fatalf("%v", got)
	}
}

func TestNormalizePilotWhitelist_GarbageFails(t *testing.T) {
	_, err := normalizePilotWhitelist([]string{"79991234567", "not-a-phone"})
	if err == nil || !strings.Contains(err.Error(), "AUTH_PILOT_WHITELIST: запись 2 не является номером") {
		t.Fatalf("err=%v", err)
	}
	if strings.Contains(err.Error(), "not-a-phone") {
		t.Fatal("error must not echo the entry")
	}
}

func TestLoad_PilotWhitelistNormalized(t *testing.T) {
	for _, kv := range [][2]string{
		{"ENV", "dev"}, {"DB_SERVER", "db"}, {"DB_NAME", "db"}, {"DB_USER", "u"}, {"DB_PASSWORD", "p"},
		{"API_PORT", ":8080"}, {"AUTH_MODE", "dev"},
		{"AUTH_PILOT_WHITELIST", "+7 999 123-45-67, 79991234567"},
	} {
		t.Setenv(kv[0], kv[1])
	}
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Auth.PilotWhitelist) != 2 || cfg.Auth.PilotWhitelist[0] != "79991234567" {
		t.Fatalf("%v", cfg.Auth.PilotWhitelist)
	}
}

func TestLoad_PilotWhitelistGarbage(t *testing.T) {
	for _, kv := range [][2]string{
		{"ENV", "dev"}, {"DB_SERVER", "db"}, {"DB_NAME", "db"}, {"DB_USER", "u"}, {"DB_PASSWORD", "p"},
		{"API_PORT", ":8080"}, {"AUTH_MODE", "dev"},
		{"AUTH_PILOT_WHITELIST", "abc"},
	} {
		t.Setenv(kv[0], kv[1])
	}
	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "запись 1") || strings.Contains(err.Error(), "abc") {
		t.Fatalf("err=%v", err)
	}
}
