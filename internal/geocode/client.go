// Package geocode provides a bounded forward-geocoding adapter for map search.
package geocode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	minQueryLength = 3
	maxQueryLength = 200
	maxLabelLength = 500
)

var (
	ErrInvalidQuery = errors.New("geocode: invalid query")
	ErrUpstream     = errors.New("geocode: upstream unavailable")
)

// Result is one reviewed map-search candidate. Bounds use south, north, west,
// east order to match Nominatim's JSON response.
type Result struct {
	Label     string     `json:"label"`
	Latitude  float64    `json:"latitude"`
	Longitude float64    `json:"longitude"`
	Bounds    [4]float64 `json:"bounds"`
}

// Searcher is the small application boundary consumed by the HTTP layer.
type Searcher interface {
	Search(context.Context, string) ([]Result, error)
}

type Config struct {
	SearchURL       string
	CountryCodes    []string
	Timeout         time.Duration
	MaxResponseSize int64
	MaxResults      int
	MinInterval     time.Duration
	CacheTTL        time.Duration
	CacheEntries    int
	UserAgent       string
}

type cacheEntry struct {
	expires time.Time
	results []Result
}

// Client queries a statically configured Nominatim-compatible provider.
type Client struct {
	baseURL         *url.URL
	countryCodes    string
	maxResponseSize int64
	maxResults      int
	minInterval     time.Duration
	cacheTTL        time.Duration
	cacheEntries    int
	userAgent       string
	httpClient      *http.Client

	cacheMu sync.Mutex
	cache   map[string]cacheEntry
	now     func() time.Time

	requestMu   sync.Mutex
	lastRequest time.Time
}

func New(cfg Config) (*Client, error) {
	parsed, err := url.Parse(strings.TrimSpace(cfg.SearchURL))
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("geocode: invalid static search URL")
	}
	if cfg.Timeout < time.Second || cfg.Timeout > 30*time.Second || cfg.MaxResponseSize < 1024 || cfg.MaxResponseSize > 2<<20 ||
		cfg.MaxResults < 1 || cfg.MaxResults > 10 || cfg.MinInterval < 0 || cfg.MinInterval > time.Minute ||
		cfg.CacheTTL < time.Minute || cfg.CacheTTL > 30*24*time.Hour || cfg.CacheEntries < 1 || cfg.CacheEntries > 4096 || strings.TrimSpace(cfg.UserAgent) == "" {
		return nil, errors.New("geocode: invalid configuration")
	}
	return &Client{
		baseURL: parsed, countryCodes: strings.Join(cfg.CountryCodes, ","), maxResponseSize: cfg.MaxResponseSize,
		maxResults: cfg.MaxResults, minInterval: cfg.MinInterval, cacheTTL: cfg.CacheTTL, cacheEntries: cfg.CacheEntries,
		userAgent:  strings.TrimSpace(cfg.UserAgent),
		httpClient: &http.Client{Timeout: cfg.Timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
		cache:      make(map[string]cacheEntry), now: time.Now,
	}, nil
}

func (client *Client) Search(ctx context.Context, rawQuery string) ([]Result, error) {
	query := strings.Join(strings.Fields(rawQuery), " ")
	if len(query) < minQueryLength || len(query) > maxQueryLength {
		return nil, ErrInvalidQuery
	}
	cacheKey := strings.ToLower(query)
	if results, ok := client.cached(cacheKey); ok {
		return results, nil
	}
	client.requestMu.Lock()
	defer client.requestMu.Unlock()
	if results, ok := client.cached(cacheKey); ok {
		return results, nil
	}
	if err := client.waitForInterval(ctx); err != nil {
		return nil, fmt.Errorf("%w: request cancelled", ErrUpstream)
	}
	client.lastRequest = client.now()

	requestURL := *client.baseURL
	values := requestURL.Query()
	values.Set("q", query)
	values.Set("format", "jsonv2")
	values.Set("addressdetails", "0")
	values.Set("layer", "address")
	values.Set("limit", strconv.Itoa(client.maxResults))
	if client.countryCodes != "" {
		values.Set("countrycodes", client.countryCodes)
	}
	requestURL.RawQuery = values.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("%w: creating request", ErrUpstream)
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Accept-Language", "de-AT,de;q=0.9")
	request.Header.Set("User-Agent", client.userAgent)
	response, err := client.httpClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("%w: request failed", ErrUpstream)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: status %d", ErrUpstream, response.StatusCode)
	}
	contentType := strings.ToLower(strings.TrimSpace(strings.SplitN(response.Header.Get("Content-Type"), ";", 2)[0]))
	if contentType != "application/json" {
		return nil, fmt.Errorf("%w: invalid content type", ErrUpstream)
	}
	payload, err := io.ReadAll(io.LimitReader(response.Body, client.maxResponseSize+1))
	if err != nil || int64(len(payload)) > client.maxResponseSize {
		return nil, fmt.Errorf("%w: invalid response size", ErrUpstream)
	}
	results, err := client.decode(payload)
	if err != nil {
		return nil, err
	}
	client.store(cacheKey, results)
	return cloneResults(results), nil
}

func (client *Client) waitForInterval(ctx context.Context) error {
	wait := client.minInterval - client.now().Sub(client.lastRequest)
	if client.lastRequest.IsZero() || wait <= 0 {
		return nil
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (client *Client) decode(payload []byte) ([]Result, error) {
	var upstream []struct {
		DisplayName string    `json:"display_name"`
		Latitude    string    `json:"lat"`
		Longitude   string    `json:"lon"`
		Bounds      [4]string `json:"boundingbox"`
	}
	if err := json.Unmarshal(payload, &upstream); err != nil {
		return nil, fmt.Errorf("%w: invalid JSON", ErrUpstream)
	}
	if len(upstream) > client.maxResults {
		upstream = upstream[:client.maxResults]
	}
	results := make([]Result, 0, len(upstream))
	for _, candidate := range upstream {
		label := strings.TrimSpace(candidate.DisplayName)
		latitude, latitudeErr := strconv.ParseFloat(candidate.Latitude, 64)
		longitude, longitudeErr := strconv.ParseFloat(candidate.Longitude, 64)
		if label == "" || len(label) > maxLabelLength || latitudeErr != nil || longitudeErr != nil || !validPoint(latitude, longitude) {
			continue
		}
		result := Result{Label: label, Latitude: latitude, Longitude: longitude, Bounds: [4]float64{latitude, latitude, longitude, longitude}}
		validBounds := true
		for index, value := range candidate.Bounds {
			parsed, parseErr := strconv.ParseFloat(value, 64)
			if parseErr != nil {
				validBounds = false
				continue
			}
			result.Bounds[index] = parsed
		}
		if !validBounds || !validBoundsValues(result.Bounds) {
			result.Bounds = [4]float64{latitude, latitude, longitude, longitude}
		}
		results = append(results, result)
	}
	return results, nil
}

func (client *Client) cached(key string) ([]Result, bool) {
	client.cacheMu.Lock()
	defer client.cacheMu.Unlock()
	entry, ok := client.cache[key]
	if !ok || !client.now().Before(entry.expires) {
		delete(client.cache, key)
		return nil, false
	}
	return cloneResults(entry.results), true
}

func (client *Client) store(key string, results []Result) {
	client.cacheMu.Lock()
	defer client.cacheMu.Unlock()
	if len(client.cache) >= client.cacheEntries {
		oldestKey := ""
		var oldest time.Time
		for candidateKey, entry := range client.cache {
			if oldestKey == "" || entry.expires.Before(oldest) {
				oldestKey, oldest = candidateKey, entry.expires
			}
		}
		delete(client.cache, oldestKey)
	}
	client.cache[key] = cacheEntry{expires: client.now().Add(client.cacheTTL), results: cloneResults(results)}
}

func validPoint(latitude, longitude float64) bool {
	return latitude >= -90 && latitude <= 90 && longitude >= -180 && longitude <= 180
}

func validBoundsValues(bounds [4]float64) bool {
	return validPoint(bounds[0], bounds[2]) && validPoint(bounds[1], bounds[3]) && bounds[0] <= bounds[1] && bounds[2] <= bounds[3]
}

func cloneResults(results []Result) []Result {
	return append([]Result(nil), results...)
}
