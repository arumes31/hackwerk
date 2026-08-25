// Package app wires HackWerk's process modes from explicit dependencies.
package app

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"example.invalid/hackplan/internal/adapters/postgres"
	"example.invalid/hackplan/internal/buildinfo"
	"example.invalid/hackplan/internal/config"
	"example.invalid/hackplan/internal/web"
)

// Serve runs the HTTP process until cancellation or listener failure.
func Serve(ctx context.Context, cfg config.Config, logger *slog.Logger) error {
	pool, err := postgres.Open(ctx, cfg.Database)
	if err != nil {
		return err
	}
	defer pool.Close()
	identity, err := IdentityService(cfg, pool)
	if err != nil {
		return err
	}
	customerService, err := CustomerService(pool)
	if err != nil {
		return err
	}
	driverService, err := DriverService(pool)
	if err != nil {
		return err
	}
	resourceService, err := ResourceService(pool)
	if err != nil {
		return err
	}
	appointmentService, err := AppointmentService(pool, driverService)
	if err != nil {
		return err
	}

	router, err := web.NewRouter(web.Dependencies{
		Config:       cfg,
		Logger:       logger,
		Database:     pool,
		Build:        buildinfo.Current(),
		Identity:     identity,
		Customers:    customerService,
		Drivers:      driverService,
		Resources:    resourceService,
		Appointments: appointmentService,
	})
	if err != nil {
		return err
	}
	server := web.Server(cfg, router)

	serverErrors := make(chan error, 1)
	go func() {
		logger.InfoContext(ctx, "http server starting", slog.String("address", cfg.ListenAddr))
		serverErrors <- server.ListenAndServe()
	}()

	select {
	case serverErr := <-serverErrors:
		if errors.Is(serverErr, http.ErrServerClosed) {
			return nil
		}
		return serverErr
	case <-ctx.Done():
		shutdownContext, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
		defer cancel()
		logger.Info("http server stopping")
		if err := server.Shutdown(shutdownContext); err != nil {
			return err
		}
		return nil
	}
}

// Worker runs the Task-00 database heartbeat and waits for cancellation.
// Later tasks attach bounded outbox and cleanup processors to this lifecycle.
func Worker(ctx context.Context, cfg config.Config, logger *slog.Logger) error {
	pool, err := postgres.Open(ctx, cfg.Database)
	if err != nil {
		return err
	}
	defer pool.Close()

	logger.InfoContext(ctx, "worker started")
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			logger.Info("worker stopped")
			return nil
		case <-ticker.C:
			if pingErr := postgres.Ping(ctx, pool, cfg.Database.ReadinessTimeout); pingErr != nil {
				logger.WarnContext(ctx, "worker database unavailable", slog.String("error_code", "database_unavailable"))
				continue
			}
			logger.DebugContext(ctx, "worker heartbeat")
		}
	}
}
