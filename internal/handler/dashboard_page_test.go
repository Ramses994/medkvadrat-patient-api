package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/medkvadrat/medkvadrat-patient-api/web"
)

func TestDashboardPage_ServesHTML(t *testing.T) {
	h := DashboardPageHandler{
		HTML:     web.DashboardHTML,
		Password: "secret",
		Secret:   []byte("test-secret-32-bytes-long!!!!!!"),
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
	h := DashboardPageHandler{
		HTML:     []byte("x"),
		Password: "secret",
		Secret:   []byte("test-secret-32-bytes-long!!!!!!"),
	}
	r := httptest.NewRequest(http.MethodPost, "/dashboard/login", strings.NewReader("password=secret"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.RemoteAddr = "127.0.0.1:1234"
	w := httptest.NewRecorder()
	h.LoginPost(w, r)
	if w.Code != http.StatusFound {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
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
