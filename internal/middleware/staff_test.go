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
	secret := []byte("test-secret-32-bytes-long!!!!!!")
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
		r.AddCookie(&http.Cookie{Name: staffsession.CookieName, Value: staffsession.Sign(secret, exp)})
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("valid cookie: %d", w.Code)
		}
	}
}
