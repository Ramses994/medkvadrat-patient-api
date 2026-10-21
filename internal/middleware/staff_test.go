package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/medkvadrat/medkvadrat-patient-api/internal/staffsession"
)

func TestAllowCIDRs(t *testing.T) {
	nets, err := ParseCIDRList([]string{"192.168.0.0/16", "127.0.0.1/32"})
	if err != nil {
		t.Fatal(err)
	}
	a := AllowCIDRs{Nets: nets}
	if !a.Allowed("192.168.2.104") {
		t.Fatal("lan should allow")
	}
	if !a.Allowed("127.0.0.1") {
		t.Fatal("loopback should allow")
	}
	if a.Allowed("8.8.8.8") {
		t.Fatal("public should deny")
	}
}

func TestRequireStaff_RedirectAndJSON(t *testing.T) {
	secret := []byte("0123456789abcdef0123456789abcdef")
	now := time.Date(2026, 6, 25, 12, 0, 0, 0, time.UTC)
	staff := RequireStaff{Secret: secret, Now: func() time.Time { return now }}
	okH := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	h := staff.Wrap(okH)

	{
		r := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != http.StatusFound {
			t.Fatalf("page without cookie: %d", w.Code)
		}
		if loc := w.Header().Get("Location"); loc != "/dashboard/login" {
			t.Fatalf("location=%q", loc)
		}
	}
	{
		r := httptest.NewRequest(http.MethodGet, "/dashboard/data", nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("data without cookie: %d", w.Code)
		}
	}
	{
		exp := now.Add(time.Hour)
		r := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
		val, err := staffsession.Sign(secret, exp)
		if err != nil {
			t.Fatal(err)
		}
		r.AddCookie(&http.Cookie{Name: staffsession.CookieName, Value: val})
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("valid cookie: %d", w.Code)
		}
	}
}

func TestRequireDashboardConfig_EmptyOrShortSecret_503BeforeHandler(t *testing.T) {
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})
	cases := []struct {
		name     string
		password string
		secret   []byte
	}{
		{"empty secret", "pass", nil},
		{"short secret", "pass", []byte("short")},
		{"empty password", "", []byte("0123456789abcdef0123456789abcdef")},
	}
	for _, tc := range cases {
		called = false
		h := RequireDashboardConfig{Password: tc.password, Secret: tc.secret}.Wrap(next)
		r := httptest.NewRequest(http.MethodGet, "/dashboard/data", nil)
		r.AddCookie(&http.Cookie{Name: staffsession.CookieName, Value: "1|deadbeef"})
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != http.StatusServiceUnavailable {
			t.Fatalf("%s: status=%d", tc.name, w.Code)
		}
		if called {
			t.Fatalf("%s: handler must not run", tc.name)
		}
		if w.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("%s: cache-control=%q", tc.name, w.Header().Get("Cache-Control"))
		}
	}
}

func TestRequireDashboardConfig_ReadySecret_Passes(t *testing.T) {
	secret := []byte("0123456789abcdef0123456789abcdef")
	h := RequireDashboardConfig{Password: "pass", Secret: secret}.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	r := httptest.NewRequest(http.MethodGet, "/dashboard/data", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d", w.Code)
	}
}

func TestRequireStaff_401JSON_NoStore(t *testing.T) {
	secret := []byte("0123456789abcdef0123456789abcdef")
	staff := RequireStaff{Secret: secret}
	h := staff.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler must not run")
	}))
	r := httptest.NewRequest(http.MethodGet, "/dashboard/data", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d", w.Code)
	}
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("cache-control=%q", w.Header().Get("Cache-Control"))
	}
}
