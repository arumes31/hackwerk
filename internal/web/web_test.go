package web

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"example.invalid/hackplan/internal/buildinfo"
	"example.invalid/hackplan/internal/config"
)

type pinger struct{ err error }

func (p pinger) Ping(context.Context) error { return p.err }

func TestHealthEndpoints(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		path           string
		pinger         DatabasePinger
		expectedStatus int
		expectedBody   string
	}{
		{name: "live does not require database", path: "/health/live", pinger: pinger{err: errors.New("down")}, expectedStatus: http.StatusOK, expectedBody: "live"},
		{name: "ready database up", path: "/health/ready", pinger: pinger{}, expectedStatus: http.StatusOK, expectedBody: "ready"},
		{name: "ready database down", path: "/health/ready", pinger: pinger{err: errors.New("down")}, expectedStatus: http.StatusServiceUnavailable, expectedBody: "not_ready"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			router := testRouter(t, tt.pinger)
			request := httptest.NewRequestWithContext(context.Background(), http.MethodGet, tt.path, nil)
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			if response.Code != tt.expectedStatus {
				t.Fatalf("status = %d, want %d", response.Code, tt.expectedStatus)
			}
			if !strings.Contains(response.Body.String(), tt.expectedBody) {
				t.Fatalf("body = %q, want containing %q", response.Body.String(), tt.expectedBody)
			}
		})
	}
}

func TestHomeAndNotFound(t *testing.T) {
	t.Parallel()

	router := testRouter(t, pinger{})
	for path, expectedStatus := range map[string]int{"/": http.StatusOK, "/fehlt": http.StatusNotFound} {
		request := httptest.NewRequestWithContext(context.Background(), http.MethodGet, path, nil)
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		if response.Code != expectedStatus {
			t.Fatalf("%s status = %d, want %d", path, response.Code, expectedStatus)
		}
		if response.Header().Get("Content-Security-Policy") == "" {
			t.Fatalf("%s has no content security policy", path)
		}
	}
}

func TestEmbeddedAssets(t *testing.T) {
	t.Parallel()

	router := testRouter(t, pinger{})
	tests := []struct {
		path        string
		contentType string
		bodyPart    string
	}{
		{path: "/assets/app.css", contentType: "text/css", bodyPart: ":root"},
		{path: "/assets/app.js", contentType: "javascript", bodyPart: "document.documentElement"},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			request := httptest.NewRequestWithContext(context.Background(), http.MethodGet, tt.path, nil)
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			if response.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
			}
			if !strings.Contains(response.Header().Get("Content-Type"), tt.contentType) {
				t.Fatalf("Content-Type = %q, want containing %q", response.Header().Get("Content-Type"), tt.contentType)
			}
			if !strings.Contains(response.Body.String(), tt.bodyPart) {
				t.Fatalf("asset body does not contain %q", tt.bodyPart)
			}
		})
	}
}

func TestRequestLogContainsNoDatabaseURL(t *testing.T) {
	t.Parallel()

	var logs bytes.Buffer
	cfg := testConfig()
	cfg.Database.URL = "postgres://canary-secret@database/hackplan"
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	router, err := NewRouter(Dependencies{Config: cfg, Logger: logger, Database: pinger{}, Build: buildinfo.Current()})
	if err != nil {
		t.Fatalf("NewRouter() error = %v", err)
	}
	request := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
	router.ServeHTTP(httptest.NewRecorder(), request)
	if strings.Contains(logs.String(), "canary-secret") {
		t.Fatalf("log contains database secret: %s", logs.String())
	}
}

func testRouter(t *testing.T, database DatabasePinger) http.Handler {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))
	router, err := NewRouter(Dependencies{Config: testConfig(), Logger: logger, Database: database, Build: buildinfo.Current()})
	if err != nil {
		t.Fatalf("NewRouter() error = %v", err)
	}
	return router
}

func testConfig() config.Config {
	return config.Config{
		AppName: "HackWerk",
		HTTP: config.HTTP{
			ReadHeaderTimeout: time.Second,
			ReadTimeout:       time.Second,
			WriteTimeout:      time.Second,
			IdleTimeout:       time.Second,
			MaxHeaderBytes:    1 << 20,
		},
		Database: config.Database{ReadinessTimeout: time.Second},
	}
}
