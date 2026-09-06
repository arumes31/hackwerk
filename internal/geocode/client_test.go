package geocode

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func TestClientSearchUsesBoundedStaticProviderRequestAndCache(t *testing.T) {
	client, err := New(Config{
		SearchURL: "https://geocoder.example/search", CountryCodes: []string{"at"}, Timeout: time.Second,
		MaxResponseSize: 4096, MaxResults: 2, MinInterval: 0, CacheTTL: time.Hour, CacheEntries: 8, UserAgent: "HackWerk/test",
	})
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	client.httpClient.Transport = roundTripFunc(func(request *http.Request) (*http.Response, error) {
		calls++
		query := request.URL.Query()
		if request.URL.Host != "geocoder.example" || request.URL.Path != "/search" || query.Get("q") != "Waldstraße 9" ||
			query.Get("format") != "jsonv2" || query.Get("limit") != "2" || query.Get("countrycodes") != "at" ||
			request.Header.Get("User-Agent") != "HackWerk/test" {
			t.Fatalf("unexpected provider request %s headers=%v", request.URL.String(), request.Header)
		}
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(
			`[{"display_name":"Waldstraße 9, Österreich","lat":"48.2001","lon":"14.2002","boundingbox":["48.19","48.21","14.19","14.21"]}]`,
		))}, nil
	})
	for range 2 {
		results, searchErr := client.Search(context.Background(), "  Waldstraße   9 ")
		if searchErr != nil {
			t.Fatal(searchErr)
		}
		if len(results) != 1 || results[0].Label != "Waldstraße 9, Österreich" || results[0].Bounds[0] != 48.19 {
			t.Fatalf("results=%+v", results)
		}
	}
	if calls != 1 {
		t.Fatalf("provider calls=%d, want 1 cached call", calls)
	}
}

func TestClientSearchRejectsInvalidInputAndUntrustedResponses(t *testing.T) {
	tests := []struct {
		name        string
		query       string
		contentType string
		body        string
		wantErr     error
		wantResults int
	}{
		{name: "short query", query: "ab", wantErr: ErrInvalidQuery},
		{name: "wrong content type", query: "Linz", contentType: "text/html", body: "<html>", wantErr: ErrUpstream},
		{name: "invalid coordinate skipped", query: "Linz", contentType: "application/json", body: `[{"display_name":"Ungültig","lat":"999","lon":"14"}]`, wantResults: 0},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client, err := New(Config{SearchURL: "https://geocoder.example/search", Timeout: time.Second, MaxResponseSize: 4096, MaxResults: 3, CacheTTL: time.Hour, CacheEntries: 8, UserAgent: "HackWerk/test"})
			if err != nil {
				t.Fatal(err)
			}
			client.httpClient.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {test.contentType}}, Body: io.NopCloser(strings.NewReader(test.body))}, nil
			})
			results, searchErr := client.Search(t.Context(), test.query)
			if test.wantErr != nil && !errors.Is(searchErr, test.wantErr) {
				t.Fatalf("Search() error=%v, want %v", searchErr, test.wantErr)
			}
			if test.wantErr == nil && (searchErr != nil || len(results) != test.wantResults) {
				t.Fatalf("Search()=%+v, %v", results, searchErr)
			}
		})
	}
}

func TestNewRejectsUnsafeStaticProviderConfiguration(t *testing.T) {
	valid := Config{
		SearchURL: "https://geocoder.example/search", Timeout: time.Second, MaxResponseSize: 1024,
		MaxResults: 1, CacheTTL: time.Minute, CacheEntries: 1, UserAgent: "HackWerk/test",
	}
	tests := []struct {
		name   string
		update func(*Config)
	}{
		{name: "non HTTPS URL", update: func(cfg *Config) { cfg.SearchURL = "http://geocoder.example/search" }},
		{name: "URL with credentials", update: func(cfg *Config) { cfg.SearchURL = "https://user:pass@geocoder.example/search" }},
		{name: "URL with query", update: func(cfg *Config) { cfg.SearchURL = "https://geocoder.example/search?format=xml" }},
		{name: "URL with fragment", update: func(cfg *Config) { cfg.SearchURL = "https://geocoder.example/search#fragment" }},
		{name: "timeout too short", update: func(cfg *Config) { cfg.Timeout = 0 }},
		{name: "response limit too small", update: func(cfg *Config) { cfg.MaxResponseSize = 1023 }},
		{name: "too many results", update: func(cfg *Config) { cfg.MaxResults = 11 }},
		{name: "cache expires too soon", update: func(cfg *Config) { cfg.CacheTTL = time.Second }},
		{name: "blank user agent", update: func(cfg *Config) { cfg.UserAgent = " " }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := valid
			test.update(&cfg)
			if client, err := New(cfg); err == nil || client != nil {
				t.Fatalf("New() = %v, %v; want configuration error", client, err)
			}
		})
	}
}

func TestClientSearchClassifiesProviderFailures(t *testing.T) {
	tests := []struct {
		name      string
		transport roundTripFunc
	}{
		{
			name: "request error",
			transport: func(*http.Request) (*http.Response, error) {
				return nil, errors.New("provider unavailable")
			},
		},
		{
			name: "non success status",
			transport: func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: http.StatusTooManyRequests, Body: io.NopCloser(strings.NewReader("retry later"))}, nil
			},
		},
		{
			name: "invalid JSON",
			transport: func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader("{"))}, nil
			},
		},
		{
			name: "response above limit",
			transport: func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(strings.Repeat("x", 1025)))}, nil
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client, err := New(Config{SearchURL: "https://geocoder.example/search", Timeout: time.Second, MaxResponseSize: 1024, MaxResults: 1, CacheTTL: time.Minute, CacheEntries: 1, UserAgent: "HackWerk/test"})
			if err != nil {
				t.Fatal(err)
			}
			client.httpClient.Transport = test.transport
			if _, err := client.Search(t.Context(), "Linz"); !errors.Is(err, ErrUpstream) {
				t.Fatalf("Search() error = %v, want upstream error", err)
			}
		})
	}
}

func TestClientDecodeRateLimitAndCacheLifecycle(t *testing.T) {
	client, err := New(Config{SearchURL: "https://geocoder.example/search", Timeout: time.Second, MaxResponseSize: 1024, MaxResults: 2, MinInterval: time.Minute, CacheTTL: time.Minute, CacheEntries: 1, UserAgent: "HackWerk/test"})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	client.now = func() time.Time { return now }
	decoded, err := client.decode([]byte(`[
		{"display_name":"Erste Adresse","lat":"48.2","lon":"14.2","boundingbox":["invalid","48.3","14.1","14.3"]},
		{"display_name":"Ungültig","lat":"NaN","lon":"14.2","boundingbox":["48.2","48.3","14.1","14.3"]},
		{"display_name":"Dritte Adresse","lat":"48.4","lon":"14.4","boundingbox":["48.4","48.5","14.4","14.5"]}
	]`))
	if err != nil || len(decoded) != 1 || decoded[0].Bounds != [4]float64{48.2, 48.2, 14.2, 14.2} {
		t.Fatalf("decode() = %#v, %v", decoded, err)
	}
	client.store("first", decoded)
	client.store("second", []Result{{Label: "Second"}})
	if _, ok := client.cached("first"); ok {
		t.Fatal("cache did not evict its oldest entry")
	}
	cached, ok := client.cached("second")
	if !ok || len(cached) != 1 {
		t.Fatalf("cached second = %#v, %v", cached, ok)
	}
	cached[0].Label = "modified outside cache"
	if again, ok := client.cached("second"); !ok || again[0].Label != "Second" {
		t.Fatalf("cache returned mutable results = %#v, %v", again, ok)
	}
	now = now.Add(time.Minute)
	if _, ok := client.cached("second"); ok {
		t.Fatal("expired cache entry was returned")
	}
	client.lastRequest = now
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := client.waitForInterval(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("waitForInterval() error = %v, want cancellation", err)
	}
	client.lastRequest = time.Time{}
	if err := client.waitForInterval(t.Context()); err != nil {
		t.Fatalf("first request wait = %v", err)
	}
}
