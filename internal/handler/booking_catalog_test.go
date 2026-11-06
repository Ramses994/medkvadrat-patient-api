package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/medkvadrat/medkvadrat-patient-api/internal/middleware"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/repo"
)

const slotSecret = "0123456789abcdef0123456789abcdef"

type fakeBooking struct {
	err    error
	offers []repo.BookingSlot
	opts   []repo.BookingOption
}

func (f fakeBooking) BookingOptions(context.Context) ([]repo.BookingOption, error) {
	return f.opts, f.err
}
func (f fakeBooking) BookingSlots(context.Context, int64, int64, string, string, time.Time) ([]repo.BookingSlot, error) {
	return f.offers, f.err
}

func TestBookingSlots_HTTP(t *testing.T) {
	now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.FixedZone("MSK", 3*3600))
	h := BookingCatalogHandler{
		Svc: fakeBooking{offers: []repo.BookingSlot{{
			Start: "2026-11-06T23:30:00", DurationMin: 30, BranchID: 496, PlSubjID: 1119, PlExamID: 10000688,
		}}},
		SlotTokenSecret: slotSecret,
		Now:             func() time.Time { return now },
	}
	r := httptest.NewRequest(http.MethodGet, "/api/catalog/slots?pl_subj_id=1119&pl_exam_id=10000688&date_from=2026-11-06&date_to=2026-11-06", nil)
	r = r.WithContext(middleware.WithPatientID(r.Context(), 1))
	w := httptest.NewRecorder()
	h.Slots(w, r)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"start":"2026-11-06T23:30:00"`) {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}

	h.Svc = fakeBooking{err: repo.ErrBadSlotWindow}
	r = httptest.NewRequest(http.MethodGet, "/api/catalog/slots?pl_subj_id=1119&pl_exam_id=10000688&date_from=2026-11-06&date_to=2026-11-20", nil)
	r = r.WithContext(middleware.WithPatientID(r.Context(), 1))
	w = httptest.NewRecorder()
	h.Slots(w, r)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "VALIDATION") {
		t.Fatalf("15 days: %d %s", w.Code, w.Body.String())
	}

	h.Svc = fakeBooking{err: repo.ErrInactivePair}
	r = httptest.NewRequest(http.MethodGet, "/api/catalog/slots?pl_subj_id=1&pl_exam_id=2&date_from=2026-11-06&date_to=2026-11-06", nil)
	r = r.WithContext(middleware.WithPatientID(r.Context(), 1))
	w = httptest.NewRecorder()
	h.Slots(w, r)
	if w.Code != http.StatusNotFound {
		t.Fatalf("inactive: %d %s", w.Code, w.Body.String())
	}

	h.SlotTokenSecret = "short"
	w = httptest.NewRecorder()
	h.Slots(w, r)
	if w.Code != http.StatusServiceUnavailable || !strings.Contains(w.Body.String(), "SLOT_TOKEN_UNAVAILABLE") {
		t.Fatalf("secret: %d %s", w.Code, w.Body.String())
	}
}
