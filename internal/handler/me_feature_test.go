package handler

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/medkvadrat/medkvadrat-patient-api/internal/middleware"
)

type countingMeSvc struct {
	fakeMeWriteSvc
	bookCalls   int
	cancelCalls int
}

func (c *countingMeSvc) MeBookAppointment(context.Context, int64, int, time.Time) (int64, bool, error) {
	c.bookCalls++
	return 1, false, nil
}

func (c *countingMeSvc) MeCancelAppointment(context.Context, int64, int64, time.Time) error {
	c.cancelCalls++
	return nil
}

func TestMeBookAppointment_FeatureDisabled_NoServiceCall(t *testing.T) {
	svc := &countingMeSvc{}
	h := MeHandler{
		Svc:        svc,
		WriteGuard: permissiveWriteGuard(t),
	}
	r := httptest.NewRequest(http.MethodPost, "/api/me/appointments", bytes.NewBufferString(`{"planning_id":1}`))
	r = r.WithContext(middleware.WithPatientID(r.Context(), 1587578))
	w := httptest.NewRecorder()
	h.BookAppointment(w, r)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d", w.Code)
	}
	if svc.bookCalls != 0 {
		t.Fatalf("bookCalls=%d", svc.bookCalls)
	}
}

func TestMeCancelAppointment_FeatureDisabled_NoServiceCall(t *testing.T) {
	svc := &countingMeSvc{}
	h := MeHandler{
		Svc:        svc,
		WriteGuard: permissiveWriteGuard(t),
	}
	r := httptest.NewRequest(http.MethodDelete, "/api/me/appointments/99", nil)
	r = r.WithContext(middleware.WithPatientID(r.Context(), 1587578))
	r.SetPathValue("motconsu_id", "99")
	w := httptest.NewRecorder()
	h.CancelAppointment(w, r)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d", w.Code)
	}
	if svc.cancelCalls != 0 {
		t.Fatalf("cancelCalls=%d", svc.cancelCalls)
	}
}

func TestMeBookAppointmentLegacy_AllowsServiceWhenGuardPasses(t *testing.T) {
	svc := &countingMeSvc{}
	h := MeHandler{
		Svc:        svc,
		WriteGuard: permissiveWriteGuard(t),
	}
	r := httptest.NewRequest(http.MethodPost, "/api/me/appointments", bytes.NewBufferString(`{"planning_id":9}`))
	r = r.WithContext(middleware.WithPatientID(r.Context(), 1))
	w := httptest.NewRecorder()
	h.bookAppointmentLegacy(w, r)
	if svc.bookCalls != 1 {
		t.Fatalf("bookCalls=%d status=%d", svc.bookCalls, w.Code)
	}
}
