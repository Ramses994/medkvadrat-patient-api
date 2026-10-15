package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/medkvadrat/medkvadrat-patient-api/internal/health"
)

type okHealthSvc struct{}

func (okHealthSvc) HealthSnapshot(ctx context.Context) health.Snapshot {
	return health.Snapshot{API: "ok", Medialog: "ok"}
}

func TestHealth_OK(t *testing.T) {
	h := HealthHandler{Svc: okHealthSvc{}}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/health", nil)

	h.Health(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d", rr.Code)
	}
	var got map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got["success"] != true {
		t.Fatalf("success=%v", got["success"])
	}
	data, ok := got["data"].(map[string]any)
	if !ok || data["api"] != "ok" {
		t.Fatalf("data=%v", got["data"])
	}
}
