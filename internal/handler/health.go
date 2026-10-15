package handler

import (
	"context"
	"net/http"

	"github.com/medkvadrat/medkvadrat-patient-api/internal/health"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/response"
)

type HealthService interface {
	HealthSnapshot(ctx context.Context) health.Snapshot
}

type HealthHandler struct {
	Svc HealthService
}

func (h HealthHandler) Health(w http.ResponseWriter, r *http.Request) {
	response.OK(w, h.Svc.HealthSnapshot(r.Context()))
}
