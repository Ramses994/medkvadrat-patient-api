package api

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/medkvadrat/medkvadrat-patient-api/internal/config"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/db"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/repo"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/service"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/store"
)

func TestBotConsents_TokenLikeConfirmationsFromTheBotNetwork(t *testing.T) {
	ctx := context.Background()
	sqlDB, err := db.OpenSQLite(filepath.Join(t.TempDir(), "gateway.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := store.Migrate(ctx, sqlDB); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{APIToken: "s2s-token"}
	h := NewRouter(cfg, service.New(nil, nil, sqlDB, repo.Repos{}, slog.New(slog.DiscardHandler), 0, cfg), slog.New(slog.DiscardHandler))

	body := `{"patient_id":1,"max_user_id":2,"kind":"service","text_version":"service-v1","action":"accept"}`
	noToken := httptest.NewRequest(http.MethodPost, "/api/internal/bot-consents", strings.NewReader(body))
	noToken.RemoteAddr = "127.0.0.1:9"
	nw := httptest.NewRecorder()
	h.ServeHTTP(nw, noToken)
	confirmNoToken := httptest.NewRequest(http.MethodPost, "/api/internal/confirmations", strings.NewReader(`{}`))
	confirmNoToken.RemoteAddr = "127.0.0.1:9"
	cw := httptest.NewRecorder()
	h.ServeHTTP(cw, confirmNoToken)
	if nw.Code != http.StatusUnauthorized || cw.Code != http.StatusUnauthorized {
		t.Fatalf("token gate consents=%d confirmations=%d", nw.Code, cw.Code)
	}

	inside := httptest.NewRequest(http.MethodPost, "/api/internal/bot-consents", strings.NewReader(body))
	inside.RemoteAddr = "172.18.0.5:9"
	inside.Header.Set("Authorization", "Bearer s2s-token")
	iw := httptest.NewRecorder()
	h.ServeHTTP(iw, inside)
	if iw.Code != http.StatusOK {
		t.Fatalf("clinic %d %s", iw.Code, iw.Body.String())
	}

	again := httptest.NewRequest(http.MethodGet, "/api/internal/bot-consents?patient_id=1", nil)
	again.RemoteAddr = "127.0.0.1:9"
	again.Header.Set("Authorization", "Bearer s2s-token")
	gw := httptest.NewRecorder()
	h.ServeHTTP(gw, again)
	if gw.Code != http.StatusOK || !strings.Contains(gw.Body.String(), `"state":"active"`) {
		t.Fatalf("get %d %s", gw.Code, gw.Body.String())
	}
}
