package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/medkvadrat/medkvadrat-patient-api/internal/config"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/db"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/repo"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/service"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/staff"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/store"
)

func TestRouter_ResetPasswordOneTransactionAndLastAdmin(t *testing.T) {
	ctx := context.Background()
	sqlite, err := db.OpenSQLite(filepath.Join(t.TempDir(), "staff.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlite.Close() })
	if err := store.Migrate(ctx, sqlite); err != nil {
		t.Fatal(err)
	}
	users := staff.New(sqlite)
	admin, _, err := users.Create(ctx, staff.CreateUser{
		Login: "admin", FullName: "Администратор", Role: staff.RoleAdmin, Password: "password123",
	})
	if err != nil {
		t.Fatal(err)
	}
	employee, _, err := users.Create(ctx, staff.CreateUser{
		Login: "reg", FullName: "Регистратор", Role: staff.RoleRegistrar, Branches: "106", Password: "password123",
	})
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{}
	cfg.Dashboard.Password = "dashboard-password"
	cfg.Dashboard.Secret = "0123456789abcdef0123456789abcdef"
	cfg.Dashboard.AllowedCIDRs = []string{"127.0.0.1/32"}
	h := NewRouter(cfg, service.New(nil, nil, sqlite, repo.Repos{}, slog.New(slog.DiscardHandler), 0, cfg), slog.New(slog.DiscardHandler))

	adminCookie := loginCookie(t, h, "admin", "password123")
	oldCookie := loginCookie(t, h, "reg", "password123")
	me := dashReq(http.MethodGet, "/dashboard/me", "", oldCookie)
	mw := httptest.NewRecorder()
	h.ServeHTTP(mw, me)
	if mw.Code != http.StatusOK {
		t.Fatalf("employee before reset %d %s", mw.Code, mw.Body.String())
	}

	resetCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	reset := dashReq(http.MethodPost, "/dashboard/staff/"+strconv.FormatInt(employee.ID, 10), `{"reset_password":true}`, adminCookie)
	reset.Header.Set("Content-Type", "application/json")
	reset = reset.WithContext(resetCtx)
	rw := httptest.NewRecorder()
	h.ServeHTTP(rw, reset)
	if resetCtx.Err() != nil {
		t.Fatalf("reset held the database: %v", resetCtx.Err())
	}
	if rw.Code != http.StatusOK {
		t.Fatalf("reset %d %s", rw.Code, rw.Body.String())
	}
	follow := dashReq(http.MethodGet, "/dashboard/me", "", adminCookie).WithContext(resetCtx)
	fw := httptest.NewRecorder()
	h.ServeHTTP(fw, follow)
	if resetCtx.Err() != nil {
		t.Fatalf("sqlite connection was not released: %v", resetCtx.Err())
	}
	if fw.Code != http.StatusOK {
		t.Fatalf("follow-up %d %s", fw.Code, fw.Body.String())
	}
	var body struct {
		Data struct {
			TemporaryPassword string `json:"temporary_password"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rw.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Data.TemporaryPassword == "" {
		t.Fatalf("body=%s", rw.Body.String())
	}
	stale := dashReq(http.MethodGet, "/dashboard/me", "", oldCookie)
	sw := httptest.NewRecorder()
	h.ServeHTTP(sw, stale)
	if sw.Code != http.StatusUnauthorized {
		t.Fatalf("old cookie %d %s", sw.Code, sw.Body.String())
	}
	loginCookie(t, h, "reg", body.Data.TemporaryPassword)

	second, _, err := users.Create(ctx, staff.CreateUser{
		Login: "admin2", FullName: "Второй", Role: staff.RoleAdmin, Password: "password123",
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = admin
	cookies := []*http.Cookie{
		loginCookie(t, h, "admin", "password123"),
		loginCookie(t, h, "admin2", "password123"),
	}
	ids := []int64{admin.ID, second.ID}
	codes := make([]int, 2)
	var wg sync.WaitGroup
	for i := range ids {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			reqCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			r := dashReq(http.MethodPost, "/dashboard/staff/"+strconv.FormatInt(ids[i], 10), `{"active":false}`, cookies[i])
			r = r.WithContext(reqCtx)
			r.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			codes[i] = w.Code
		}(i)
	}
	wg.Wait()
	ok, conflict := 0, 0
	for _, code := range codes {
		if code == http.StatusOK {
			ok++
		} else if code == http.StatusConflict {
			conflict++
		}
	}
	if ok != 1 || conflict != 1 {
		t.Fatalf("codes=%v", codes)
	}
}

func loginCookie(t *testing.T, h http.Handler, login, password string) *http.Cookie {
	t.Helper()
	r := dashReq(http.MethodPost, "/dashboard/login", "login="+login+"&password="+password, nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusFound {
		t.Fatalf("login %s %d %s", login, w.Code, w.Body.String())
	}
	for _, c := range w.Result().Cookies() {
		if c.Name != "" && c.Value != "" {
			return c
		}
	}
	t.Fatal("no cookie")
	return nil
}

func dashReq(method, path, body string, cookie *http.Cookie) *http.Request {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.RemoteAddr = "127.0.0.1:9"
	r.Host = "dash.local"
	r.Header.Set("Origin", "http://dash.local")
	r.Header.Set("X-Dashboard-Request", "1")
	if method == http.MethodPost && !strings.HasPrefix(body, "{") {
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if cookie != nil {
		r.AddCookie(cookie)
	}
	return r
}
