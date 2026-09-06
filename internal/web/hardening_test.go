package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"example.invalid/hackplan/internal/config"
)

func TestHostAllowlistAndTrustedProxyHeaders(t *testing.T) {
	cfg := config.Config{BaseURL: "https://hackwerk.example", HTTP: config.HTTP{AllowedHosts: []string{"hackwerk.example"}, TrustedProxyCIDRs: []string{"10.0.0.0/8"}}}
	boundary, err := newNetworkBoundary(cfg)
	if err != nil {
		t.Fatal(err)
	}
	handler := boundary.Middleware(securityHeaders(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("X-Test-Remote", request.RemoteAddr)
		response.WriteHeader(http.StatusNoContent)
	})))

	badHost := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "https://evil.example/", nil)
	badHost.Host = "evil.example"
	badHost.RemoteAddr = "10.1.2.3:1234"
	badResponse := httptest.NewRecorder()
	handler.ServeHTTP(badResponse, badHost)
	if badResponse.Code != http.StatusBadRequest {
		t.Fatalf("bad host status=%d", badResponse.Code)
	}

	untrusted := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "http://hackwerk.example/", nil)
	untrusted.RemoteAddr = "203.0.113.9:1234"
	untrusted.Header.Set("X-Forwarded-Proto", "https")
	untrusted.Header.Set("X-Forwarded-For", "198.51.100.2")
	untrustedResponse := httptest.NewRecorder()
	handler.ServeHTTP(untrustedResponse, untrusted)
	if untrustedResponse.Header().Get("Strict-Transport-Security") != "" || untrustedResponse.Header().Get("X-Test-Remote") != "203.0.113.9" {
		t.Fatalf("untrusted forwarded headers accepted: %#v", untrustedResponse.Header())
	}

	trusted := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "http://hackwerk.example/", nil)
	trusted.RemoteAddr = "10.1.2.3:1234"
	trusted.Header.Set("X-Forwarded-Proto", "https")
	trusted.Header.Set("X-Forwarded-For", "198.51.100.2, 10.2.3.4")
	trustedResponse := httptest.NewRecorder()
	handler.ServeHTTP(trustedResponse, trusted)
	if trustedResponse.Header().Get("Strict-Transport-Security") == "" || trustedResponse.Header().Get("X-Test-Remote") != "198.51.100.2" {
		t.Fatalf("trusted forwarded headers ignored: %#v", trustedResponse.Header())
	}

	longChain := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "http://hackwerk.example/", nil)
	longChain.RemoteAddr = "10.1.2.3:1234"
	longChain.Header.Set("X-Forwarded-For", strings.Join([]string{
		"198.51.100.1", "198.51.100.2", "198.51.100.3", "198.51.100.4",
		"198.51.100.5", "198.51.100.6", "198.51.100.7", "198.51.100.8",
		"203.0.113.99", "10.2.3.4",
	}, ", "))
	longChainResponse := httptest.NewRecorder()
	handler.ServeHTTP(longChainResponse, longChain)
	if longChainResponse.Header().Get("X-Test-Remote") != "203.0.113.99" {
		t.Fatalf("long forwarded chain client=%q, want proxy-appended client", longChainResponse.Header().Get("X-Test-Remote"))
	}
}

func TestDevelopmentWildcardAllowsTailscaleHost(t *testing.T) {
	boundary, err := newNetworkBoundary(config.Config{BaseURL: "http://localhost:18533", HTTP: config.HTTP{AllowedHosts: []string{"*"}}})
	if err != nil {
		t.Fatal(err)
	}
	handler := boundary.Middleware(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) { response.WriteHeader(http.StatusNoContent) }))
	for _, host := range []string{"100.115.58.99:18533", "dr-ex-develop01.werewolf-gondola.ts.net:18533"} {
		request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "http://"+host+"/", nil)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusNoContent {
			t.Fatalf("host %q status = %d", host, response.Code)
		}
	}
}

func TestRequestLimitsAndStrictCSP(t *testing.T) {
	handler := requestLimits(8, 10)(securityHeaders(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) { _, _ = response.Write([]byte("ok")) })))
	wrong := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "http://example.com/", strings.NewReader("payload"))
	wrong.Header.Set("Content-Type", "text/plain")
	wrongResponse := httptest.NewRecorder()
	handler.ServeHTTP(wrongResponse, wrong)
	if wrongResponse.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("wrong content status=%d", wrongResponse.Code)
	}
	valid := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "http://example.com/", nil)
	validResponse := httptest.NewRecorder()
	handler.ServeHTTP(validResponse, valid)
	csp := validResponse.Header().Get("Content-Security-Policy")
	if strings.Contains(csp, "unsafe") || !strings.Contains(csp, "frame-ancestors 'none'") || !strings.Contains(csp, "media-src 'self' blob:") {
		t.Fatalf("CSP=%q", csp)
	}
}
