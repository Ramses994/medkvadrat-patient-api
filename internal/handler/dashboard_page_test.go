package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/medkvadrat/medkvadrat-patient-api/internal/db"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/middleware"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/staff"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/store"
	"github.com/medkvadrat/medkvadrat-patient-api/web"
	_ "modernc.org/sqlite"
)

func TestDashboardPage_ServesHTML(t *testing.T) {
	h := DashboardPageHandler{
		HTML:     web.DashboardHTML,
		Password: "secret",
		Secret:   []byte("0123456789abcdef0123456789abcdef"),
	}
	r := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	w := httptest.NewRecorder()
	h.Page(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "text/html") {
		t.Fatalf("content-type=%q", ct)
	}
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("cache-control=%q", w.Header().Get("Cache-Control"))
	}
	body := w.Body.String()
	if strings.Contains(body, "API_TOKEN") || strings.Contains(body, "Authorization") {
		t.Fatal("HTML must not contain API_TOKEN or Authorization")
	}
	if !strings.Contains(body, "/dashboard/data") {
		t.Fatal("expected /dashboard/data endpoint")
	}
	if !strings.Contains(body, "/dashboard/waitlist") {
		t.Fatal("expected waitlist endpoint")
	}
	if !strings.Contains(body, "Отметки ответов пациентов из MAX. Запись в Медиалоге не меняется — отмену или перенос делайте в клиенте") {
		t.Fatal("expected waitlist caption")
	}
	if !strings.Contains(body, "a.textContent = it.patient_phone") {
		t.Fatal("phone must be assigned via textContent")
	}
	if !strings.Contains(body, "it.patient_birth || '—'") {
		t.Fatal("empty birth date must render as a dash")
	}
	if !strings.Contains(body, "res.status === 409") {
		t.Fatal("waitlist save must handle WAITLIST_CHANGED")
	}
	save := body[strings.Index(body, "async function saveWaitlist"):]
	block := save[strings.Index(save, "if (res.status === 409)"):strings.Index(save, "if (!res.ok)")]
	if strings.Index(block, "showBanner(msg)") < 0 || strings.Contains(block, "reloadWaitlistRow") {
		t.Fatal("409 must show the banner without a late reload")
	}
	for _, line := range strings.Split(body, "\n") {
		trim := strings.TrimSpace(line)
		if strings.Contains(trim, "innerHTML") && !strings.Contains(trim, "board.innerHTML = ''") && !strings.Contains(trim, "wrap.innerHTML = ''") {
			t.Fatalf("unexpected innerHTML: %s", trim)
		}
	}
}

func TestPasswordPost_StaleIs409(t *testing.T) {
	db, err := db.OpenSQLite(filepath.Join(t.TempDir(), "staff.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := store.Migrate(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	users := staff.New(db)
	created, _, err := users.Create(context.Background(), staff.CreateUser{
		Login: "reg", FullName: "R", Role: staff.RoleRegistrar, Branches: "106", Password: "password123",
	})
	if err != nil {
		t.Fatal(err)
	}
	users.Compare = func(hash, password []byte) error {
		_, err := db.ExecContext(context.Background(), `UPDATE staff_users SET session_version = session_version + 1 WHERE id = ?`, created.ID)
		return err
	}
	principal, err := users.ByID(context.Background(), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	h := DashboardPageHandler{Users: users, Secret: []byte("0123456789abcdef0123456789abcdef")}
	r := httptest.NewRequest(http.MethodPost, "/dashboard/password", strings.NewReader("current_password=password123&new_password=new-password-1&new_password_again=new-password-1"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r = r.WithContext(staff.WithPrincipal(r.Context(), principal))
	w := httptest.NewRecorder()
	h.PasswordPost(w, r)
	if w.Code != http.StatusConflict {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "Пароль уже изменён — войдите заново") {
		t.Fatalf("body=%s", w.Body.String())
	}
}

func TestDashboardPage_NotConfigured(t *testing.T) {
	h := DashboardPageHandler{HTML: []byte("x")}
	r := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	w := httptest.NewRecorder()
	h.Page(w, r)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d", w.Code)
	}
}

func TestDashboardPage_LoginSetsCookie(t *testing.T) {
	db, err := db.OpenSQLite(filepath.Join(t.TempDir(), "staff.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := store.Migrate(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	if _, err := staff.EnsureFirstAdmin(context.Background(), db, "admin", "secret"); err != nil {
		t.Fatal(err)
	}
	h := DashboardPageHandler{
		HTML:     []byte("x"),
		Password: "secret",
		Secret:   []byte("0123456789abcdef0123456789abcdef"),
		Users:    staff.New(db),
	}
	r := httptest.NewRequest(http.MethodPost, "/dashboard/login", strings.NewReader("login=admin&password=secret"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.RemoteAddr = "127.0.0.1:1234"
	w := httptest.NewRecorder()
	h.LoginPost(w, r)
	if w.Code != http.StatusFound {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if loc := w.Header().Get("Location"); loc != "/dashboard/password" {
		t.Fatalf("first admin must change password, location=%s", loc)
	}
	found := false
	for _, c := range w.Result().Cookies() {
		if c.Name == "mk_staff" && c.Value != "" && c.HttpOnly && c.Path == "/dashboard" {
			found = true
		}
	}
	if !found {
		t.Fatal("expected mk_staff cookie")
	}
}

func TestLogout_OldCookieDoesNotOpenDashboard(t *testing.T) {
	db, err := db.OpenSQLite(filepath.Join(t.TempDir(), "staff.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := store.Migrate(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	users := staff.New(db)
	created, _, err := users.Create(context.Background(), staff.CreateUser{
		Login: "reg106", FullName: "Регистратор", Role: staff.RoleRegistrar, Branches: "106", Password: "password123",
	})
	if err != nil {
		t.Fatal(err)
	}
	secret := []byte("0123456789abcdef0123456789abcdef")
	h := DashboardPageHandler{HTML: []byte("board"), Password: "secret", Secret: secret, Users: users}
	login := httptest.NewRequest(http.MethodPost, "/dashboard/login", strings.NewReader("login=reg106&password=password123"))
	login.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	login.Header.Set("Origin", "http://example.test")
	login.RemoteAddr = "127.0.0.1:9"
	lw := httptest.NewRecorder()
	h.LoginPost(lw, login)
	if lw.Code != http.StatusFound || lw.Header().Get("Location") != "/dashboard" {
		t.Fatalf("login %d %s", lw.Code, lw.Header().Get("Location"))
	}
	var cookie *http.Cookie
	for _, c := range lw.Result().Cookies() {
		if c.Name == "mk_staff" {
			cookie = c
		}
	}
	if cookie == nil || cookie.Value == "" {
		t.Fatal("missing session")
	}
	staffMW := middleware.RequireStaff{Secret: secret, Lookup: users.ByID}
	open := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	open.AddCookie(cookie)
	ow := httptest.NewRecorder()
	staffMW.Wrap(http.HandlerFunc(h.Page)).ServeHTTP(ow, open)
	if ow.Code != http.StatusOK {
		t.Fatalf("open %d", ow.Code)
	}
	out := httptest.NewRequest(http.MethodPost, "http://example.test/dashboard/logout", nil)
	out.Host = "example.test"
	out.Header.Set("Origin", "http://example.test")
	out.AddCookie(cookie)
	outw := httptest.NewRecorder()
	staffMW.Wrap(middleware.RequireSameOrigin(http.HandlerFunc(h.Logout))).ServeHTTP(outw, out)
	if outw.Code != http.StatusFound || outw.Header().Get("Location") != "/dashboard/login" {
		t.Fatalf("logout %d %s", outw.Code, outw.Header().Get("Location"))
	}
	again := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	again.AddCookie(cookie)
	aw := httptest.NewRecorder()
	staffMW.Wrap(http.HandlerFunc(h.Page)).ServeHTTP(aw, again)
	if aw.Code != http.StatusFound || aw.Header().Get("Location") != "/dashboard/login" {
		t.Fatalf("old cookie %d %s", aw.Code, aw.Header().Get("Location"))
	}
	if created.ID == 0 {
		t.Fatal("user")
	}
}
