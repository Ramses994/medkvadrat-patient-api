package handler

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/medkvadrat/medkvadrat-patient-api/internal/repo"
)

type countingSearch struct {
	calls int
}

func (c *countingSearch) PatientSearch(context.Context, string) ([]repo.PatientInfo, error) {
	c.calls++
	return nil, nil
}

func (c *countingSearch) LabResults(context.Context, string, int) ([]repo.LabResult, error) {
	return nil, nil
}

func (c *countingSearch) LabPanels(context.Context, string, int) ([]repo.LabPanel, error) {
	return nil, nil
}

func TestPatientSearch_InvalidPhoneDoesNotQuery(t *testing.T) {
	for _, phone := range []string{"", "abc"} {
		svc := &countingSearch{}
		h := PatientHandler{Svc: svc, Logger: slog.New(slog.DiscardHandler)}
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/patients/search", strings.NewReader(`{"phone":"`+phone+`"}`))
		h.Search(w, req)
		if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), `"code":"VALIDATION"`) {
			t.Fatalf("phone %q: %d %s", phone, w.Code, w.Body.String())
		}
		if svc.calls != 0 {
			t.Fatalf("phone %q queried the clinic %d times", phone, svc.calls)
		}
	}
}
