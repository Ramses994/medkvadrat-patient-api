package handler

import (
	"context"
	"net/http"

	"github.com/medkvadrat/medkvadrat-patient-api/internal/response"
)

type HealthService interface {
	Ping(ctx context.Context) error
}

type HealthHandler struct {
	Svc HealthService
}

func (h HealthHandler) Health(w http.ResponseWriter, r *http.Request) {
	if err := h.Svc.Ping(r.Context()); err != nil {
		response.Error(w, http.StatusInternalServerError, "INTERNAL", "DB unavailable")
		return
	}
	response.OK(w, "OK")
}
