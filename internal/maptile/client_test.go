package maptile

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func TestClientFetchesBoundedRasterTile(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/3/4/5.png" || request.Header.Get("User-Agent") != "HackWerk/test" {
			t.Fatalf("unexpected request %s", request.URL.Path)
		}
		response.Header().Set("Content-Type", "image/png")
		_, _ = response.Write([]byte("png"))
	}))
	defer server.Close()
	client, err := New(Config{URLTemplate: server.URL + "/{z}/{x}/{y}.png", Timeout: time.Second, MaxResponseBytes: 16, MaxZoom: 19, UserAgent: "HackWerk/test"})
	if err != nil {
		t.Fatal(err)
	}
	client.client = server.Client()
	tile, err := client.Fetch(context.Background(), 3, 4, 5)
	if err != nil {
		t.Fatal(err)
	}
	if string(tile.Data) != "png" || tile.ContentType != "image/png" {
		t.Fatalf("unexpected tile %#v", tile)
	}
}

func TestClientRejectsInvalidCoordinatesAndOversizedResponse(t *testing.T) {
	client, err := New(Config{URLTemplate: "https://tiles.example/{z}/{x}/{y}.png", Timeout: time.Second, MaxResponseBytes: 3, MaxZoom: 19})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Fetch(context.Background(), 2, 4, 0); !errors.Is(err, ErrInvalidCoordinate) {
		t.Fatalf("expected coordinate error, got %v", err)
	}

	server := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("Content-Type", "image/png")
		_, _ = response.Write([]byte(strings.Repeat("x", 4)))
	}))
	defer server.Close()
	client.template = server.URL + "/{z}/{x}/{y}.png"
	client.client = server.Client()
	if _, err := client.Fetch(context.Background(), 0, 0, 0); !errors.Is(err, ErrUpstream) {
		t.Fatalf("expected upstream error, got %v", err)
	}
}

func TestNewRejectsIncompleteConfiguration(t *testing.T) {
	valid := Config{URLTemplate: "https://tiles.example/{z}/{x}/{y}.png", Timeout: time.Second, MaxResponseBytes: 1, MaxZoom: 1}
	tests := []struct {
		name   string
		update func(*Config)
	}{
		{name: "blank template", update: func(cfg *Config) { cfg.URLTemplate = " " }},
		{name: "zero timeout", update: func(cfg *Config) { cfg.Timeout = 0 }},
		{name: "zero response limit", update: func(cfg *Config) { cfg.MaxResponseBytes = 0 }},
		{name: "zero max zoom", update: func(cfg *Config) { cfg.MaxZoom = 0 }},
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

func TestClientFetchValidatesUpstreamResponsesAndTemplate(t *testing.T) {
	client, err := New(Config{URLTemplate: "https://tiles.example/{z}/{x}/{y}?access_token={token}", Token: "private", Timeout: time.Second, MaxResponseBytes: 8, MaxZoom: 3, UserAgent: "HackWerk/test"})
	if err != nil {
		t.Fatal(err)
	}
	client.client.Transport = roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.String() != "https://tiles.example/2/1/3?access_token=private" || request.Header.Get("User-Agent") != "HackWerk/test" {
			t.Fatalf("unexpected tile request %s headers=%v", request.URL, request.Header)
		}
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"IMAGE/JPEG; charset=binary"}, "Etag": {`"tile-v1"`}}, Body: io.NopCloser(strings.NewReader("jpeg"))}, nil
	})
	tile, err := client.Fetch(t.Context(), 2, 1, 3)
	if err != nil || tile.ContentType != "image/jpeg" || string(tile.Data) != "jpeg" || tile.ETag != `"tile-v1"` {
		t.Fatalf("Fetch() = %#v, %v", tile, err)
	}

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
				return &http.Response{StatusCode: http.StatusForbidden, Body: io.NopCloser(strings.NewReader("denied"))}, nil
			},
		},
		{
			name: "non image content",
			transport: func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/html"}}, Body: io.NopCloser(strings.NewReader("no tile"))}, nil
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client.client.Transport = test.transport
			if _, err := client.Fetch(t.Context(), 0, 0, 0); !errors.Is(err, ErrUpstream) {
				t.Fatalf("Fetch() error = %v, want upstream error", err)
			}
		})
	}
	client.template = "://not-a-URL"
	if _, err := client.Fetch(t.Context(), 0, 0, 0); err == nil {
		t.Fatal("Fetch() accepted an invalid expanded template")
	}
}
