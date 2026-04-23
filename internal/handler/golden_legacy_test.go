package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/medkvadrat/medkvadrat-patient-api/internal/repo"
)

type fakeScheduleSvc struct{}

func (fakeScheduleSvc) ScheduleChanges(ctx context.Context, since time.Time) ([]repo.ScheduleChange, error) {
	return []repo.ScheduleChange{
		{
			MotconsuID:  1,
			PatientID:   2,
			PatientName: "Иван Иван",
			DoctorID:    3,
			DoctorName:  "Доктор Док",
			DateConsult: time.Date(2026, 4, 23, 10, 30, 0, 0, time.Local),
			ModifyDate:  time.Date(2026, 4, 23, 9, 0, 0, 0, time.Local),
		},
	}, nil
}
func (fakeScheduleSvc) FreeSlots(ctx context.Context, doctorID, date string) ([]repo.FreeSlot, error) {
	return []repo.FreeSlot{
		{
			PlanningID: 10,
			DoctorName: "Терапевт",
			PlSubjID:   20,
			DateCons:   time.Date(2026, 4, 24, 0, 0, 0, 0, time.Local),
			Heure:      930,
			Duration:   15,
		},
	}, nil
}
func (fakeScheduleSvc) Book(ctx context.Context, in repo.BookInput) (repo.BookResult, error) {
	return repo.BookResult{MotconsuID: 123}, nil
}

type fakeDoctorSvc struct{}

func (fakeDoctorSvc) Doctors(ctx context.Context) ([]repo.Doctor, error) {
	return []repo.Doctor{
		{DoctorID: 7, FullName: "Иван Петров", Specialty: ""},
	}, nil
}

type fakePatientSvc struct{}

func (fakePatientSvc) PatientSearch(ctx context.Context, phone string) ([]repo.PatientInfo, error) {
	return []repo.PatientInfo{{PatientID: 1, FullName: "Иван Иванов", Phone: "+79990000000"}}, nil
}
func (fakePatientSvc) LabResults(ctx context.Context, patientID string, daysBack int) ([]repo.LabResult, error) {
	return []repo.LabResult{
		{
			ResultID:   1,
			PatdirecID: 2,
			Name:       "Глюкоза",
			Value:      "5.1",
			Unit:       "ммоль/л",
			Norms:      "3.9-5.5",
			ReadyAt:    time.Date(2026, 4, 22, 8, 0, 0, 0, time.Local),
		},
	}, nil
}
func (fakePatientSvc) LabPanels(ctx context.Context, patientID string, daysBack int) ([]repo.LabPanel, error) {
	return []repo.LabPanel{
		{
			PatdirecID:    2,
			PanelName:     "Биохимия",
			OrderedAt:     "2026-04-21 10:00",
			ReadyAt:       "2026-04-22 08:00",
			TestsCount:    1,
			HasOutOfRange: false,
			Tests: []repo.LabResultDTO{
				{
					ResultID:   1,
					PatdirecID: 2,
					Name:       "Глюкоза",
					Value:      "5.1",
					Unit:       "ммоль/л",
					Norms:      "3.9-5.5",
					ReadyAt:    "2026-04-22 08:00",
				},
			},
		},
	}, nil
}

func writeGolden(t *testing.T, name string, b []byte) {
	t.Helper()
	dir := filepath.Join("testdata", "golden")
	_ = os.MkdirAll(dir, 0o755)
	path := filepath.Join(dir, name+".json")
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatalf("write golden: %v", err)
	}
}

func readGolden(t *testing.T, name string) []byte {
	t.Helper()
	path := filepath.Join("testdata", "golden", name+".json")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden %s: %v", name, err)
	}
	return b
}

func assertGolden(t *testing.T, name string, got []byte) {
	t.Helper()
	want := readGolden(t, name)
	if !bytes.Equal(want, got) {
		t.Fatalf("golden mismatch: %s\nwant=%s\ngot=%s", name, string(want), string(got))
	}
}

func TestGolden_Doctors_OK_LegacyShape(t *testing.T) {
	h := DoctorHandler{Svc: fakeDoctorSvc{}, Logger: nil}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/doctors", nil)

	h.List(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d", rr.Code)
	}
	assertGolden(t, "doctors_ok", rr.Body.Bytes())
}

func TestGolden_PatientSearch_OK_LegacyShape(t *testing.T) {
	h := PatientHandler{Svc: fakePatientSvc{}, Logger: nil}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/patients/search?phone=+7999", nil)

	h.Search(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d", rr.Code)
	}
	assertGolden(t, "patients_search_ok", rr.Body.Bytes())
}

func TestGolden_ScheduleSlots_OK_LegacyShape(t *testing.T) {
	h := ScheduleHandler{Svc: fakeScheduleSvc{}, Logger: nil}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/schedule/slots?doctor_id=1&date=2026-04-24", nil)

	h.FreeSlots(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d", rr.Code)
	}
	assertGolden(t, "slots_ok", rr.Body.Bytes())
}

func TestGolden_ScheduleChanges_OK_LegacyShape(t *testing.T) {
	h := ScheduleHandler{Svc: fakeScheduleSvc{}, Logger: nil}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/schedule/changes?since=2026-04-23T00:00:00", nil)

	h.Changes(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d", rr.Code)
	}
	assertGolden(t, "changes_ok", rr.Body.Bytes())
}

func TestGolden_LabResults_OK_LegacyShape(t *testing.T) {
	h := PatientHandler{Svc: fakePatientSvc{}, Logger: nil}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/patients/lab-results?patient_id=1", nil)

	h.LabResults(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d", rr.Code)
	}
	assertGolden(t, "lab_results_ok", rr.Body.Bytes())
}

func TestGolden_LabPanels_OK_LegacyShape(t *testing.T) {
	h := PatientHandler{Svc: fakePatientSvc{}, Logger: nil}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/patients/lab-panels?patient_id=1", nil)

	h.LabPanels(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d", rr.Code)
	}
	assertGolden(t, "lab_panels_ok", rr.Body.Bytes())
}

func TestGolden_Slots_ValidationError_LegacyShape(t *testing.T) {
	h := ScheduleHandler{Svc: fakeScheduleSvc{}, Logger: nil}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/schedule/slots", nil)

	h.FreeSlots(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status=%d", rr.Code)
	}
	assertGolden(t, "slots_validation_error", rr.Body.Bytes())
}

func TestGolden_Book_InvalidJSON_LegacyShape(t *testing.T) {
	h := ScheduleHandler{Svc: fakeScheduleSvc{}, Logger: nil}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/schedule/book", bytes.NewBufferString("{"))

	h.Book(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status=%d", rr.Code)
	}
	assertGolden(t, "book_invalid_json", rr.Body.Bytes())
}

// Helper to generate initial golden files (run manually once, then commit testdata).
func Test__GenerateGoldenFiles(t *testing.T) {
	if os.Getenv("UPDATE_GOLDEN") != "1" {
		t.Skip("set UPDATE_GOLDEN=1 to (re)generate golden files")
	}

	// doctors
	{
		h := DoctorHandler{Svc: fakeDoctorSvc{}, Logger: nil}
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/doctors", nil)
		h.List(rr, req)
		writeGolden(t, "doctors_ok", rr.Body.Bytes())
	}

	// patient search
	{
		h := PatientHandler{Svc: fakePatientSvc{}, Logger: nil}
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/patients/search?phone=+7999", nil)
		h.Search(rr, req)
		writeGolden(t, "patients_search_ok", rr.Body.Bytes())
	}

	// slots
	{
		h := ScheduleHandler{Svc: fakeScheduleSvc{}, Logger: nil}
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/schedule/slots?doctor_id=1&date=2026-04-24", nil)
		h.FreeSlots(rr, req)
		writeGolden(t, "slots_ok", rr.Body.Bytes())
	}

	// changes
	{
		h := ScheduleHandler{Svc: fakeScheduleSvc{}, Logger: nil}
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/schedule/changes?since=2026-04-23T00:00:00", nil)
		h.Changes(rr, req)
		writeGolden(t, "changes_ok", rr.Body.Bytes())
	}

	// lab results
	{
		h := PatientHandler{Svc: fakePatientSvc{}, Logger: nil}
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/patients/lab-results?patient_id=1", nil)
		h.LabResults(rr, req)
		writeGolden(t, "lab_results_ok", rr.Body.Bytes())
	}

	// lab panels
	{
		h := PatientHandler{Svc: fakePatientSvc{}, Logger: nil}
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/patients/lab-panels?patient_id=1", nil)
		h.LabPanels(rr, req)
		writeGolden(t, "lab_panels_ok", rr.Body.Bytes())
	}

	// slots validation error
	{
		h := ScheduleHandler{Svc: fakeScheduleSvc{}, Logger: nil}
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/schedule/slots", nil)
		h.FreeSlots(rr, req)
		writeGolden(t, "slots_validation_error", rr.Body.Bytes())
	}

	// book invalid json
	{
		h := ScheduleHandler{Svc: fakeScheduleSvc{}, Logger: nil}
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/schedule/book", bytes.NewBufferString("{"))
		h.Book(rr, req)
		writeGolden(t, "book_invalid_json", rr.Body.Bytes())
	}

	// sanity: JSON must be valid
	for _, n := range []string{
		"doctors_ok",
		"patients_search_ok",
		"slots_ok",
		"changes_ok",
		"lab_results_ok",
		"lab_panels_ok",
		"slots_validation_error",
		"book_invalid_json",
	} {
		b := readGolden(t, n)
		var v any
		if err := json.Unmarshal(b, &v); err != nil {
			t.Fatalf("invalid json %s: %v", n, err)
		}
	}
}
