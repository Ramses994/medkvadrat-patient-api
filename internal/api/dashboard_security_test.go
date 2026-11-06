package api

import (
	"context"
	"database/sql"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/medkvadrat/medkvadrat-patient-api/internal/config"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/repo"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/service"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/staff"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/store"
	_ "modernc.org/sqlite"
)

func TestDashboard_OldCookieDiesAfterPasswordChangeAndForeignOriginIs403(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open("sqlite", "file:dash_sec?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := store.Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	users := staff.New(db)
	if _, _, err := users.Create(ctx, staff.CreateUser{
		Login: "reg", FullName: "Регистратор", Role: staff.RoleRegistrar, Branches: "106", Password: "password123",
	}); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{}
	cfg.Dashboard.Password = "dashboard-password"
	cfg.Dashboard.Secret = "0123456789abcdef0123456789abcdef"
	cfg.Dashboard.AllowedCIDRs = []string{"127.0.0.1/32"}
	h := NewRouter(cfg, service.New(nil, nil, db, repo.Repos{}, slog.New(slog.DiscardHandler), 0, cfg), slog.New(slog.DiscardHandler))

	foreign := httptest.NewRequest(http.MethodPost, "/dashboard/login", strings.NewReader("login=reg&password=password123"))
	foreign.RemoteAddr = "127.0.0.1:9"
	foreign.Host = "dash.local"
	foreign.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	foreign.Header.Set("Origin", "http://evil.test")
	fw := httptest.NewRecorder()
	h.ServeHTTP(fw, foreign)
	if fw.Code != http.StatusForbidden {
		t.Fatalf("foreign origin %d %s", fw.Code, fw.Body.String())
	}

	login := httptest.NewRequest(http.MethodPost, "/dashboard/login", strings.NewReader("login=reg&password=password123"))
	login.RemoteAddr = "127.0.0.1:9"
	login.Host = "dash.local"
	login.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	login.Header.Set("Origin", "http://dash.local")
	lw := httptest.NewRecorder()
	h.ServeHTTP(lw, login)
	if lw.Code != http.StatusFound {
		t.Fatalf("login %d %s", lw.Code, lw.Body.String())
	}
	old := lw.Result().Cookies()
	if len(old) == 0 {
		t.Fatal("no cookie")
	}

	form := url.Values{"current_password": {"password123"}, "new_password": {"password1234"}}
	change := httptest.NewRequest(http.MethodPost, "/dashboard/password", strings.NewReader(form.Encode()))
	change.RemoteAddr = "127.0.0.1:9"
	change.Host = "dash.local"
	change.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	change.Header.Set("Origin", "http://dash.local")
	change.AddCookie(old[0])
	cw := httptest.NewRecorder()
	h.ServeHTTP(cw, change)
	if cw.Code != http.StatusFound {
		t.Fatalf("password %d %s", cw.Code, cw.Body.String())
	}
	fresh := cw.Result().Cookies()
	if len(fresh) == 0 || fresh[0].Value == old[0].Value {
		t.Fatal("current session did not get a new cookie")
	}

	stale := httptest.NewRequest(http.MethodGet, "/dashboard/me", nil)
	stale.RemoteAddr = "127.0.0.1:9"
	stale.Host = "dash.local"
	stale.AddCookie(old[0])
	sw := httptest.NewRecorder()
	h.ServeHTTP(sw, stale)
	if sw.Code != http.StatusUnauthorized {
		t.Fatalf("old cookie %d %s", sw.Code, sw.Body.String())
	}
	okReq := httptest.NewRequest(http.MethodGet, "/dashboard/me", nil)
	okReq.RemoteAddr = "127.0.0.1:9"
	okReq.Host = "dash.local"
	okReq.AddCookie(fresh[0])
	ow := httptest.NewRecorder()
	h.ServeHTTP(ow, okReq)
	if ow.Code != http.StatusOK {
		t.Fatalf("new cookie %d %s", ow.Code, ow.Body.String())
	}
}
