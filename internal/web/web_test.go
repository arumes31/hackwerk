package web

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"example.invalid/hackplan/internal/buildinfo"
	"example.invalid/hackplan/internal/config"
	"example.invalid/hackplan/web/assets"
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

func TestHealthcheckUsesLocalListenerAndPublicHostHeader(t *testing.T) {
	t.Parallel()
	hosts := make(chan string, 1)
	router := testRouter(t, pinger{})
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		hosts <- request.Host
		router.ServeHTTP(response, request)
	}))
	defer server.Close()
	serverURL, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}

	if err := Healthcheck(t.Context(), serverURL.Host, "https://example.com", time.Second); err != nil {
		t.Fatalf("Healthcheck() error = %v", err)
	}
	if got := <-hosts; got != "example.com" {
		t.Fatalf("healthcheck Host = %q, want %q", got, "example.com")
	}
}

func TestHealthcheckClientDisablesProxyResolution(t *testing.T) {
	t.Parallel()

	client := loopbackHTTPClient(time.Second)

	if client.Timeout != time.Second {
		t.Fatalf("client timeout = %s, want %s", client.Timeout, time.Second)
	}
	transport, ok := client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("client transport = %T, want *http.Transport", client.Transport)
	}
	if transport.Proxy != nil {
		t.Fatal("loopback healthcheck transport resolves proxies")
	}
	if transport == http.DefaultTransport {
		t.Fatal("loopback healthcheck mutates the shared default transport")
	}
}

func TestLocalHealthEndpointNormalizesUnspecifiedListener(t *testing.T) {
	t.Parallel()
	endpoint, err := localHealthEndpoint(":18533")
	if err != nil {
		t.Fatal(err)
	}
	if endpoint != "http://127.0.0.1:18533/health/ready" {
		t.Fatalf("localHealthEndpoint() = %q", endpoint)
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
		{path: "/assets/login.css", contentType: "text/css", bodyPart: ".login-body"},
		{path: "/assets/login-original.css", contentType: "text/css", bodyPart: ".scene"},
		{path: "/assets/login-background.js", contentType: "javascript", bodyPart: "prefers-reduced-motion"},
		{path: "/assets/login-background-loader.js", contentType: "javascript", bodyPart: "desktopScene"},
		{path: "/assets/app.js", contentType: "javascript", bodyPart: "document.documentElement"},
		{path: "/assets/maplibre-gl-csp.js", contentType: "javascript", bodyPart: "MapLibre GL JS"},
		{path: "/assets/maplibre-gl-csp-worker.js", contentType: "javascript", bodyPart: "MapLibre GL JS"},
		{path: "/assets/maplibre-gl.css", contentType: "text/css", bodyPart: ".maplibregl-map"},
		{path: "/assets/manifest.json", contentType: "application/json", bodyPart: `"short_name": "HackWerk"`},
		{path: "/assets/hackwerk-icon.svg", contentType: "image/svg+xml", bodyPart: "HackWerk"},
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

func TestVersionedAssetsAreImmutableAndConditionallyCached(t *testing.T) {
	t.Parallel()

	paths, err := assets.LoadPaths()
	if err != nil {
		t.Fatal(err)
	}
	router := testRouter(t, pinger{})
	request := httptest.NewRequestWithContext(context.Background(), http.MethodGet, paths.CSS, nil)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	if response.Header().Get("Cache-Control") != immutableAssetCacheControl {
		t.Fatalf("Cache-Control = %q", response.Header().Get("Cache-Control"))
	}
	etag := response.Header().Get("ETag")
	if etag == "" {
		t.Fatal("versioned asset has no ETag")
	}

	conditional := httptest.NewRequestWithContext(context.Background(), http.MethodGet, paths.CSS, nil)
	conditional.Header.Set("If-None-Match", etag)
	notModified := httptest.NewRecorder()
	router.ServeHTTP(notModified, conditional)
	if notModified.Code != http.StatusNotModified {
		t.Fatalf("conditional status = %d, want %d", notModified.Code, http.StatusNotModified)
	}

	unversioned := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/assets/app.css", nil)
	revalidated := httptest.NewRecorder()
	router.ServeHTTP(revalidated, unversioned)
	if strings.Contains(revalidated.Header().Get("Cache-Control"), "immutable") {
		t.Fatalf("unversioned Cache-Control = %q", revalidated.Header().Get("Cache-Control"))
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
			MaxBodyBytes:      16 << 20,
			AllowedHosts:      []string{"example.com"},
			InternalRateLimit: 600,
		},
		Database: config.Database{ReadinessTimeout: time.Second, ExpectedSchema: config.CurrentSchemaVersion},
		Metrics:  config.Metrics{WorkerStaleAfter: time.Minute},
	}
}
