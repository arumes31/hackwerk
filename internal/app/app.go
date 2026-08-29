// Package app wires HackWerk's process modes from explicit dependencies.
package app

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"example.invalid/hackplan/internal/adapters/postgres"
	"example.invalid/hackplan/internal/buildinfo"
	"example.invalid/hackplan/internal/calendarfeed"
	"example.invalid/hackplan/internal/config"
	"example.invalid/hackplan/internal/geocode"
	"example.invalid/hackplan/internal/maptile"
	"example.invalid/hackplan/internal/notification"
	"example.invalid/hackplan/internal/observability"
	"example.invalid/hackplan/internal/routelocation"
	"example.invalid/hackplan/internal/voice"
	"example.invalid/hackplan/internal/web"
)

// Serve runs the HTTP process until cancellation or listener failure.
func Serve(ctx context.Context, cfg config.Config, logger *slog.Logger) error {
	pool, err := postgres.Open(ctx, cfg.Database)
	if err != nil {
		return err
	}
	defer pool.Close()
	operations := postgres.NewOperationsStore(pool, cfg.Metrics.WorkerStaleAfter)
	if err := operations.Ready(ctx, cfg.Database.ExpectedSchema); err != nil {
		return err
	}
	build := buildinfo.Current()
	metrics := observability.New(operations, cfg.Metrics.CollectionTimeout, build.Version, build.Commit, map[string]bool{
		"email": cfg.Mail.Enabled, "sms": cfg.SMS.Enabled, "voice": cfg.Voice.Enabled,
		"routing_external": cfg.Planning.Router == "osrm", "geocoding": cfg.Geocoding.Enabled, "ics": cfg.CalendarFeed.Enabled,
	})
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
	routeLocationStore := postgres.NewRouteLocationStore(pool)
	routeLocationService, err := routelocation.New(routeLocationStore)
	if err != nil {
		return err
	}
	appointmentService, err := AppointmentService(cfg, pool, driverService)
	if err != nil {
		return err
	}
	tokens, err := notification.NewKeyRing(cfg.Confirmation.TokenKeys, cfg.Confirmation.CurrentKeyID)
	if err != nil {
		return err
	}
	notificationStore := postgres.NewNotificationStore(pool, postgres.WithNotificationPlanning(
		tokens, cfg.Confirmation.TokenTTL, cfg.Mail.MaxAttempts, cfg.SMS.MaxAttempts, cfg.Mail.Enabled, cfg.SMS.Enabled,
	))
	confirmationService, err := notification.NewConfirmationService(notificationStore, tokens, time.Now)
	if err != nil {
		return err
	}
	notificationAdmin, err := notification.NewAdminService(notificationStore, time.Now)
	if err != nil {
		return err
	}
	dashboardService, err := DashboardService(cfg, pool)
	if err != nil {
		return err
	}
	var calendarFeedService *calendarfeed.Service
	if cfg.CalendarFeed.Enabled {
		calendarFeedService, err = CalendarFeedService(cfg, pool)
		if err != nil {
			return err
		}
	}
	planningService, err := PlanningService(cfg, pool, driverService, routeLocationStore, metrics)
	if err != nil {
		return err
	}
	routeService, err := RoutePlanningService(cfg, pool)
	if err != nil {
		return err
	}
	voiceService, err := VoiceService(cfg, pool, metrics)
	if err != nil {
		return err
	}
	mapTiles, err := maptile.New(maptile.Config{
		URLTemplate: cfg.Map.TileURL, Token: cfg.Map.TileToken, Timeout: cfg.Map.Timeout,
		MaxResponseBytes: cfg.Map.MaxResponseBytes, MaxZoom: cfg.Map.MaxZoom,
		UserAgent: "HackWerk/" + build.Version + " (map tile proxy)",
	})
	if err != nil {
		return err
	}
	var geocoder geocode.Searcher
	if cfg.Geocoding.Enabled {
		geocoder, err = geocode.New(geocode.Config{
			SearchURL: cfg.Geocoding.SearchURL, CountryCodes: cfg.Geocoding.CountryCodes, Timeout: cfg.Geocoding.Timeout,
			MaxResponseSize: cfg.Geocoding.MaxResponseBytes, MaxResults: cfg.Geocoding.MaxResults, MinInterval: cfg.Geocoding.MinInterval,
			CacheTTL: cfg.Geocoding.CacheTTL, CacheEntries: cfg.Geocoding.CacheEntries,
			UserAgent: "HackWerk/" + build.Version + " (address search)",
		})
		if err != nil {
			return err
		}
	}

	router, err := web.NewRouter(web.Dependencies{
		Config:         cfg,
		Logger:         logger,
		Database:       operations,
		Build:          build,
		Identity:       identity,
		Customers:      customerService,
		Drivers:        driverService,
		Resources:      resourceService,
		RouteLocations: routeLocationService,
		Appointments:   appointmentService,
		Confirmations:  confirmationService,
		Notifications:  notificationAdmin,
		Dashboard:      dashboardService,
		CalendarFeeds:  calendarFeedService,
		Planning:       planningService,
		Routes:         routeService,
		Voice:          voiceService,
		Metrics:        metrics,
		MapTiles:       mapTiles,
		Geocoder:       geocoder,
	})
	if err != nil {
		return err
	}
	server := web.Server(cfg, router)

	serverErrors := make(chan error, 2)
	go func() {
		logger.InfoContext(ctx, "http server starting", slog.String("address", cfg.ListenAddr))
		serverErrors <- server.ListenAndServe()
	}()
	var metricsServer *http.Server
	if cfg.Metrics.Enabled {
		metricsServer = web.MetricsServer(cfg, metrics.Handler())
		go func() {
			logger.InfoContext(ctx, "metrics server starting", slog.String("address", cfg.Metrics.ListenAddr))
			serverErrors <- metricsServer.ListenAndServe()
		}()
	}

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
		shutdownErr := server.Shutdown(shutdownContext)
		if metricsServer != nil {
			shutdownErr = errors.Join(shutdownErr, metricsServer.Shutdown(shutdownContext))
		}
		return shutdownErr
	}
}

// Worker delivers bounded batches from the transactional notification outbox.
func Worker(ctx context.Context, cfg config.Config, logger *slog.Logger) error {
	pool, err := postgres.Open(ctx, cfg.Database)
	if err != nil {
		return err
	}
	defer pool.Close()
	operations := postgres.NewOperationsStore(pool, cfg.Metrics.WorkerStaleAfter)
	if err := operations.Ready(ctx, cfg.Database.ExpectedSchema); err != nil {
		return err
	}
	workerID, err := workerIdentity(cfg.Worker.InstanceID, os.Hostname)
	if err != nil {
		return err
	}
	startedAt := time.Now().UTC()
	if err := operations.Heartbeat(ctx, workerID, startedAt, startedAt, "running"); err != nil {
		return err
	}
	defer func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = operations.Heartbeat(stopCtx, workerID, startedAt, time.Now().UTC(), "stopping")
	}()

	providers := make(map[notification.Channel]notification.Provider, 2)
	if cfg.Mail.Enabled {
		provider, providerErr := notification.NewSMTPProvider(notification.SMTPConfig{
			Host: cfg.Mail.Host, Port: cfg.Mail.Port, TLSMode: cfg.Mail.TLSMode,
			Username: cfg.Mail.Username, Password: cfg.Mail.Password,
			FromAddress: cfg.Mail.FromAddress, FromName: cfg.Mail.FromName, ReplyTo: cfg.Mail.ReplyTo,
			ConnectTimeout: cfg.Mail.ConnectTimeout, CommandTimeout: cfg.Mail.CommandTimeout,
		})
		if providerErr != nil {
			return providerErr
		}
		providers[notification.ChannelEmail] = provider
	}
	if cfg.SMS.Enabled {
		var provider notification.Provider
		var providerErr error
		switch cfg.SMS.Provider {
		case "sendberry":
			provider, providerErr = notification.NewSendberryProvider(notification.SendberryConfig{
				URL: cfg.SMS.SendberryURL, APIKey: cfg.SMS.SendberryKey, AccessName: cfg.SMS.SendberryName,
				AccessPassword: cfg.SMS.SendberryPassword, Sender: cfg.SMS.Sender, Timeout: cfg.SMS.Timeout,
			})
		case "webhook":
			provider, providerErr = notification.NewSMSWebhookProvider(notification.SMSWebhookConfig{
				URL: cfg.SMS.WebhookURL, Secret: cfg.SMS.HMACSecret, Sender: cfg.SMS.Sender, Timeout: cfg.SMS.Timeout,
			}, time.Now)
		default:
			providerErr = errors.New("app: unsupported SMS provider")
		}
		if providerErr != nil {
			return providerErr
		}
		providers[notification.ChannelSMS] = provider
	}
	tokens, err := notification.NewKeyRing(cfg.Confirmation.TokenKeys, cfg.Confirmation.CurrentKeyID)
	if err != nil {
		return err
	}
	location, err := time.LoadLocation(cfg.Timezone)
	if err != nil {
		return err
	}
	// #nosec G115 -- config validation bounds the worker batch size to 1..500.
	batchSize := int32(cfg.Worker.BatchSize)
	processor, err := notification.NewProcessor(
		postgres.NewNotificationWorkerStore(pool), providers, tokens, location,
		notification.ProcessorConfig{
			BaseURL: cfg.BaseURL, BusinessName: cfg.Business.Name, BusinessAddress: cfg.Business.Address,
			BusinessPhone: cfg.Business.Phone, Lease: cfg.Worker.Lease, BatchSize: batchSize,
		}, time.Now, logger,
	)
	if err != nil {
		return err
	}
	voiceService, err := VoiceService(cfg, pool)
	if err != nil {
		return err
	}

	logger.InfoContext(ctx, "worker started", slog.Int("batch_size", cfg.Worker.BatchSize))
	ticker := time.NewTicker(cfg.Worker.PollInterval)
	defer ticker.Stop()
	nextVoiceCleanup := time.Time{}
	heartbeatInterval := min(cfg.Metrics.WorkerStaleAfter/3, 30*time.Second)
	if heartbeatInterval < 5*time.Second {
		heartbeatInterval = 5 * time.Second
	}
	heartbeatTicker := time.NewTicker(heartbeatInterval)
	heartbeatDone := make(chan struct{})
	voiceDone := make(chan struct{})
	go func() {
		defer close(heartbeatDone)
		runWorkerHeartbeat(ctx, operations, workerID, startedAt, heartbeatTicker.C, logger)
	}()
	go func() {
		defer close(voiceDone)
		runVoiceWorker(ctx, voiceService, workerID, cfg.Worker.PollInterval, cfg.Voice.ProviderTimeout+30*time.Second, logger)
	}()
	defer func() {
		heartbeatTicker.Stop()
		<-heartbeatDone
		<-voiceDone
	}()

	for {
		if _, processErr := processor.RunOnce(ctx); processErr != nil && ctx.Err() == nil {
			logger.WarnContext(ctx, "notification batch failed", slog.String("error_code", "notification_batch_failed"))
		}
		if now := time.Now(); !now.Before(nextVoiceCleanup) {
			if _, err := voiceService.Cleanup(ctx); err != nil && ctx.Err() == nil {
				logger.WarnContext(ctx, "voice data cleanup failed", slog.String("error_code", "voice_cleanup_failed"))
			}
			nextVoiceCleanup = now.Add(time.Minute)
		}
		select {
		case <-ctx.Done():
			logger.Info("worker stopped")
			return nil
		case <-ticker.C:
		}
	}
}

func runVoiceWorker(ctx context.Context, service *voice.Service, workerID string, pollInterval, lease time.Duration, logger *slog.Logger) {
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for {
		if _, err := service.ProcessNext(ctx, workerID, lease); err != nil && ctx.Err() == nil {
			logger.WarnContext(ctx, "voice recording processing failed", slog.String("error_code", "voice_processing_failed"))
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

type workerHeartbeatStore interface {
	Heartbeat(context.Context, string, time.Time, time.Time, string) error
}

func runWorkerHeartbeat(ctx context.Context, operations workerHeartbeatStore, workerID string, startedAt time.Time, ticks <-chan time.Time, logger *slog.Logger) {
	for {
		select {
		case <-ctx.Done():
			return
		case now, ok := <-ticks:
			if !ok {
				return
			}
			if err := operations.Heartbeat(ctx, workerID, startedAt, now.UTC(), "running"); err != nil && ctx.Err() == nil {
				logger.WarnContext(ctx, "worker heartbeat failed", slog.String("error_code", "worker_heartbeat_failed"))
			}
		}
	}
}

// WorkerHealthcheck verifies the worker's database/schema contract and the
// shared heartbeat without depending on the web container or reverse proxy.
func WorkerHealthcheck(ctx context.Context, cfg config.Config) error {
	checkCtx, cancel := context.WithTimeout(ctx, cfg.Database.ReadinessTimeout)
	defer cancel()
	databaseConfig := cfg.Database
	databaseConfig.MinConnections = 0
	databaseConfig.MaxConnections = 1
	pool, err := postgres.Open(checkCtx, databaseConfig)
	if err != nil {
		return err
	}
	defer pool.Close()
	operations := postgres.NewOperationsStore(pool, cfg.Metrics.WorkerStaleAfter)
	workerID, err := workerIdentity(cfg.Worker.InstanceID, os.Hostname)
	if err != nil {
		return err
	}
	return checkWorkerHealth(checkCtx, operations, cfg.Database.ExpectedSchema, workerID, cfg.Metrics.WorkerStaleAfter)
}

type workerHealthStore interface {
	Ready(context.Context, int64) error
	WorkerHealthyByID(context.Context, string, time.Duration) (time.Time, bool, error)
}

func checkWorkerHealth(ctx context.Context, operations workerHealthStore, expectedSchema int64, workerID string, staleAfter time.Duration) error {
	if err := operations.Ready(ctx, expectedSchema); err != nil {
		return err
	}
	_, healthy, err := operations.WorkerHealthyByID(ctx, workerID, staleAfter)
	if err != nil {
		return err
	}
	if !healthy {
		return errors.New("app: worker heartbeat is stale")
	}
	return nil
}

func workerIdentity(configured string, hostname func() (string, error)) (string, error) {
	value := strings.TrimSpace(configured)
	if value == "" {
		var err error
		value, err = hostname()
		value = strings.TrimSpace(value)
		if err != nil || value == "" {
			return "", errors.New("app: resolving worker identity")
		}
	}
	if len(value) > 128 || strings.ContainsAny(value, "\r\n\t") {
		return "", errors.New("app: invalid worker identity")
	}
	return value, nil
}
