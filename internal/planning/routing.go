package planning

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"example.invalid/hackplan/internal/outbound"
)

type HaversineRouter struct {
	RoadFactor, SpeedKMH float64
	now                  func() time.Time
}

type RouteLeg struct {
	DistanceMeters int
	Duration       time.Duration
}

// RouteDirections is an ordered, provider-neutral route shape. Geometry contains
// only coordinates; customer and job data never crosses the routing port.
type RouteDirections struct {
	Geometry       []Point
	Legs           []RouteLeg
	DistanceMeters int
	Duration       time.Duration
	Source         string
	Estimated      bool
	FreshAt        time.Time
}

type DirectionsRouter interface {
	Directions(context.Context, []Point) (RouteDirections, error)
}

var (
	_ Router           = (*HaversineRouter)(nil)
	_ DirectionsRouter = (*HaversineRouter)(nil)
	_ Router           = (*OSRMRouter)(nil)
	_ DirectionsRouter = (*OSRMRouter)(nil)
	_ Router           = FallbackRouter{}
	_ DirectionsRouter = FallbackRouter{}
	_ Router           = (*CachedRouter)(nil)
	_ DirectionsRouter = (*CachedRouter)(nil)
)

func NewHaversineRouter(roadFactor, speedKMH float64) *HaversineRouter {
	if roadFactor < 1 {
		roadFactor = 1.3
	}
	if speedKMH <= 0 {
		speedKMH = 55
	}
	return &HaversineRouter{RoadFactor: roadFactor, SpeedKMH: speedKMH, now: time.Now}
}
func (r *HaversineRouter) Matrix(_ context.Context, points []Point) (Matrix, error) {
	if len(points) < 2 || len(points) > 25 {
		return Matrix{}, ErrValidation
	}
	cells := make([][]MatrixCell, len(points))
	for i, a := range points {
		if !a.Valid() {
			return Matrix{}, ErrValidation
		}
		cells[i] = make([]MatrixCell, len(points))
		for j, b := range points {
			distance := haversine(a, b) * r.RoadFactor
			cells[i][j] = MatrixCell{DistanceMeters: int(math.Round(distance)), Duration: time.Duration(distance / 1000 / r.SpeedKMH * float64(time.Hour))}
		}
	}
	return Matrix{Cells: cells, Source: "haversine", Estimated: true, FreshAt: r.now().UTC()}, nil
}

func (r *HaversineRouter) Directions(ctx context.Context, points []Point) (RouteDirections, error) {
	matrix, err := r.Matrix(ctx, points)
	if err != nil {
		return RouteDirections{}, err
	}
	result := RouteDirections{
		Geometry: append([]Point(nil), points...), Source: matrix.Source,
		Estimated: matrix.Estimated, FreshAt: matrix.FreshAt,
	}
	for index := 0; index < len(points)-1; index++ {
		cell := matrix.Cells[index][index+1]
		result.Legs = append(result.Legs, RouteLeg(cell))
		result.DistanceMeters += cell.DistanceMeters
		result.Duration += cell.Duration
	}
	return result, nil
}
func haversine(a, b Point) float64 {
	const earth = 6371000.
	lat1, lat2 := a.Latitude*math.Pi/180, b.Latitude*math.Pi/180
	dLat := (b.Latitude - a.Latitude) * math.Pi / 180
	dLon := (b.Longitude - a.Longitude) * math.Pi / 180
	h := math.Sin(dLat/2)*math.Sin(dLat/2) + math.Cos(lat1)*math.Cos(lat2)*math.Sin(dLon/2)*math.Sin(dLon/2)
	return earth * 2 * math.Atan2(math.Sqrt(h), math.Sqrt(1-h))
}

type OSRMConfig struct {
	BaseURL          string
	Timeout, Backoff time.Duration
	MaxResponseBytes int
}
type OSRMRouter struct {
	base         *url.URL
	client       *http.Client
	max          int
	backoff      time.Duration
	now          func() time.Time
	mu           sync.Mutex
	failures     int
	blockedUntil time.Time
}

func NewOSRMRouter(cfg OSRMConfig) (*OSRMRouter, error) {
	parsed, err := url.Parse(strings.TrimSpace(cfg.BaseURL))
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || loopback(parsed.Hostname()) {
		return nil, ErrValidation
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 5 * time.Second
	}
	if cfg.Backoff <= 0 {
		cfg.Backoff = 30 * time.Second
	}
	if cfg.MaxResponseBytes < 1024 {
		cfg.MaxResponseBytes = 1 << 20
	}
	client := &http.Client{Transport: outbound.Transport(), Timeout: cfg.Timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("planning: routing redirect rejected") }}
	return &OSRMRouter{base: parsed, client: client, max: cfg.MaxResponseBytes, backoff: cfg.Backoff, now: time.Now}, nil
}
func (r *OSRMRouter) Matrix(ctx context.Context, points []Point) (result Matrix, resultErr error) {
	if len(points) < 2 || len(points) > 25 {
		return Matrix{}, ErrValidation
	}
	r.mu.Lock()
	blocked := r.now().Before(r.blockedUntil)
	r.mu.Unlock()
	if blocked {
		return Matrix{}, errors.New("planning: routing backoff active")
	}
	coordinates := make([]string, len(points))
	for i, p := range points {
		if !p.Valid() {
			return Matrix{}, ErrValidation
		}
		coordinates[i] = strconv.FormatFloat(p.Longitude, 'f', 6, 64) + "," + strconv.FormatFloat(p.Latitude, 'f', 6, 64)
	}
	target := *r.base
	target.Path = strings.TrimRight(target.Path, "/") + "/table/v1/driving/" + strings.Join(coordinates, ";")
	target.RawQuery = "annotations=distance,duration"
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return Matrix{}, err
	}
	response, err := r.client.Do(request)
	if err != nil {
		r.failed()
		return Matrix{}, fmt.Errorf("planning: routing request: %w", err)
	}
	defer func() { resultErr = errors.Join(resultErr, response.Body.Close()) }()
	if response.StatusCode != http.StatusOK {
		r.failed()
		return Matrix{}, errors.New("planning: routing provider rejected request")
	}
	limited := io.LimitReader(response.Body, int64(r.max)+1)
	payload, err := io.ReadAll(limited)
	if err != nil || len(payload) > r.max {
		r.failed()
		return Matrix{}, errors.New("planning: routing response invalid")
	}
	var decoded struct {
		Code      string       `json:"code"`
		Distances [][]*float64 `json:"distances"`
		Durations [][]*float64 `json:"durations"`
	}
	if json.Unmarshal(payload, &decoded) != nil || decoded.Code != "Ok" || len(decoded.Distances) != len(points) || len(decoded.Durations) != len(points) {
		r.failed()
		return Matrix{}, errors.New("planning: routing response invalid")
	}
	cells := make([][]MatrixCell, len(points))
	for i := range points {
		if len(decoded.Distances[i]) != len(points) || len(decoded.Durations[i]) != len(points) {
			r.failed()
			return Matrix{}, errors.New("planning: routing matrix dimensions invalid")
		}
		cells[i] = make([]MatrixCell, len(points))
		for j := range points {
			if decoded.Distances[i][j] == nil || decoded.Durations[i][j] == nil {
				r.failed()
				return Matrix{}, errors.New("planning: incomplete routing matrix")
			}
			if !validRouteMetric(*decoded.Distances[i][j]) || !validRouteDuration(*decoded.Durations[i][j]) {
				r.failed()
				return Matrix{}, errors.New("planning: routing matrix value invalid")
			}
			cells[i][j] = MatrixCell{DistanceMeters: int(math.Round(*decoded.Distances[i][j])), Duration: time.Duration(*decoded.Durations[i][j] * float64(time.Second))}
		}
	}
	r.succeeded()
	return Matrix{Cells: cells, Source: "osrm", FreshAt: r.now().UTC()}, nil
}

func (r *OSRMRouter) Directions(ctx context.Context, points []Point) (result RouteDirections, resultErr error) {
	if len(points) < 2 || len(points) > 25 {
		return RouteDirections{}, ErrValidation
	}
	if r.isBlocked() {
		return RouteDirections{}, errors.New("planning: routing backoff active")
	}
	coordinates := make([]string, len(points))
	for index, point := range points {
		if !point.Valid() {
			return RouteDirections{}, ErrValidation
		}
		coordinates[index] = strconv.FormatFloat(point.Longitude, 'f', 6, 64) + "," + strconv.FormatFloat(point.Latitude, 'f', 6, 64)
	}
	target := *r.base
	target.Path = strings.TrimRight(target.Path, "/") + "/route/v1/driving/" + strings.Join(coordinates, ";")
	target.RawQuery = "overview=full&geometries=geojson&steps=false"
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return RouteDirections{}, err
	}
	response, err := r.client.Do(request)
	if err != nil {
		r.failed()
		return RouteDirections{}, fmt.Errorf("planning: routing request: %w", err)
	}
	defer func() { resultErr = errors.Join(resultErr, response.Body.Close()) }()
	if response.StatusCode != http.StatusOK {
		r.failed()
		return RouteDirections{}, errors.New("planning: routing provider rejected request")
	}
	payload, err := io.ReadAll(io.LimitReader(response.Body, int64(r.max)+1))
	if err != nil || len(payload) > r.max {
		r.failed()
		return RouteDirections{}, errors.New("planning: routing response invalid")
	}
	var decoded struct {
		Code   string `json:"code"`
		Routes []struct {
			Distance float64 `json:"distance"`
			Duration float64 `json:"duration"`
			Geometry struct {
				Type        string      `json:"type"`
				Coordinates [][]float64 `json:"coordinates"`
			} `json:"geometry"`
			Legs []struct {
				Distance float64 `json:"distance"`
				Duration float64 `json:"duration"`
			} `json:"legs"`
		} `json:"routes"`
	}
	if json.Unmarshal(payload, &decoded) != nil || decoded.Code != "Ok" || len(decoded.Routes) != 1 {
		r.failed()
		return RouteDirections{}, errors.New("planning: routing response invalid")
	}
	route := decoded.Routes[0]
	if route.Geometry.Type != "LineString" || len(route.Geometry.Coordinates) < 2 || len(route.Geometry.Coordinates) > 100000 || len(route.Legs) != len(points)-1 || !validRouteMetric(route.Distance) || !validRouteDuration(route.Duration) {
		r.failed()
		return RouteDirections{}, errors.New("planning: routing route invalid")
	}
	result = RouteDirections{
		Geometry: make([]Point, 0, len(route.Geometry.Coordinates)), DistanceMeters: int(math.Round(route.Distance)),
		Duration: time.Duration(route.Duration * float64(time.Second)), Source: "osrm", FreshAt: r.now().UTC(),
	}
	for _, coordinate := range route.Geometry.Coordinates {
		if len(coordinate) != 2 {
			r.failed()
			return RouteDirections{}, errors.New("planning: routing geometry invalid")
		}
		point := Point{Latitude: coordinate[1], Longitude: coordinate[0]}
		if !point.Valid() || math.IsNaN(point.Latitude) || math.IsNaN(point.Longitude) || math.IsInf(point.Latitude, 0) || math.IsInf(point.Longitude, 0) {
			r.failed()
			return RouteDirections{}, errors.New("planning: routing geometry invalid")
		}
		result.Geometry = append(result.Geometry, point)
	}
	for _, leg := range route.Legs {
		if !validRouteMetric(leg.Distance) || !validRouteDuration(leg.Duration) {
			r.failed()
			return RouteDirections{}, errors.New("planning: routing leg invalid")
		}
		result.Legs = append(result.Legs, RouteLeg{
			DistanceMeters: int(math.Round(leg.Distance)),
			Duration:       time.Duration(leg.Duration * float64(time.Second)),
		})
	}
	r.succeeded()
	return result, nil
}

func (r *OSRMRouter) isBlocked() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.now().Before(r.blockedUntil)
}

func (r *OSRMRouter) succeeded() {
	r.mu.Lock()
	r.failures = 0
	r.blockedUntil = time.Time{}
	r.mu.Unlock()
}

func validRouteMetric(value float64) bool {
	return value >= 0 && value <= float64(math.MaxInt) && !math.IsNaN(value) && !math.IsInf(value, 0)
}

func validRouteDuration(value float64) bool {
	return value >= 0 && value <= float64(math.MaxInt64)/float64(time.Second) && !math.IsNaN(value) && !math.IsInf(value, 0)
}
func (r *OSRMRouter) failed() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.failures++
	if r.failures >= 3 {
		r.blockedUntil = r.now().Add(r.backoff)
	}
}
func loopback(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && (ip.IsLoopback() || ip.IsUnspecified())
}

type FallbackRouter struct{ Primary, Fallback Router }

func (r FallbackRouter) Matrix(ctx context.Context, points []Point) (Matrix, error) {
	if r.Primary != nil {
		if result, err := r.Primary.Matrix(ctx, points); err == nil {
			return result, nil
		}
	}
	if r.Fallback == nil {
		return Matrix{}, errors.New("planning: routing unavailable")
	}
	return r.Fallback.Matrix(ctx, points)
}

func (r FallbackRouter) Directions(ctx context.Context, points []Point) (RouteDirections, error) {
	if primary, ok := r.Primary.(DirectionsRouter); ok {
		if result, err := primary.Directions(ctx, points); err == nil {
			return result, nil
		}
	}
	if fallback, ok := r.Fallback.(DirectionsRouter); ok {
		return fallback.Directions(ctx, points)
	}
	return RouteDirections{}, errors.New("planning: route directions unavailable")
}

type matrixCacheEntry struct {
	value                Matrix
	expiresAt, createdAt time.Time
}

type CachedRouter struct {
	next       Router
	ttl        time.Duration
	maxEntries int
	now        func() time.Time
	mu         sync.Mutex
	entries    map[string]matrixCacheEntry
}

func NewCachedRouter(next Router, ttl time.Duration, maxEntries int) Router {
	if next == nil {
		return nil
	}
	if ttl <= 0 {
		ttl = time.Hour
	}
	if maxEntries < 1 {
		maxEntries = 512
	}
	return &CachedRouter{next: next, ttl: ttl, maxEntries: maxEntries, now: time.Now, entries: make(map[string]matrixCacheEntry)}
}

func (r *CachedRouter) Matrix(ctx context.Context, points []Point) (Matrix, error) {
	key, now := matrixCacheKey(points), r.now()
	r.mu.Lock()
	entry, ok := r.entries[key]
	if ok && now.Before(entry.expiresAt) {
		r.mu.Unlock()
		return entry.value, nil
	}
	if ok {
		delete(r.entries, key)
	}
	r.mu.Unlock()
	value, err := r.next.Matrix(ctx, points)
	if err != nil {
		return Matrix{}, err
	}
	r.mu.Lock()
	if len(r.entries) >= r.maxEntries {
		oldestKey := ""
		var oldest time.Time
		for candidate, cached := range r.entries {
			if oldestKey == "" || cached.createdAt.Before(oldest) {
				oldestKey, oldest = candidate, cached.createdAt
			}
		}
		delete(r.entries, oldestKey)
	}
	r.entries[key] = matrixCacheEntry{value: value, createdAt: now, expiresAt: now.Add(r.ttl)}
	r.mu.Unlock()
	return value, nil
}

func (r *CachedRouter) Directions(ctx context.Context, points []Point) (RouteDirections, error) {
	next, ok := r.next.(DirectionsRouter)
	if !ok {
		return RouteDirections{}, errors.New("planning: route directions unavailable")
	}
	return next.Directions(ctx, points)
}

func matrixCacheKey(points []Point) string {
	var builder strings.Builder
	for _, point := range points {
		builder.WriteString(strconv.FormatFloat(point.Latitude, 'f', 6, 64))
		builder.WriteByte(',')
		builder.WriteString(strconv.FormatFloat(point.Longitude, 'f', 6, 64))
		builder.WriteByte(';')
	}
	return builder.String()
}
