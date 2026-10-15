package handler

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/medkvadrat/medkvadrat-patient-api/internal/medialogwrite"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/middleware"
)

func TestGolden_LK_FeatureDisabled_ScheduleSlots(t *testing.T) {
	h := ScheduleHandler{}
	r := httptest.NewRequest(http.MethodGet, "/api/schedule/slots?doctor_id=1&date=2026-06-26", nil)
	w := httptest.NewRecorder()
	h.FreeSlots(w, r)
	assertGolden(t, "lk_feature_disabled_schedule_slots", w.Body.Bytes())
}

func TestGolden_LK_FeatureDisabled_CatalogSlots(t *testing.T) {
	h := CatalogHandler{}
	r := httptest.NewRequest(http.MethodGet, "/api/catalog/slots?doctor_id=1&date_from=2026-06-26&date_to=2026-06-27", nil)
	w := httptest.NewRecorder()
	h.Slots(w, r)
	assertGolden(t, "lk_feature_disabled_catalog_slots", w.Body.Bytes())
}

func TestGolden_LK_FeatureDisabled_ScheduleBook(t *testing.T) {
	h := ScheduleHandler{}
	r := httptest.NewRequest(http.MethodPost, "/api/schedule/book", bytes.NewBufferString(`{"planning_id":1,"patient_id":2}`))
	w := httptest.NewRecorder()
	h.Book(w, r)
	assertGolden(t, "lk_feature_disabled_schedule_book", w.Body.Bytes())
}

func TestGolden_LK_WriteDisabled_MeBook(t *testing.T) {
	h := MeHandler{
		Svc:        fakeMeWriteSvc{},
		WriteGuard: medialogwrite.HTTPDeps{Guard: medialogwrite.NewGuard(false, true, nil, nil)},
	}
	r := httptest.NewRequest(http.MethodPost, "/api/me/appointments", bytes.NewBufferString(`{"planning_id":1}`))
	ctx := middleware.WithPatientID(r.Context(), 1587578)
	r = r.WithContext(ctx)
	w := httptest.NewRecorder()
	h.BookAppointment(w, r)
	assertGolden(t, "lk_write_disabled_me_book", w.Body.Bytes())
}

func Test__GenerateLKGolden(t *testing.T) {
	if os.Getenv("UPDATE_GOLDEN") != "1" {
		t.Skip("set UPDATE_GOLDEN=1 to (re)generate golden files")
	}
	tests := []struct {
		name string
		run  func() []byte
	}{
		{"lk_feature_disabled_schedule_slots", func() []byte {
			w := httptest.NewRecorder()
			ScheduleHandler{}.FreeSlots(w, httptest.NewRequest(http.MethodGet, "/api/schedule/slots", nil))
			return w.Body.Bytes()
		}},
		{"lk_feature_disabled_catalog_slots", func() []byte {
			w := httptest.NewRecorder()
			CatalogHandler{}.Slots(w, httptest.NewRequest(http.MethodGet, "/api/catalog/slots", nil))
			return w.Body.Bytes()
		}},
		{"lk_feature_disabled_schedule_book", func() []byte {
			w := httptest.NewRecorder()
			ScheduleHandler{}.Book(w, httptest.NewRequest(http.MethodPost, "/api/schedule/book", nil))
			return w.Body.Bytes()
		}},
		{"lk_write_disabled_me_book", func() []byte {
			h := MeHandler{Svc: fakeMeWriteSvc{}, WriteGuard: medialogwrite.HTTPDeps{Guard: medialogwrite.NewGuard(false, true, nil, nil)}}
			r := httptest.NewRequest(http.MethodPost, "/api/me/appointments", bytes.NewBufferString(`{"planning_id":1}`))
			r = r.WithContext(middleware.WithPatientID(r.Context(), 1587578))
			w := httptest.NewRecorder()
			h.BookAppointment(w, r)
			return w.Body.Bytes()
		}},
	}
	for _, tc := range tests {
		writeGolden(t, tc.name, tc.run())
	}
}

func TestLegacySlotHandlers_Preserved(t *testing.T) {
	var sh ScheduleHandler
	var ch CatalogHandler
	_ = sh.freeSlotsLegacy
	_ = sh.bookLegacy
	_ = ch.slotsLegacy
}
