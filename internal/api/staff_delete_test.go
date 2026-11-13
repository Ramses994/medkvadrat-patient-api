package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/medkvadrat/medkvadrat-patient-api/internal/config"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/confirmations"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/db"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/repo"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/service"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/staff"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/store"
)

func staffDeleteRouter(t *testing.T) (http.Handler, *staff.Store, *sql.DB) {
	t.Helper()
	ctx := context.Background()
	sqlite, err := db.OpenSQLite(filepath.Join(t.TempDir(), "staff.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlite.Close() })
	if err := store.Migrate(ctx, sqlite); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{}
	cfg.Dashboard.Password = "dashboard-password"
	cfg.Dashboard.Secret = "0123456789abcdef0123456789abcdef"
	cfg.Dashboard.AllowedCIDRs = []string{"127.0.0.1/32"}
	h := NewRouter(cfg, service.New(nil, nil, sqlite, repo.Repos{}, slog.New(slog.DiscardHandler), 0, cfg), slog.New(slog.DiscardHandler))
	return h, staff.New(sqlite), sqlite
}

func mustCreateStaff(t *testing.T, users *staff.Store, login, role, branches string) staff.User {
	t.Helper()
	u, _, err := users.Create(context.Background(), staff.CreateUser{
		Login: login, FullName: login, Role: role, Branches: branches, Password: "password123",
	})
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func deleteStaff(h http.Handler, id int64, cookie *http.Cookie) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	h.ServeHTTP(w, dashReq(http.MethodDelete, "/dashboard/staff/"+strconv.FormatInt(id, 10), "", cookie))
	return w
}

func staffLogins(t *testing.T, h http.Handler, cookie *http.Cookie) []string {
	t.Helper()
	w := httptest.NewRecorder()
	h.ServeHTTP(w, dashReq(http.MethodGet, "/dashboard/staff", "", cookie))
	if w.Code != http.StatusOK {
		t.Fatalf("list %d %s", w.Code, w.Body.String())
	}
	var body struct {
		Data struct {
			Users []struct {
				Login string `json:"login"`
			} `json:"users"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	out := make([]string, 0, len(body.Data.Users))
	for _, u := range body.Data.Users {
		out = append(out, u.Login)
	}
	return out
}

func hasLogin(logins []string, login string) bool {
	for _, l := range logins {
		if l == login {
			return true
		}
	}
	return false
}

func TestRouter_StaffDeleteAdminOnly(t *testing.T) {
	h, users, _ := staffDeleteRouter(t)
	mustCreateStaff(t, users, "admin", staff.RoleAdmin, "*")
	mustCreateStaff(t, users, "mgr", staff.RoleManager, "*")
	mustCreateStaff(t, users, "reg", staff.RoleRegistrar, "106")
	target := mustCreateStaff(t, users, "victim", staff.RoleRegistrar, "106")

	for _, login := range []string{"mgr", "reg"} {
		w := deleteStaff(h, target.ID, loginCookie(t, h, login, "password123"))
		if w.Code != http.StatusForbidden {
			t.Fatalf("%s delete %d %s", login, w.Code, w.Body.String())
		}
	}
	adminCookie := loginCookie(t, h, "admin", "password123")
	noHeader := dashReq(http.MethodDelete, "/dashboard/staff/"+strconv.FormatInt(target.ID, 10), "", adminCookie)
	noHeader.Header.Del("X-Dashboard-Request")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, noHeader)
	if w.Code != http.StatusForbidden {
		t.Fatalf("no dashboard header %d %s", w.Code, w.Body.String())
	}
	foreign := dashReq(http.MethodDelete, "/dashboard/staff/"+strconv.FormatInt(target.ID, 10), "", adminCookie)
	foreign.Header.Set("Origin", "http://evil.example")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, foreign)
	if w.Code != http.StatusForbidden {
		t.Fatalf("foreign origin %d %s", w.Code, w.Body.String())
	}
	if !hasLogin(staffLogins(t, h, adminCookie), "victim") {
		t.Fatal("refused delete removed the employee")
	}
	loginCookie(t, h, "victim", "password123")
}

func TestRouter_StaffDeleteSelfAndLastAdminAre409(t *testing.T) {
	h, users, _ := staffDeleteRouter(t)
	admin := mustCreateStaff(t, users, "admin", staff.RoleAdmin, "*")
	adminCookie := loginCookie(t, h, "admin", "password123")
	w := deleteStaff(h, admin.ID, adminCookie)
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "CANNOT_DELETE_SELF") {
		t.Fatalf("self %d %s", w.Code, w.Body.String())
	}
	if !hasLogin(staffLogins(t, h, adminCookie), "admin") {
		t.Fatal("self delete went through")
	}

	mgr := mustCreateStaff(t, users, "mgr", staff.RoleManager, "*")
	err := users.Delete(context.Background(), mgr.ID, "mgr", admin.ID)
	if err != staff.ErrLastAdmin {
		t.Fatalf("last admin err=%v", err)
	}
	if w := deleteStaff(h, mgr.ID, adminCookie); w.Code != http.StatusOK {
		t.Fatalf("delete manager %d %s", w.Code, w.Body.String())
	}
}

func TestRouter_StaffDeleteEndsSessionFreesLoginKeepsWaitlistMark(t *testing.T) {
	ctx := context.Background()
	h, users, sqlite := staffDeleteRouter(t)
	mustCreateStaff(t, users, "admin", staff.RoleAdmin, "*")
	reg := mustCreateStaff(t, users, "reg", staff.RoleRegistrar, "106")
	adminCookie := loginCookie(t, h, "admin", "password123")
	regCookie := loginCookie(t, h, "reg", "password123")

	if _, err := sqlite.ExecContext(ctx, `
INSERT INTO waitlist(planning_id,patient_id,appt_version,appt_start,reason,source,status,created_at,updated_at,created_at_msk,branch_id)
VALUES(1,1,'v1','2026-11-14 12:00:00','declined','max','new','2026-11-13T09:00:00Z','2026-11-13T09:00:00Z','2026-11-13 12:00:00',106)`); err != nil {
		t.Fatal(err)
	}
	waitlist := confirmations.NewRepo(sqlite)
	if _, err := waitlist.UpdateWaitlist(ctx, 1, "contacted", "call_back", "reg", time.Now(), nil); err != nil {
		t.Fatal(err)
	}

	me := httptest.NewRecorder()
	h.ServeHTTP(me, dashReq(http.MethodGet, "/dashboard/me", "", regCookie))
	if me.Code != http.StatusOK {
		t.Fatalf("before delete %d %s", me.Code, me.Body.String())
	}

	w := deleteStaff(h, reg.ID, adminCookie)
	if w.Code != http.StatusOK {
		t.Fatalf("delete %d %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "password") {
		t.Fatalf("body=%s", w.Body.String())
	}

	stale := httptest.NewRecorder()
	h.ServeHTTP(stale, dashReq(http.MethodGet, "/dashboard/me", "", regCookie))
	if stale.Code != http.StatusUnauthorized {
		t.Fatalf("old cookie %d %s", stale.Code, stale.Body.String())
	}
	login := httptest.NewRecorder()
	h.ServeHTTP(login, dashReq(http.MethodPost, "/dashboard/login", "login=reg&password=password123", nil))
	if login.Code == http.StatusFound {
		t.Fatal("deleted employee logged in")
	}
	if hasLogin(staffLogins(t, h, adminCookie), "reg") {
		t.Fatal("deleted employee still listed")
	}
	if w := deleteStaff(h, reg.ID, adminCookie); w.Code != http.StatusNotFound {
		t.Fatalf("second delete %d %s", w.Code, w.Body.String())
	}
	if w := deleteStaff(h, 9999, adminCookie); w.Code != http.StatusNotFound {
		t.Fatalf("unknown id %d %s", w.Code, w.Body.String())
	}

	var actor, change string
	var target int64
	if err := sqlite.QueryRowContext(ctx, `
SELECT actor_login, target_id, change FROM staff_admin_log WHERE change LIKE 'delete;%'`).Scan(&actor, &target, &change); err != nil {
		t.Fatal(err)
	}
	if actor != "admin" || target != reg.ID || change != "delete;login=reg" {
		t.Fatalf("journal actor=%q target=%d change=%q", actor, target, change)
	}

	row, err := waitlist.GetWaitlist(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if row.UpdatedBy != "reg" {
		t.Fatalf("waitlist mark %q", row.UpdatedBy)
	}

	create := dashReq(http.MethodPost, "/dashboard/staff", `{"login":"reg","full_name":"Новый","role":"registrar","branches":"106","password":"password456"}`, adminCookie)
	create.Header.Set("Content-Type", "application/json")
	cw := httptest.NewRecorder()
	h.ServeHTTP(cw, create)
	if cw.Code != http.StatusOK {
		t.Fatalf("recreate %d %s", cw.Code, cw.Body.String())
	}
	fresh := loginCookie(t, h, "reg", "password456")
	fm := httptest.NewRecorder()
	h.ServeHTTP(fm, dashReq(http.MethodGet, "/dashboard/me", "", fresh))
	if fm.Code != http.StatusOK || !strings.Contains(fm.Body.String(), `"full_name":"Новый"`) {
		t.Fatalf("new account %d %s", fm.Code, fm.Body.String())
	}
	dup := dashReq(http.MethodPost, "/dashboard/staff", `{"login":"reg","full_name":"Дубль","role":"registrar","branches":"106","password":"password789"}`, adminCookie)
	dup.Header.Set("Content-Type", "application/json")
	dw := httptest.NewRecorder()
	h.ServeHTTP(dw, dup)
	if dw.Code != http.StatusUnprocessableEntity || !strings.Contains(dw.Body.String(), "Такой логин уже есть") {
		t.Fatalf("duplicate live login %d %s", dw.Code, dw.Body.String())
	}
	if row, err := waitlist.GetWaitlist(ctx, 1); err != nil || row.UpdatedBy != "reg" {
		t.Fatalf("waitlist mark after recreate %q err=%v", row.UpdatedBy, err)
	}
}
