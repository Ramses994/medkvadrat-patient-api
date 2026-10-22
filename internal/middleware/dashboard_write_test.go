package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRequireDashboardMutation_OK(t *testing.T) {
	called := false
	h := RequireDashboardMutation(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))
	r := httptest.NewRequest(http.MethodPost, "/dashboard/waitlist/1", nil)
	r.Host = "clinic.local:8080"
	r.Header.Set("Origin", "http://clinic.local:8080")
	r.Header.Set("X-Dashboard-Request", "1")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if !called || w.Code != http.StatusOK {
		t.Fatalf("called=%v status=%d", called, w.Code)
	}
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("cache=%q", w.Header().Get("Cache-Control"))
	}
}
