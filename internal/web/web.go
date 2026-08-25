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
	"example.invalid/hackplan/internal/config"
	"example.invalid/hackplan/internal/customers"
	"example.invalid/hackplan/internal/driver"
	"example.invalid/hackplan/internal/resource"
	"example.invalid/hackplan/web/assets"
	"example.invalid/hackplan/web/templates"
	"github.com/a-h/templ"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

const contentSecurityPolicy = "default-src 'self'; base-uri 'self'; form-action 'self'; frame-ancestors 'none'; object-src 'none'; script-src 'self'; style-src 'self'; style-src-attr 'unsafe-inline'; img-src 'self' data:"

// DatabasePinger is the readiness boundary consumed by the HTTP layer.
type DatabasePinger interface {
	Ping(context.Context) error
}

// Dependencies contains explicit HTTP dependencies.
type Dependencies struct {
	Config       config.Config
	Logger       *slog.Logger
	Database     DatabasePinger
	Build        buildinfo.Info
	Identity     *auth.Service
	Customers    *customers.Service
	Drivers      *driver.Service
	Resources    *resource.Service
	Appointments *appointment.Service
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

	pageData := templates.PageData{
		AppName:                     dependencies.Config.AppName,
		Version:                     dependencies.Build.Version,
		CSSPath:                     assetPaths.CSS,
		JSPath:                      assetPaths.JavaScript,
		FullCalendarThemeJSPath:     assetPaths.FullCalendarThemeJavaScript,
		FullCalendarSkeletonCSSPath: assetPaths.FullCalendarSkeletonCSS,
		FullCalendarThemeCSSPath:    assetPaths.FullCalendarThemeCSS,
		FullCalendarPaletteCSSPath:  assetPaths.FullCalendarPaletteCSS,
		FullCalendarJSPath:          assetPaths.FullCalendarJavaScript,
	}

	router := chi.NewRouter()
	router.Use(middleware.RequestID)
	router.Use(securityHeaders)
	router.Use(recoverer(dependencies.Logger))
	router.Use(requestLogger(dependencies.Logger))

	router.Handle("/assets/*", http.StripPrefix("/assets/", http.FileServer(http.FS(publicAssets))))
	router.Get("/health/live", liveHandler(dependencies.Build))
	router.Get("/health/ready", readyHandler(dependencies.Database, dependencies.Config.Database.ReadinessTimeout))
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

func readyHandler(database DatabasePinger, timeout time.Duration) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		if database == nil {
			writeJSON(response, http.StatusServiceUnavailable, map[string]string{"status": "not_ready"})
			return
		}
		ctx, cancel := context.WithTimeout(request.Context(), timeout)
		defer cancel()
		if err := database.Ping(ctx); err != nil {
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
		response.Header().Set("Permissions-Policy", "camera=(), geolocation=(), microphone=(self)")
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
					http.Error(response, "Interner Fehler. Bitte später erneut versuchen.", http.StatusInternalServerError)
				}
			}()
			next.ServeHTTP(response, request)
		})
	}
}

func requestLogger(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
			startedAt := time.Now()
			wrapped := middleware.NewWrapResponseWriter(response, request.ProtoMajor)
			next.ServeHTTP(wrapped, request)

			route := "unmatched"
			if routeContext := chi.RouteContext(request.Context()); routeContext != nil && routeContext.RoutePattern() != "" {
				route = routeContext.RoutePattern()
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
