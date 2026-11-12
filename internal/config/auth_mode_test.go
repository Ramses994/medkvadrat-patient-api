package config

import "testing"

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
