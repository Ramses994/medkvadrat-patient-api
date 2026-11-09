package middleware

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRecover_DashboardPanicIs500(t *testing.T) {
	h := Recover(slog.New(slog.DiscardHandler), http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("dashboard")
	}))
	r := httptest.NewRequest(http.MethodGet, "/dashboard/waitlist", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "Внутренняя ошибка") {
		t.Fatalf("body=%s", w.Body.String())
	}
}
