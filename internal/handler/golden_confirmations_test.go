package handler

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/medkvadrat/medkvadrat-patient-api/internal/confirmations"
)

type fakeConfirmationsSvc struct {
	lastMotconsuID int64
	lastPatientID  int64
	lastStatus     string
	err            error
}

func (f *fakeConfirmationsSvc) UpsertConfirmation(ctx context.Context, motconsuID, patientID int64, status, source string) (confirmations.Record, error) {
	f.lastMotconsuID = motconsuID
	f.lastPatientID = patientID
	f.lastStatus = status
	if f.err != nil {
		return confirmations.Record{}, f.err
	}
	return confirmations.Record{
		MotconsuID: motconsuID,
		PatientID:  patientID,
		Status:     status,
		Source:     source,
		UpdatedAt:  time.Date(2026, 6, 23, 15, 0, 0, 0, time.UTC),
	}, nil
}

func TestGolden_Confirmations_Upsert_OK(t *testing.T) {
	svc := &fakeConfirmationsSvc{}
	h := ConfirmationsHandler{Svc: svc}
	body := `{"motconsu_id":12345,"patient_id":6789,"status":"confirmed"}`
	r := httptest.NewRequest(http.MethodPost, "/api/internal/confirmations", bytes.NewBufferString(body))
	w := httptest.NewRecorder()
	h.Upsert(w, r)
	assertGolden(t, "confirmations_upsert_ok", w.Body.Bytes())
	if svc.lastStatus != "confirmed" {
		t.Fatalf("status=%q", svc.lastStatus)
	}
}

func TestGolden_Confirmations_InvalidStatus(t *testing.T) {
	h := ConfirmationsHandler{Svc: &fakeConfirmationsSvc{}}
	body := `{"motconsu_id":1,"patient_id":2,"status":"maybe"}`
	r := httptest.NewRequest(http.MethodPost, "/api/internal/confirmations", bytes.NewBufferString(body))
	w := httptest.NewRecorder()
	h.Upsert(w, r)
	assertGolden(t, "confirmations_invalid_status", w.Body.Bytes())
}

func TestGolden_Confirmations_NotFound(t *testing.T) {
	h := ConfirmationsHandler{Svc: &fakeConfirmationsSvc{err: confirmations.ErrAppointmentNotFound}}
	body := `{"motconsu_id":1,"patient_id":2,"status":"confirmed"}`
	r := httptest.NewRequest(http.MethodPost, "/api/internal/confirmations", bytes.NewBufferString(body))
	w := httptest.NewRecorder()
	h.Upsert(w, r)
	assertGolden(t, "confirmations_not_found", w.Body.Bytes())
}

func TestGolden_Confirmations_PatientMismatch(t *testing.T) {
	h := ConfirmationsHandler{Svc: &fakeConfirmationsSvc{err: confirmations.ErrPatientMismatch}}
	body := `{"motconsu_id":1,"patient_id":2,"status":"declined"}`
	r := httptest.NewRequest(http.MethodPost, "/api/internal/confirmations", bytes.NewBufferString(body))
	w := httptest.NewRecorder()
	h.Upsert(w, r)
	assertGolden(t, "confirmations_patient_mismatch", w.Body.Bytes())
}

func Test__GenerateConfirmationsGolden(t *testing.T) {
	if os.Getenv("UPDATE_GOLDEN") != "1" {
		t.Skip("set UPDATE_GOLDEN=1 to (re)generate golden files")
	}
	svc := &fakeConfirmationsSvc{}
	cases := []struct {
		name string
		body string
		svc  ConfirmationsService
	}{
		{"confirmations_upsert_ok", `{"motconsu_id":12345,"patient_id":6789,"status":"confirmed"}`, svc},
		{"confirmations_invalid_status", `{"motconsu_id":1,"patient_id":2,"status":"maybe"}`, &fakeConfirmationsSvc{}},
		{"confirmations_not_found", `{"motconsu_id":1,"patient_id":2,"status":"confirmed"}`, &fakeConfirmationsSvc{err: confirmations.ErrAppointmentNotFound}},
		{"confirmations_patient_mismatch", `{"motconsu_id":1,"patient_id":2,"status":"declined"}`, &fakeConfirmationsSvc{err: confirmations.ErrPatientMismatch}},
	}
	for _, c := range cases {
		h := ConfirmationsHandler{Svc: c.svc}
		r := httptest.NewRequest(http.MethodPost, "/api/internal/confirmations", bytes.NewBufferString(c.body))
		w := httptest.NewRecorder()
		h.Upsert(w, r)
		writeGolden(t, c.name, w.Body.Bytes())
	}
}
