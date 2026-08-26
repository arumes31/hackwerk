package maptile

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

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
