// Package maptile retrieves raster map tiles through a bounded, static
// same-origin proxy so browser code never learns provider credentials.
package maptile

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"example.invalid/hackplan/internal/outbound"
)

var (
	ErrInvalidCoordinate = errors.New("maptile: invalid tile coordinate")
	ErrUpstream          = errors.New("maptile: upstream unavailable")
)

type Config struct {
	URLTemplate      string
	Token            string
	Timeout          time.Duration
	MaxResponseBytes int64
	MaxZoom          int
	UserAgent        string
}

type Tile struct {
	ContentType string
	Data        []byte
	ETag        string
}

type Client struct {
	template  string
	token     string
	maxBytes  int64
	maxZoom   int
	client    *http.Client
	userAgent string
}

func New(cfg Config) (*Client, error) {
	if strings.TrimSpace(cfg.URLTemplate) == "" || cfg.Timeout <= 0 || cfg.MaxResponseBytes <= 0 || cfg.MaxZoom < 1 {
		return nil, errors.New("maptile: invalid configuration")
	}
	client := &http.Client{
		Transport:     outbound.Transport(),
		Timeout:       cfg.Timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	return &Client{
		template:  cfg.URLTemplate,
		token:     cfg.Token,
		maxBytes:  cfg.MaxResponseBytes,
		maxZoom:   cfg.MaxZoom,
		client:    client,
		userAgent: strings.TrimSpace(cfg.UserAgent),
	}, nil
}

func (client *Client) Fetch(ctx context.Context, z, x, y int) (Tile, error) {
	if z < 0 || z > client.maxZoom || x < 0 || y < 0 || x >= 1<<z || y >= 1<<z {
		return Tile{}, ErrInvalidCoordinate
	}
	upstreamURL := strings.NewReplacer(
		"{z}", strconv.Itoa(z), "{x}", strconv.Itoa(x), "{y}", strconv.Itoa(y), "{token}", client.token,
	).Replace(client.template)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, upstreamURL, nil)
	if err != nil {
		return Tile{}, fmt.Errorf("maptile: creating request: %w", err)
	}
	if client.userAgent != "" {
		request.Header.Set("User-Agent", client.userAgent)
	}
	response, err := client.client.Do(request)
	if err != nil {
		return Tile{}, fmt.Errorf("%w: request failed", ErrUpstream)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return Tile{}, fmt.Errorf("%w: status %d", ErrUpstream, response.StatusCode)
	}
	contentType := strings.ToLower(strings.TrimSpace(strings.SplitN(response.Header.Get("Content-Type"), ";", 2)[0]))
	switch contentType {
	case "image/png", "image/jpeg", "image/webp":
	default:
		return Tile{}, fmt.Errorf("%w: invalid content type", ErrUpstream)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, client.maxBytes+1))
	if err != nil {
		return Tile{}, fmt.Errorf("%w: reading response", ErrUpstream)
	}
	if int64(len(data)) > client.maxBytes {
		return Tile{}, fmt.Errorf("%w: response too large", ErrUpstream)
	}
	return Tile{ContentType: contentType, Data: data, ETag: response.Header.Get("ETag")}, nil
}
