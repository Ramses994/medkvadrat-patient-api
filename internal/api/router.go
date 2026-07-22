package api

import (
	"log/slog"
	"net/http"
	"strings"

	"github.com/medkvadrat/medkvadrat-patient-api/internal/config"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/handler"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/middleware"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/ratelimit"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/service"
	"github.com/medkvadrat/medkvadrat-patient-api/web"
)

func NewRouter(cfg config.Config, svc *service.Services, logger *slog.Logger) http.Handler {
	mux := http.NewServeMux()

	healthH := handler.HealthHandler{Svc: svc}
	authH := handler.AuthHandler{Svc: svc, Logger: logger}
	doctorH := handler.DoctorHandler{Svc: svc, Logger: logger}
	patientH := handler.PatientHandler{Svc: svc, Logger: logger}
	scheduleH := handler.ScheduleHandler{Svc: svc, Logger: logger}
	catalogH := handler.CatalogHandler{Svc: svc, Logger: logger}
	meH := handler.MeHandler{Svc: svc, Logger: logger, CancelMinHours: cfg.CancelMinHoursBefore}
	confirmationsH := handler.ConfirmationsHandler{Svc: svc, Logger: logger}
	remindersH := handler.RemindersHandler{Svc: svc, Logger: logger}
	dashboardH := handler.DashboardHandler{Svc: svc, Logger: logger}
	dashboardPageH := handler.DashboardPageHandler{
		HTML:         web.DashboardHTML,
		Password:     cfg.Dashboard.Password,
		Secret:       []byte(cfg.Dashboard.Secret),
		SessionTTL:   cfg.Dashboard.SessionTTL,
		CookieSecure: cfg.Dashboard.CookieSecure,
		RateLimit:    ratelimit.NewStore(svc.SQLite),
		Logger:       logger,
	}

	// Public
	mux.HandleFunc("GET /api/health", healthH.Health)
	mux.HandleFunc("POST /api/auth/otp/request", authH.OTPRequest)
	mux.HandleFunc("POST /api/auth/otp/verify", authH.OTPVerify)
	mux.HandleFunc("POST /api/auth/otp/select-patient", authH.OTPSelectPatient)
	mux.HandleFunc("POST /api/auth/refresh", authH.Refresh)
	mux.HandleFunc("POST /api/auth/logout", authH.Logout)

	// Protected (step 1: protect all existing /api/* endpoints except /api/health)
	mux.HandleFunc("/api/schedule/changes", scheduleH.Changes)
	mux.HandleFunc("/api/schedule/slots", scheduleH.FreeSlots)
	mux.HandleFunc("/api/schedule/book", scheduleH.Book)
	mux.HandleFunc("/api/doctors", doctorH.List)
	mux.HandleFunc("/api/patients/search", patientH.Search)
	mux.HandleFunc("/api/patients/lab-results", patientH.LabResults)
	mux.HandleFunc("/api/patients/lab-panels", patientH.LabPanels)
	mux.HandleFunc("GET /api/reminders/due", remindersH.Due)
	mux.HandleFunc("GET /api/dashboard/schedule", dashboardH.Schedule)

	// Patient JWT-protected (step 3)
	mux.HandleFunc("GET /api/catalog/specialties", catalogH.Specialties)
	mux.HandleFunc("GET /api/catalog/departments", catalogH.Departments)
	mux.HandleFunc("GET /api/catalog/doctors", catalogH.Doctors)
	mux.HandleFunc("GET /api/catalog/slots", catalogH.Slots)
	mux.HandleFunc("GET /api/me/profile", meH.Profile)
	mux.HandleFunc("GET /api/me/appointments", meH.Appointments)
	mux.HandleFunc("POST /api/me/appointments", meH.BookAppointment)
	mux.HandleFunc("DELETE /api/me/appointments/{motconsu_id}", meH.CancelAppointment)
	mux.HandleFunc("GET /api/me/lab-panels", meH.LabPanels)

	// Server-to-server (API_TOKEN), same as /api/reminders/due
	mux.HandleFunc("POST /api/internal/confirmations", confirmationsH.Upsert)

	// Staff dashboard (session cookie; not API_TOKEN)
	mux.HandleFunc("GET /dashboard/login", dashboardPageH.LoginForm)
	mux.HandleFunc("POST /dashboard/login", dashboardPageH.LoginPost)
	mux.HandleFunc("POST /dashboard/logout", dashboardPageH.Logout)

	staff := middleware.RequireStaff{Secret: []byte(cfg.Dashboard.Secret)}
	mux.Handle("GET /dashboard", staff.Wrap(http.HandlerFunc(dashboardPageH.Page)))
	mux.Handle("GET /dashboard/data", staff.Wrap(http.HandlerFunc(dashboardH.Schedule)))

	cidrNets, err := middleware.ParseCIDRList(cfg.Dashboard.AllowedCIDRs)
	if err != nil || len(cidrNets) == 0 {
		if err != nil {
			logger.Error("invalid DASHBOARD_ALLOWED_CIDRS, using defaults", "err", err)
		}
		cidrNets, _ = middleware.ParseCIDRList([]string{"192.168.0.0/16", "127.0.0.1/32"})
	}
	cidr := middleware.AllowCIDRs{Nets: cidrNets}

	auth := middleware.Auth{Token: cfg.APIToken}
	reqPatient := middleware.RequirePatient{JWTSecret: []byte(cfg.JWT.Secret)}
	var base http.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/dashboard") {
			cidr.Wrap(mux).ServeHTTP(w, r)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/auth/") || r.URL.Path == "/api/health" {
			mux.ServeHTTP(w, r)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/me/") || strings.HasPrefix(r.URL.Path, "/api/catalog/") {
			reqPatient.Wrap(mux).ServeHTTP(w, r)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/") {
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
