package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/medkvadrat/medkvadrat-patient-api/internal/api"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/config"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/db"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/logging"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/poll"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/repo"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/service"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/store"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		// config.Load already prints a safe message via stderr when it can.
		os.Exit(1)
	}

	logger := logging.New(cfg.LogLevel)

	mssql, err := db.OpenMSSQL(cfg.MSSQL)
	if err != nil {
		logger.Error("mssql connect failed", "err", err)
		os.Exit(1)
	}

	sqlite, err := db.OpenSQLite(cfg.SQLite.Path)
	if err != nil {
		logger.Error("sqlite connect failed", "err", err)
		os.Exit(1)
	}

	if err := store.Migrate(ctxBackground(), sqlite); err != nil {
		logger.Error("sqlite migrate failed", "err", err)
		os.Exit(1)
	}

	repos := repo.New(mssql)
	defaultModelsID := service.ParseDefaultModelsID(os.Getenv("DEFAULT_MODELS_ID"))
	services := service.New(mssql, sqlite, repos, logger, defaultModelsID, cfg)
	router := api.NewRouter(cfg, services, logger)

	server := &http.Server{
		Addr:              cfg.HTTP.ListenAddr,
		Handler:           router,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Start polling (best-effort, logs only for now).
	poller := poll.NewMotconsuPoller(services, logger, cfg.PollInterval)
	if err := poller.Start(ctx); err != nil {
		logger.Error("poller start failed", "err", err)
		os.Exit(1)
	}

	go func() {
		logger.Info("http server starting", "addr", cfg.HTTP.ListenAddr)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("http server failed", "err", err)
			stop()
		}
	}()

	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	logger.Info("http server shutting down")
	_ = server.Shutdown(shutdownCtx)
	logger.Info("poller stopping")
	poller.Stop()
	logger.Info("db closing")
	_ = mssql.Close()
	_ = sqlite.Close()
}

func ctxBackground() context.Context { return context.Background() }
