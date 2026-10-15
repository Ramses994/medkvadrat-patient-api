package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCORS_Allowlist(t *testing.T) {
	h := CORS([]string{"https://lk.example.com"}, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	{
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.Header.Set("Origin", "https://lk.example.com")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Header().Get("Access-Control-Allow-Origin") != "https://lk.example.com" {
			t.Fatalf("origin=%q", w.Header().Get("Access-Control-Allow-Origin"))
		}
	}
	{
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.Header.Set("Origin", "https://evil.example.com")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Header().Get("Access-Control-Allow-Origin") != "" {
			t.Fatal("foreign origin must not be allowed")
		}
	}
}

func TestCORS_EmptyList(t *testing.T) {
	h := CORS(nil, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }))
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("Origin", "https://lk.example.com")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatal("empty allowlist must not set ACAO")
	}
}

func TestCORS_Options(t *testing.T) {
	h := CORS([]string{"https://lk.example.com"}, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("next should not run")
	}))
	r := httptest.NewRequest(http.MethodOptions, "/", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusNoContent {
		t.Fatalf("status=%d", w.Code)
	}
}
