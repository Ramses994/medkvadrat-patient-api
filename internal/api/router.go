package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"net"
	"net/http"
	"strings"

	"github.com/medkvadrat/medkvadrat-patient-api/internal/config"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/consents"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/handler"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/middleware"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/ratelimit"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/repo"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/service"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/staff"
	"github.com/medkvadrat/medkvadrat-patient-api/web"
)

func NewRouter(cfg config.Config, svc *service.Services, logger *slog.Logger) http.Handler {
	mux := http.NewServeMux()

	healthH := handler.HealthHandler{Svc: svc}
	authH := handler.AuthHandler{Svc: svc, Logger: logger}
	doctorH := handler.DoctorHandler{Svc: svc, Logger: logger}
	patientH := handler.PatientHandler{Svc: svc, Logger: logger}
	wg := svc.WriteGuardDeps()
	scheduleH := handler.ScheduleHandler{Svc: svc, Logger: logger, WriteGuard: wg}
	catalogH := handler.CatalogHandler{Svc: svc, Logger: logger}
	bookingH := handler.BookingCatalogHandler{Svc: svc, Logger: logger, SlotTokenSecret: cfg.SlotTokenSecret}
	meH := handler.MeHandler{Svc: svc, Logger: logger, CancelMinHours: cfg.CancelMinHoursBefore, WaitlistExamIDs: cfg.WaitlistExamIDs, WriteGuard: wg}
	confirmationsH := handler.ConfirmationsHandler{Svc: svc, Logger: logger}
	consentStore := consents.New(svc.SQLite)
	remindersH := handler.RemindersHandler{Svc: svc, Logger: logger, Consents: consentStore}
	notificationsH := handler.NotificationsHandler{Svc: svc, Logger: logger, Consents: consentStore}
	consentsH := handler.BotConsentsHandler{
		Store: consentStore,
		Notify: func(ctx context.Context, patientID int64) (*int, error) {
			return repo.MessengerNotifyByPatient(ctx, svc.MSSQL, patientID)
		},
		Logger: logger,
	}
	adsExportH := handler.AdsExportHandler{Store: consentStore, Names: handler.NamesFromMedialog(svc.MSSQL), Logger: logger}
	dashboardH := handler.DashboardHandler{Svc: svc, Logger: logger}
	waitlistH := handler.WaitlistHandler{Svc: svc, Logger: logger}
	users := staff.New(svc.SQLite)
	dashboardPageH := handler.DashboardPageHandler{
		HTML:         web.DashboardHTML,
		Password:     cfg.Dashboard.Password,
		Secret:       []byte(cfg.Dashboard.Secret),
		SessionTTL:   cfg.Dashboard.SessionTTL,
		CookieSecure: cfg.Dashboard.CookieSecure,
		RateLimit:    ratelimit.NewStore(svc.SQLite),
		Logger:       logger,
		Users:        users,
	}
	staffH := handler.StaffHandler{Store: users, Logger: logger}

	// Public
	mux.HandleFunc("GET /api/health", healthH.Health)
	mux.HandleFunc("POST /api/auth/otp/request", authH.OTPRequest)
	mux.HandleFunc("POST /api/auth/otp/verify", authH.OTPVerify)
	mux.HandleFunc("POST /api/auth/otp/select-patient", authH.OTPSelectPatient)
	mux.HandleFunc("POST /api/auth/refresh", authH.Refresh)
	mux.HandleFunc("POST /api/auth/logout", authH.Logout)

	// Protected (step 1: protect all existing /api/* endpoints except /api/health)
	mux.HandleFunc("/api/schedule/changes", scheduleH.Changes)
	mux.HandleFunc("/api/doctors", doctorH.List)
	mux.HandleFunc("/api/patients/search", patientH.Search)
	mux.HandleFunc("/api/patients/lab-results", patientH.LabResults)
	mux.HandleFunc("/api/patients/lab-panels", patientH.LabPanels)
	mux.HandleFunc("GET /api/reminders/due", remindersH.Due)
	mux.HandleFunc("POST /api/notifications/new-bookings", notificationsH.NewBookings)
	mux.HandleFunc("GET /api/notifications/booking/{planning_id}", notificationsH.BookingByID)
	mux.HandleFunc("GET /api/dashboard/schedule", dashboardH.Schedule)

	// Patient JWT-protected (step 3)
	mux.HandleFunc("GET /api/catalog/specialties", catalogH.Specialties)
	mux.HandleFunc("GET /api/catalog/departments", catalogH.Departments)
	mux.HandleFunc("GET /api/catalog/doctors", catalogH.Doctors)
	mux.HandleFunc("GET /api/catalog/booking-options", bookingH.Options)
	mux.HandleFunc("GET /api/catalog/slots", bookingH.Slots)
	mux.HandleFunc("GET /api/me/profile", meH.Profile)
	mux.HandleFunc("GET /api/me/appointments", meH.Appointments)
	mux.HandleFunc("GET /api/me/visits", meH.Visits)
	bookH := handler.BookingHandler{API: svc, Actor: tokenLabel(cfg.APIToken), LAN: middleware.AllowCIDRs{Nets: cidrNetsOrDefault(cfg)}}
	mux.HandleFunc("POST /api/me/appointments", bookH.Book)
	mux.HandleFunc("DELETE /api/me/appointments/{planning_id}", bookH.Cancel)
	mux.HandleFunc("GET /api/internal/write-ops", bookH.List)
	mux.HandleFunc("POST /api/internal/write-ops/{op_id}/resolve", bookH.Resolve)
	mux.HandleFunc("GET /api/me/lab-panels", meH.LabPanels)

	// Server-to-server (API_TOKEN), same as /api/reminders/due.
	mux.HandleFunc("POST /api/internal/confirmations", confirmationsH.Upsert)
	mux.HandleFunc("GET /api/internal/bot-consents", consentsH.Get)
	mux.HandleFunc("POST /api/internal/bot-consents", consentsH.Post)

	// Staff dashboard (session cookie; not API_TOKEN)
	mux.HandleFunc("GET /dashboard/login", dashboardPageH.LoginForm)
	mux.Handle("POST /dashboard/login", middleware.RequireSameOrigin(http.HandlerFunc(dashboardPageH.LoginPost)))
	staffMW := middleware.RequireStaff{Secret: []byte(cfg.Dashboard.Secret), Lookup: users.ByID}
	mux.Handle("POST /dashboard/logout", staffMW.Wrap(middleware.RequireSameOrigin(http.HandlerFunc(dashboardPageH.Logout))))
	mux.Handle("GET /dashboard", staffMW.Wrap(http.HandlerFunc(dashboardPageH.Page)))
	mux.Handle("GET /dashboard/password", staffMW.Wrap(http.HandlerFunc(dashboardPageH.PasswordForm)))
	mux.Handle("POST /dashboard/password", staffMW.Wrap(middleware.RequireSameOrigin(http.HandlerFunc(dashboardPageH.PasswordPost))))
	mux.Handle("GET /dashboard/me", staffMW.Wrap(http.HandlerFunc(staffH.Me)))
	mux.Handle("GET /dashboard/data", staffMW.Wrap(http.HandlerFunc(dashboardH.Schedule)))
	mux.Handle("GET /dashboard/waitlist", staffMW.Wrap(http.HandlerFunc(waitlistH.List)))
	mux.Handle("PATCH /dashboard/waitlist/{source}/{id}", staffMW.Wrap(middleware.RequireDashboardMutation(http.HandlerFunc(waitlistH.Patch))))
	mux.Handle("GET /dashboard/staff", staffMW.Wrap(http.HandlerFunc(staffH.List)))
	mux.Handle("GET /dashboard/consents/ads.csv", staffMW.Wrap(http.HandlerFunc(adsExportH.AdsCSV)))
	mux.Handle("POST /dashboard/staff", staffMW.Wrap(middleware.RequireDashboardMutation(http.HandlerFunc(staffH.Create))))
	mux.Handle("POST /dashboard/staff/{id}", staffMW.Wrap(middleware.RequireDashboardMutation(http.HandlerFunc(staffH.Update))))

	cidrNets, err := middleware.ParseCIDRList(cfg.Dashboard.AllowedCIDRs)
	if err != nil || len(cidrNets) == 0 {
		if err != nil {
			logger.Error("invalid DASHBOARD_ALLOWED_CIDRS, using defaults", "err", err)
		}
		cidrNets, _ = middleware.ParseCIDRList([]string{"192.168.0.0/16", "127.0.0.1/32"})
	}
	cidr := middleware.AllowCIDRs{Nets: cidrNets}
	dashReady := middleware.RequireDashboardConfig{
		Password: cfg.Dashboard.Password,
		Secret:   []byte(cfg.Dashboard.Secret),
	}

	auth := middleware.Auth{Token: cfg.APIToken}
	reqPatient := middleware.RequirePatient{JWTSecret: []byte(cfg.JWT.Secret), Mode: cfg.Auth.Mode}
	var base http.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/dashboard") {
			dashReady.Wrap(cidr.Wrap(mux)).ServeHTTP(w, r)
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
	h = middleware.CORS(cfg.CORSAllowedOrigins, h)

	return h
}

func tokenLabel(token string) string {
	token = strings.TrimSpace(token)
	if token == "" {
		return "dev"
	}
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:4])
}

func cidrNetsOrDefault(cfg config.Config) []*net.IPNet {
	nets, err := middleware.ParseCIDRList(cfg.Dashboard.AllowedCIDRs)
	if err != nil || len(nets) == 0 {
		nets, _ = middleware.ParseCIDRList([]string{"192.168.0.0/16", "127.0.0.1/32"})
	}
	return nets
}
