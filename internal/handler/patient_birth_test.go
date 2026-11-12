package handler

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/medkvadrat/medkvadrat-patient-api/internal/repo"
)

type birthSearch struct{}

func (birthSearch) PatientSearch(context.Context, string) ([]repo.PatientInfo, error) {
	when := time.Date(1985, 1, 2, 0, 0, 0, 0, time.UTC)
	return []repo.PatientInfo{{
		PatientID: 1,
		FullName:  "Иванов Иван",
		Phone:     "+79990000000",
		BirthDate: &when,
	}}, nil
}

func (birthSearch) LabResults(context.Context, string, int) ([]repo.LabResult, error) {
	return nil, nil
}

func (birthSearch) LabPanels(context.Context, string, int) ([]repo.LabPanel, error) {
	return nil, nil
}

func TestPatientSearch_BirthDateIsCalendarDay(t *testing.T) {
	h := PatientHandler{Svc: birthSearch{}, Logger: slog.New(slog.DiscardHandler)}
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/patients/search", strings.NewReader(`{"phone":"+79990000000"}`))
	h.Search(w, req)
	body := w.Body.String()
	if w.Code != http.StatusOK || !strings.Contains(body, `"birth_date":"1985-01-02"`) {
		t.Fatalf("%d %s", w.Code, body)
	}
}
