package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAuth_WithoutHeader_Unauthorized(t *testing.T) {
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/doctors", nil)

	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true })

	Auth{Token: "t"}.RequireBearer(next).ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d", rr.Code)
	}
	if called {
		t.Fatalf("next should not be called")
	}
}

func TestAuth_WithValidHeader_CallsNext(t *testing.T) {
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/doctors", nil)
	req.Header.Set("Authorization", "Bearer t")

	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true })

	Auth{Token: "t"}.RequireBearer(next).ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d", rr.Code)
	}
	if !called {
		t.Fatalf("next should be called")
	}
}
