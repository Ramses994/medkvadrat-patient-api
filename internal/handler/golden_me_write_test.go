package handler

import (
	"bytes"
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/medkvadrat/medkvadrat-patient-api/internal/middleware"
)

type fakeMeWriteSvc struct{ fakeMeSvc }

func (fakeMeWriteSvc) MeBookAppointment(ctx context.Context, patientID int64, planningID int, now time.Time) (int64, bool, error) {
	switch planningID {
	case 1:
		return 0, false, errStr("SLOT_TAKEN")
	case 2:
		return 0, false, errStr("SLOT_IN_PAST")
	case 3:
		return 0, false, errStr("ALREADY_BOOKED")
	default:
		return 777, planningID == 9, nil
	}
}

func (fakeMeWriteSvc) MeCancelAppointment(ctx context.Context, patientID int64, motconsuID int64, now time.Time) error {
	switch motconsuID {
	case 1:
		return errStr("FORBIDDEN")
	case 2:
		return errStr("ALREADY_CANCELLED")
	case 3:
		return errStr("CANCEL_NOT_SUPPORTED")
	case 4:
		return errStr("CANCEL_TOO_LATE")
	case 5:
		return sql.ErrNoRows
	default:
		return nil
	}
}

type errStr string

func (e errStr) Error() string { return string(e) }

func TestGolden_Me_Book_OK(t *testing.T) {
	h := MeHandler{Svc: fakeMeWriteSvc{}}
	r := httptest.NewRequest(http.MethodPost, "/api/me/appointments", bytes.NewBufferString(`{"planning_id":9}`))
	r = r.WithContext(middleware.WithPatientID(r.Context(), 1))
	w := httptest.NewRecorder()
	h.BookAppointment(w, r)
	assertGolden(t, "me_book_ok", w.Body.Bytes())
}

func TestGolden_Me_Book_SlotTaken(t *testing.T) {
	h := MeHandler{Svc: fakeMeWriteSvc{}}
	r := httptest.NewRequest(http.MethodPost, "/api/me/appointments", bytes.NewBufferString(`{"planning_id":1}`))
	r = r.WithContext(middleware.WithPatientID(r.Context(), 1))
	w := httptest.NewRecorder()
	h.BookAppointment(w, r)
	assertGolden(t, "me_book_slot_taken", w.Body.Bytes())
}

func TestGolden_Me_Book_InvalidJSON(t *testing.T) {
	h := MeHandler{Svc: fakeMeWriteSvc{}}
	r := httptest.NewRequest(http.MethodPost, "/api/me/appointments", bytes.NewBufferString(`{`))
	r = r.WithContext(middleware.WithPatientID(r.Context(), 1))
	w := httptest.NewRecorder()
	h.BookAppointment(w, r)
	assertGolden(t, "me_book_invalid_json", w.Body.Bytes())
}

func TestGolden_Me_Cancel_OK(t *testing.T) {
	h := MeHandler{Svc: fakeMeWriteSvc{}, CancelMinHours: 24}
	r := httptest.NewRequest(http.MethodDelete, "/api/me/appointments/10", nil)
	r.SetPathValue("motconsu_id", "10")
	r = r.WithContext(middleware.WithPatientID(r.Context(), 1))
	w := httptest.NewRecorder()
	h.CancelAppointment(w, r)
	assertGolden(t, "me_cancel_ok", w.Body.Bytes())
}

func TestGolden_Me_Cancel_TooLate(t *testing.T) {
	h := MeHandler{Svc: fakeMeWriteSvc{}, CancelMinHours: 24}
	r := httptest.NewRequest(http.MethodDelete, "/api/me/appointments/4", nil)
	r.SetPathValue("motconsu_id", "4")
	r = r.WithContext(middleware.WithPatientID(r.Context(), 1))
	w := httptest.NewRecorder()
	h.CancelAppointment(w, r)
	assertGolden(t, "me_cancel_too_late", w.Body.Bytes())
}

func TestGolden_Me_Cancel_Forbidden(t *testing.T) {
	h := MeHandler{Svc: fakeMeWriteSvc{}, CancelMinHours: 24}
	r := httptest.NewRequest(http.MethodDelete, "/api/me/appointments/1", nil)
	r.SetPathValue("motconsu_id", "1")
	r = r.WithContext(middleware.WithPatientID(r.Context(), 1))
	w := httptest.NewRecorder()
	h.CancelAppointment(w, r)
	assertGolden(t, "me_cancel_forbidden", w.Body.Bytes())
}

func TestGolden_Me_Cancel_AlreadyCancelled(t *testing.T) {
	h := MeHandler{Svc: fakeMeWriteSvc{}, CancelMinHours: 24}
	r := httptest.NewRequest(http.MethodDelete, "/api/me/appointments/2", nil)
	r.SetPathValue("motconsu_id", "2")
	r = r.WithContext(middleware.WithPatientID(r.Context(), 1))
	w := httptest.NewRecorder()
	h.CancelAppointment(w, r)
	assertGolden(t, "me_cancel_already_cancelled", w.Body.Bytes())
}

func TestGolden_Me_Cancel_NotSupported(t *testing.T) {
	h := MeHandler{Svc: fakeMeWriteSvc{}, CancelMinHours: 24}
	r := httptest.NewRequest(http.MethodDelete, "/api/me/appointments/3", nil)
	r.SetPathValue("motconsu_id", "3")
	r = r.WithContext(middleware.WithPatientID(r.Context(), 1))
	w := httptest.NewRecorder()
	h.CancelAppointment(w, r)
	assertGolden(t, "me_cancel_not_supported", w.Body.Bytes())
}

func TestGolden_Me_Cancel_NotFound(t *testing.T) {
	h := MeHandler{Svc: fakeMeWriteSvc{}, CancelMinHours: 24}
	r := httptest.NewRequest(http.MethodDelete, "/api/me/appointments/5", nil)
	r.SetPathValue("motconsu_id", "5")
	r = r.WithContext(middleware.WithPatientID(r.Context(), 1))
	w := httptest.NewRecorder()
	h.CancelAppointment(w, r)
	assertGolden(t, "me_cancel_not_found", w.Body.Bytes())
}
