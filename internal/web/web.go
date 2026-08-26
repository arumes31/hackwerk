// Package web defines HackWerk's HTTP boundary and baseline middleware.
package web

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"
	"strconv"
	"time"

	"example.invalid/hackplan/internal/appointment"
	"example.invalid/hackplan/internal/auth"
	"example.invalid/hackplan/internal/buildinfo"
	"example.invalid/hackplan/internal/calendarfeed"
	"example.invalid/hackplan/internal/config"
	"example.invalid/hackplan/internal/customers"
	"example.invalid/hackplan/internal/dashboard"
	"example.invalid/hackplan/internal/driver"
	"example.invalid/hackplan/internal/maptile"
	"example.invalid/hackplan/internal/notification"
	"example.invalid/hackplan/internal/observability"
	"example.invalid/hackplan/internal/planning"
	"example.invalid/hackplan/internal/resource"
	"example.invalid/hackplan/internal/voice"
	"example.invalid/hackplan/web/assets"
	"example.invalid/hackplan/web/templates"
	"github.com/a-h/templ"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

const contentSecurityPolicy = "default-src 'self'; base-uri 'self'; form-action 'self'; frame-ancestors 'none'; object-src 'none'; script-src 'self'; worker-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'"

// DatabasePinger is the readiness boundary consumed by the HTTP layer.
type DatabasePinger interface {
	Ping(context.Context) error
}

type OperationsHealth interface {
	Ready(context.Context, int64) error
	WorkerHealthy(context.Context, time.Duration) (time.Time, bool, error)
}

// Dependencies contains explicit HTTP dependencies.
type Dependencies struct {
	Config        config.Config
	Logger        *slog.Logger
	Database      DatabasePinger
	Build         buildinfo.Info
	Identity      *auth.Service
	Customers     *customers.Service
	Drivers       *driver.Service
	Resources     *resource.Service
	Appointments  *appointment.Service
	Confirmations *notification.ConfirmationService
	Notifications *notification.AdminService
	Dashboard     *dashboard.Service
	CalendarFeeds *calendarfeed.Service
	Planning      *planning.Service
	Routes        *planning.RouteService
	Voice         *voice.Service
	Metrics       *observability.Registry
	MapTiles      *maptile.Client
}

// NewRouter builds the complete Task-00 router without starting a listener.
func NewRouter(dependencies Dependencies) (http.Handler, error) {
	assetPaths, err := assets.LoadPaths()
	if err != nil {
		return nil, err
	}
	publicAssets, err := assets.PublicFS()
	if err != nil {
		return nil, err
	}
	assetVersions, err := assets.LoadVersions()
	if err != nil {
		return nil, err
	}

	pageData := templates.PageData{
		AppName:                     dependencies.Config.AppName,
		Version:                     dependencies.Build.Version,
		CSSPath:                     assetPaths.CSS,
		ControlFoundationCSSPath:    assetPaths.ControlFoundationCSS,
		JSPath:                      assetPaths.JavaScript,
		ManifestPath:                assetPaths.Manifest,
		IconPath:                    assetPaths.Icon,
		LoginOriginalCSSPath:        assetPaths.LoginOriginalCSS,
		LoginCSSPath:                assetPaths.LoginCSS,
		LoginLoaderJSPath:           assetPaths.LoginLoaderJavaScript,
		LoginBackgroundJSPath:       assetPaths.LoginBackgroundJavaScript,
		MapLibreJSPath:              assetPaths.MapLibreJavaScript,
		MapLibreWorkerPath:          assetPaths.MapLibreWorker,
		MapLibreCSSPath:             assetPaths.MapLibreCSS,
		MapAttribution:              dependencies.Config.Map.Attribution,
		FullCalendarThemeJSPath:     assetPaths.FullCalendarThemeJavaScript,
		FullCalendarSkeletonCSSPath: assetPaths.FullCalendarSkeletonCSS,
		FullCalendarThemeCSSPath:    assetPaths.FullCalendarThemeCSS,
		FullCalendarPaletteCSSPath:  assetPaths.FullCalendarPaletteCSS,
		FullCalendarJSPath:          assetPaths.FullCalendarJavaScript,
	}

	router := chi.NewRouter()
	boundary, err := newNetworkBoundary(dependencies.Config)
	if err != nil {
		return nil, fmt.Errorf("web: network boundary: %w", err)
	}
	router.Use(middleware.RequestID)
	router.Use(boundary.Middleware)
	router.Use(securityHeaders)
	router.Use(requestLimits(dependencies.Config.HTTP.MaxBodyBytes, dependencies.Config.HTTP.InternalRateLimit))
	router.Use(recoverer(dependencies.Logger))
	router.Use(requestLogger(dependencies.Logger, dependencies.Metrics))
	router.Use(maintenanceMode(dependencies.Config.MaintenanceMode))

	router.Handle("/assets/*", staticAssetHandler(publicAssets, assetVersions))
	router.Get("/health/live", liveHandler(dependencies.Build))
	router.Get("/health/ready", readyHandler(dependencies.Database, dependencies.Config.Database.ReadinessTimeout, dependencies.Config.Database.ExpectedSchema))
	router.Get("/health/worker", workerHealthHandler(dependencies.Database, dependencies.Config.Database.ReadinessTimeout, dependencies.Config.Metrics.WorkerStaleAfter))
	if dependencies.Identity == nil {
		router.Get("/", componentHandler(templates.Home(pageData), dependencies.Logger))
	} else {
		router.Group(func(identityRouter chi.Router) {
			registerIdentityRoutes(identityRouter, dependencies, pageData)
		})
	}
	router.NotFound(componentStatusHandler(
		templates.Error(pageData, http.StatusNotFound, "Seite nicht gefunden", "Die aufgerufene Seite existiert nicht oder wurde verschoben."),
		http.StatusNotFound,
		dependencies.Logger,
	))
	return router, nil
}

func componentHandler(component templ.Component, logger *slog.Logger) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		if err := component.Render(request.Context(), response); err != nil {
			logger.ErrorContext(request.Context(), "rendering page", slog.Any("error", err))
		}
	}
}

func componentStatusHandler(component templ.Component, status int, logger *slog.Logger) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		response.WriteHeader(status)
		if err := component.Render(request.Context(), response); err != nil {
			logger.ErrorContext(request.Context(), "rendering error page", slog.Any("error", err))
		}
	}
}

func liveHandler(build buildinfo.Info) http.HandlerFunc {
	return func(response http.ResponseWriter, _ *http.Request) {
		writeJSON(response, http.StatusOK, map[string]string{
			"status":  "live",
			"version": build.Version,
		})
	}
}

func readyHandler(database DatabasePinger, timeout time.Duration, expectedSchema int64) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		if database == nil {
			writeJSON(response, http.StatusServiceUnavailable, map[string]string{"status": "not_ready"})
			return
		}
		ctx, cancel := context.WithTimeout(request.Context(), timeout)
		defer cancel()
		var err error
		if health, ok := database.(OperationsHealth); ok {
			err = health.Ready(ctx, expectedSchema)
		} else {
			err = database.Ping(ctx)
		}
		if err != nil {
			writeJSON(response, http.StatusServiceUnavailable, map[string]string{"status": "not_ready"})
			return
		}
		writeJSON(response, http.StatusOK, map[string]string{"status": "ready"})
	}
}

func workerHealthHandler(database DatabasePinger, timeout, staleAfter time.Duration) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		health, ok := database.(OperationsHealth)
		if !ok {
			writeJSON(response, http.StatusServiceUnavailable, map[string]string{"status": "not_ready"})
			return
		}
		ctx, cancel := context.WithTimeout(request.Context(), timeout)
		defer cancel()
		_, healthy, err := health.WorkerHealthy(ctx, staleAfter)
		if err != nil || !healthy {
			writeJSON(response, http.StatusServiceUnavailable, map[string]string{"status": "not_ready"})
			return
		}
		writeJSON(response, http.StatusOK, map[string]string{"status": "ready"})
	}
}

func writeJSON(response http.ResponseWriter, status int, value any) {
	var payload bytes.Buffer
	if err := json.NewEncoder(&payload).Encode(value); err != nil {
		http.Error(response, "Interner Fehler. Bitte später erneut versuchen.", http.StatusInternalServerError)
		return
	}
	response.Header().Set("Content-Type", "application/json; charset=utf-8")
	response.WriteHeader(status)
	if _, err := response.Write(payload.Bytes()); err != nil {
		return
	}
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Security-Policy", contentSecurityPolicy)
		response.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		response.Header().Set("X-Content-Type-Options", "nosniff")
		response.Header().Set("X-Frame-Options", "DENY")
		response.Header().Set("Permissions-Policy", "camera=(), geolocation=(self), microphone=(self)")
		response.Header().Set("Cross-Origin-Opener-Policy", "same-origin")
		if requestIsSecure(request) {
			response.Header().Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		}
		next.ServeHTTP(response, request)
	})
}

func recoverer(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
			defer func() {
				if recovered := recover(); recovered != nil {
					logger.ErrorContext(
						request.Context(),
						"http panic recovered",
						slog.String("request_id", middleware.GetReqID(request.Context())),
						slog.String("error_type", fmt.Sprintf("%T", recovered)),
						slog.String("stack", string(debug.Stack())),
					)
					reference := middleware.GetReqID(request.Context())
					http.Error(response, "Interner Fehler. Fehlerreferenz: "+reference, http.StatusInternalServerError)
				}
			}()
			next.ServeHTTP(response, request)
		})
	}
}

func requestLogger(logger *slog.Logger, metrics *observability.Registry) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
			startedAt := time.Now()
			wrapped := middleware.NewWrapResponseWriter(response, request.ProtoMajor)
			next.ServeHTTP(wrapped, request)

			route := "unmatched"
			if routeContext := chi.RouteContext(request.Context()); routeContext != nil && routeContext.RoutePattern() != "" {
				route = routeContext.RoutePattern()
			}
			if metrics != nil {
				metrics.ObserveHTTP(route, request.Method, wrapped.Status(), time.Since(startedAt))
			}
			logger.InfoContext(
				request.Context(),
				"http request",
				slog.String("request_id", middleware.GetReqID(request.Context())),
				slog.String("method", request.Method),
				slog.String("route", route),
				slog.Int("status", wrapped.Status()),
				slog.Duration("duration", time.Since(startedAt)),
			)
		})
	}
}

// Server returns a configured HTTP server with bounded timeouts.
func Server(cfg config.Config, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           handler,
		ReadHeaderTimeout: cfg.HTTP.ReadHeaderTimeout,
		ReadTimeout:       cfg.HTTP.ReadTimeout,
		WriteTimeout:      cfg.HTTP.WriteTimeout,
		IdleTimeout:       cfg.HTTP.IdleTimeout,
		MaxHeaderBytes:    cfg.HTTP.MaxHeaderBytes,
		ErrorLog:          slog.NewLogLogger(slog.Default().Handler(), slog.LevelError),
	}
}

// MetricsServer returns the separately bound internal-only metrics listener.
func MetricsServer(cfg config.Config, handler http.Handler) *http.Server {
	return &http.Server{
		Addr: cfg.Metrics.ListenAddr, Handler: handler,
		ReadHeaderTimeout: 3 * time.Second, ReadTimeout: 5 * time.Second,
		WriteTimeout: 10 * time.Second, IdleTimeout: 30 * time.Second,
		MaxHeaderBytes: 64 << 10, ErrorLog: slog.NewLogLogger(slog.Default().Handler(), slog.LevelError),
	}
}

// Healthcheck requests a health endpoint and accepts only a 200 response.
func Healthcheck(ctx context.Context, baseURL string, timeout time.Duration) (checkErr error) {
	client := &http.Client{Timeout: timeout}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/health/ready", nil)
	if err != nil {
		return fmt.Errorf("healthcheck: creating request: %w", err)
	}
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("healthcheck: requesting readiness: %w", err)
	}
	defer func() {
		checkErr = errors.Join(checkErr, response.Body.Close())
	}()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("healthcheck: readiness returned status %s", strconv.Itoa(response.StatusCode))
	}
	return nil
}
