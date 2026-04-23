package api

import (
	"log/slog"
	"net/http"
	"strings"

	"github.com/medkvadrat/medkvadrat-patient-api/internal/config"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/handler"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/middleware"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/service"
)

func NewRouter(cfg config.Config, svc *service.Services, logger *slog.Logger) http.Handler {
	mux := http.NewServeMux()

	healthH := handler.HealthHandler{Svc: svc}
	doctorH := handler.DoctorHandler{Svc: svc, Logger: logger}
	patientH := handler.PatientHandler{Svc: svc, Logger: logger}
	scheduleH := handler.ScheduleHandler{Svc: svc, Logger: logger}

	// Public
	mux.HandleFunc("GET /api/health", healthH.Health)

	// Protected (step 1: protect all existing /api/* endpoints except /api/health)
	mux.HandleFunc("/api/schedule/changes", scheduleH.Changes)
	mux.HandleFunc("/api/schedule/slots", scheduleH.FreeSlots)
	mux.HandleFunc("/api/schedule/book", scheduleH.Book)
	mux.HandleFunc("/api/doctors", doctorH.List)
	mux.HandleFunc("/api/patients/search", patientH.Search)
	mux.HandleFunc("/api/patients/lab-results", patientH.LabResults)
	mux.HandleFunc("/api/patients/lab-panels", patientH.LabPanels)

	auth := middleware.Auth{Token: cfg.APIToken}
	var base http.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") && r.URL.Path != "/api/health" {
			auth.RequireBearer(mux).ServeHTTP(w, r)
			return
		}
		mux.ServeHTTP(w, r)
	})

	// Middleware chain:
	// RequestID → Logging → Recover → CORS → Auth (prefix) → handler
	var h http.Handler = base
	h = middleware.RequestID(h)
	h = middleware.Logging(logger, h)
	h = middleware.Recover(logger, h)
	h = middleware.CORS(h)

	return h
}
