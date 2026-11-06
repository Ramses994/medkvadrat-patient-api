package handler

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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
	db, err := sql.Open("sqlite", "file:dash_login?mode=memory&cache=shared")
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
