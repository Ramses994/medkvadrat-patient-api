package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/medkvadrat/medkvadrat-patient-api/internal/booking"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/middleware"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/writeops"
)

type fakeBookingAPI struct {
	out booking.WriteHTTP
}

func (f fakeBookingAPI) BookSlot(context.Context, int64, string, string) booking.WriteHTTP {
	return f.out
}
func (f fakeBookingAPI) CancelBooking(context.Context, int64, int64, string) booking.WriteHTTP {
	return f.out
}
func (f fakeBookingAPI) ListWriteOps(context.Context, []string) ([]writeops.Op, error) {
	return nil, nil
}
func (f fakeBookingAPI) ResolveWriteOp(context.Context, booking.ResolveInput) booking.WriteHTTP {
	return f.out
}

func TestBook_MissingIdempotencyKey(t *testing.T) {
	h := BookingHandler{API: fakeBookingAPI{}}
	r := httptest.NewRequest(http.MethodPost, "/api/me/appointments", strings.NewReader(`{"slot_token":"x"}`))
	r = r.WithContext(middleware.WithPatientID(r.Context(), 1))
	w := httptest.NewRecorder()
	h.Book(w, r)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status %d", w.Code)
	}
	if strings.Contains(w.Body.String(), "SELECT") || strings.Contains(w.Body.String(), "Произошла ошибка") {
		t.Fatalf("raw text %s", w.Body.String())
	}
}

func TestWriteOut_PatientMessageOnly(t *testing.T) {
	raw := "Произошла ошибка при выполнении записи в расписание - Cannot insert"
	h := BookingHandler{API: fakeBookingAPI{out: booking.WriteHTTP{
		Status: http.StatusInternalServerError, Code: "BOOKING_ERROR", Message: booking.Message("BOOKING_ERROR"),
	}}}
	r := httptest.NewRequest(http.MethodPost, "/api/me/appointments", strings.NewReader(`{"slot_token":"x"}`))
	r = r.WithContext(middleware.WithPatientID(r.Context(), 1))
	r.Header.Set("Idempotency-Key", "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")
	w := httptest.NewRecorder()
	h.Book(w, r)
	if strings.Contains(w.Body.String(), raw) || strings.Contains(w.Body.String(), "Cannot insert") {
		t.Fatalf("leaked %s", w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "BOOKING_ERROR") {
		t.Fatalf("body %s", w.Body.String())
	}
}
