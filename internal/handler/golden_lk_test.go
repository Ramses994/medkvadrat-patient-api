package handler

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/medkvadrat/medkvadrat-patient-api/internal/middleware"
)

func TestGolden_LK_FeatureDisabled_ScheduleBook(t *testing.T) {
	h := ScheduleHandler{}
	r := httptest.NewRequest(http.MethodPost, "/api/schedule/book", bytes.NewBufferString(`{"planning_id":1,"patient_id":2}`))
	w := httptest.NewRecorder()
	h.Book(w, r)
	assertGolden(t, "lk_feature_disabled_schedule_book", w.Body.Bytes())
}

func TestGolden_LK_FeatureDisabled_MeBook(t *testing.T) {
	h := MeHandler{}
	r := httptest.NewRequest(http.MethodPost, "/api/me/appointments", bytes.NewBufferString(`{"planning_id":1}`))
	ctx := middleware.WithPatientID(r.Context(), 1587578)
	r = r.WithContext(ctx)
	w := httptest.NewRecorder()
	h.BookAppointment(w, r)
	assertGolden(t, "lk_feature_disabled_me_book", w.Body.Bytes())
}

func TestGolden_LK_FeatureDisabled_MeCancel(t *testing.T) {
	h := MeHandler{}
	r := httptest.NewRequest(http.MethodDelete, "/api/me/appointments/99", nil)
	r = r.WithContext(middleware.WithPatientID(r.Context(), 1587578))
	r.SetPathValue("motconsu_id", "99")
	w := httptest.NewRecorder()
	h.CancelAppointment(w, r)
	assertGolden(t, "lk_feature_disabled_me_cancel", w.Body.Bytes())
}

func Test__GenerateLKGolden(t *testing.T) {
	if os.Getenv("UPDATE_GOLDEN") != "1" {
		t.Skip("set UPDATE_GOLDEN=1 to (re)generate golden files")
	}
	tests := []struct {
		name string
		run  func() []byte
	}{
		{"lk_feature_disabled_schedule_book", func() []byte {
			w := httptest.NewRecorder()
			ScheduleHandler{}.Book(w, httptest.NewRequest(http.MethodPost, "/api/schedule/book", nil))
			return w.Body.Bytes()
		}},
		{"lk_feature_disabled_me_book", func() []byte {
			h := MeHandler{}
			r := httptest.NewRequest(http.MethodPost, "/api/me/appointments", bytes.NewBufferString(`{"planning_id":1}`))
			r = r.WithContext(middleware.WithPatientID(r.Context(), 1587578))
			w := httptest.NewRecorder()
			h.BookAppointment(w, r)
			return w.Body.Bytes()
		}},
		{"lk_feature_disabled_me_cancel", func() []byte {
			h := MeHandler{}
			r := httptest.NewRequest(http.MethodDelete, "/api/me/appointments/99", nil)
			r = r.WithContext(middleware.WithPatientID(r.Context(), 1587578))
			r.SetPathValue("motconsu_id", "99")
			w := httptest.NewRecorder()
			h.CancelAppointment(w, r)
			return w.Body.Bytes()
		}},
	}
	for _, tc := range tests {
		writeGolden(t, tc.name, tc.run())
	}
}

func TestLegacyWriteHandlers_Preserved(t *testing.T) {
	var mh MeHandler
	var sh ScheduleHandler
	var ch CatalogHandler
	_ = mh.bookAppointmentLegacy
	_ = mh.cancelAppointmentLegacy
	_ = sh.bookLegacy
	_ = ch
}
