package planning

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestHaversineMatrix(t *testing.T) {
	matrix, err := NewHaversineRouter(1.3, 55).Matrix(context.Background(), []Point{{48.2, 14.2}, {48.3, 14.3}})
	if err != nil {
		t.Fatal(err)
	}
	if matrix.Source != "haversine" || !matrix.Estimated || matrix.Cells[0][1].DistanceMeters <= 0 || matrix.Cells[0][1].Duration <= 0 {
		t.Fatalf("matrix=%+v", matrix)
	}
}

func TestHaversineDirectionsAreTransparentStraightLineEstimate(t *testing.T) {
	points := []Point{{48.2, 14.2}, {48.3, 14.3}, {48.4, 14.4}}
	directions, err := NewHaversineRouter(1.3, 55).Directions(t.Context(), points)
	if err != nil {
		t.Fatal(err)
	}
	if directions.Source != "haversine" || !directions.Estimated || len(directions.Geometry) != len(points) || len(directions.Legs) != len(points)-1 || directions.DistanceMeters <= 0 || directions.Duration <= 0 {
		t.Fatalf("directions=%+v", directions)
	}
}

func TestOSRMRejectsSSRFConfiguration(t *testing.T) {
	for _, raw := range []string{"http://router.example/path", "https://localhost/path", "https://127.0.0.1/path", "https://router.example/path?target=x"} {
		if _, err := NewOSRMRouter(OSRMConfig{BaseURL: raw}); !errors.Is(err, ErrValidation) {
			t.Fatalf("%s accepted: %v", raw, err)
		}
	}
}

func TestOSRMInternalEndpointIsExactAndExplicit(t *testing.T) {
	if _, err := NewOSRMRouter(OSRMConfig{BaseURL: "http://osrm:5000", Internal: true}); err != nil {
		t.Fatalf("exact internal endpoint rejected: %v", err)
	}
	for _, raw := range []string{
		"http://osrm", "http://osrm:80", "http://OSRM:5000", "http://osrm:5000/",
		"http://osrm:5000/base", "http://osrm:5000?target=x", "http://user@osrm:5000",
		"https://osrm:5000", "http://127.0.0.1:5000", "http://router:5000",
	} {
		if _, err := NewOSRMRouter(OSRMConfig{BaseURL: raw, Internal: true}); !errors.Is(err, ErrValidation) {
			t.Fatalf("internal endpoint %q accepted: %v", raw, err)
		}
	}
	if _, err := NewOSRMRouter(OSRMConfig{BaseURL: "http://osrm:5000"}); !errors.Is(err, ErrValidation) {
		t.Fatalf("internal endpoint accepted without opt-in: %v", err)
	}
}

func TestOSRMTailscaleEndpointIsNumericAndExplicit(t *testing.T) {
	if _, err := NewOSRMRouter(OSRMConfig{BaseURL: "http://100.115.58.99:5000", Tailscale: true}); err != nil {
		t.Fatalf("exact Tailscale endpoint rejected: %v", err)
	}
	for _, raw := range []string{
		"http://100.115.58.99", "http://100.115.58.99:80", "http://router:5000",
		"http://100.115.58.99:5000/", "http://100.115.58.99:5000/base",
		"http://100.115.58.99:5000?target=x", "http://user@100.115.58.99:5000",
		"https://100.115.58.99:5000", "http://100.63.255.255:5000",
		"http://100.128.0.0:5000", "http://10.0.0.1:5000", "http://127.0.0.1:5000",
	} {
		if _, err := NewOSRMRouter(OSRMConfig{BaseURL: raw, Tailscale: true}); !errors.Is(err, ErrValidation) {
			t.Fatalf("Tailscale endpoint %q accepted: %v", raw, err)
		}
	}
	if _, err := NewOSRMRouter(OSRMConfig{BaseURL: "http://100.115.58.99:5000"}); !errors.Is(err, ErrValidation) {
		t.Fatalf("Tailscale endpoint accepted without opt-in: %v", err)
	}
	if _, err := NewOSRMRouter(OSRMConfig{BaseURL: "http://100.115.58.99:5000", Internal: true, Tailscale: true}); !errors.Is(err, ErrValidation) {
		t.Fatalf("mixed internal/Tailscale mode accepted: %v", err)
	}
}

func TestOSRMMatrixContainsCoordinatesOnly(t *testing.T) {
	var path string
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.RequestURI()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":"Ok","distances":[[0,1200],[1200,0]],"durations":[[0,100],[100,0]]}`))
	}))
	defer server.Close()
	base, _ := url.Parse(server.URL)
	router := &OSRMRouter{base: base, client: server.Client(), max: 1 << 20, backoff: time.Minute, now: time.Now}
	matrix, err := router.Matrix(context.Background(), []Point{{48.2, 14.2}, {48.3, 14.3}})
	if err != nil {
		t.Fatal(err)
	}
	if matrix.Source != "osrm" || matrix.Cells[0][1].DistanceMeters != 1200 {
		t.Fatalf("matrix=%+v", matrix)
	}
	for _, pii := range []string{"Huber", "+43660", "@"} {
		if strings.Contains(path, pii) {
			t.Fatalf("PII in path %q", path)
		}
	}
}

func TestOSRMMatrixRejectsInvalidMetrics(t *testing.T) {
	tests := []struct {
		name    string
		payload string
	}{
		{
			name:    "negative distance",
			payload: `{"code":"Ok","distances":[[0,-1],[1,0]],"durations":[[0,1],[1,0]]}`,
		},
		{
			name:    "negative duration",
			payload: `{"code":"Ok","distances":[[0,1],[1,0]],"durations":[[0,-1],[1,0]]}`,
		},
		{
			name:    "distance overflow",
			payload: `{"code":"Ok","distances":[[0,1e20],[1,0]],"durations":[[0,1],[1,0]]}`,
		},
		{
			name:    "duration overflow",
			payload: `{"code":"Ok","distances":[[0,1],[1,0]],"durations":[[0,1e20],[1,0]]}`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(test.payload))
			}))
			defer server.Close()
			base, _ := url.Parse(server.URL)
			router := &OSRMRouter{base: base, client: server.Client(), max: 1 << 20, backoff: time.Minute, now: time.Now}
			if _, err := router.Matrix(t.Context(), []Point{{48.2, 14.2}, {48.3, 14.3}}); err == nil {
				t.Fatal("invalid matrix metric accepted")
			}
		})
	}
}

func TestOSRMDirectionsContainValidatedGeometryAndNoPII(t *testing.T) {
	var path string
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.RequestURI()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":"Ok","routes":[{"distance":2300,"duration":180,"geometry":{"type":"LineString","coordinates":[[14.2,48.2],[14.25,48.25],[14.3,48.3]]},"legs":[{"distance":2300,"duration":180}]}]}`))
	}))
	defer server.Close()
	base, _ := url.Parse(server.URL)
	router := &OSRMRouter{base: base, client: server.Client(), max: 1 << 20, backoff: time.Minute, now: time.Now}
	directions, err := router.Directions(t.Context(), []Point{{48.2, 14.2}, {48.3, 14.3}})
	if err != nil {
		t.Fatal(err)
	}
	if directions.Source != "osrm" || directions.Estimated || directions.DistanceMeters != 2300 || directions.Duration != 3*time.Minute || len(directions.Geometry) != 3 || len(directions.Legs) != 1 {
		t.Fatalf("directions=%+v", directions)
	}
	if !strings.Contains(path, "/route/v1/driving/") || !strings.Contains(path, "geometries=geojson") {
		t.Fatalf("route request path=%q", path)
	}
	for _, pii := range []string{"Huber", "+43660", "@"} {
		if strings.Contains(path, pii) {
			t.Fatalf("PII in path %q", path)
		}
	}
}

func TestOSRMDirectionsRejectMalformedGeometry(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"code":"Ok","routes":[{"distance":10,"duration":5,"geometry":{"type":"LineString","coordinates":[[14.2,48.2],[999,999]]},"legs":[{"distance":10,"duration":5}]}]}`))
	}))
	defer server.Close()
	base, _ := url.Parse(server.URL)
	router := &OSRMRouter{base: base, client: server.Client(), max: 1 << 20, backoff: time.Minute, now: time.Now}
	if _, err := router.Directions(t.Context(), []Point{{48.2, 14.2}, {48.3, 14.3}}); err == nil {
		t.Fatal("malformed geometry accepted")
	}
}

func TestOSRMResponseLimitAndBackoff(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(strings.Repeat("x", 2048))) }))
	defer server.Close()
	base, _ := url.Parse(server.URL)
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	router := &OSRMRouter{base: base, client: server.Client(), max: 1024, backoff: time.Minute, now: func() time.Time { return now }}
	points := []Point{{48.2, 14.2}, {48.3, 14.3}}
	for range 3 {
		if _, err := router.Matrix(context.Background(), points); err == nil {
			t.Fatal("oversized response accepted")
		}
	}
	if _, err := router.Matrix(context.Background(), points); err == nil || !strings.Contains(err.Error(), "backoff") {
		t.Fatalf("backoff err=%v", err)
	}
}

type countingRouter struct{ calls int }

func (r *countingRouter) Matrix(_ context.Context, points []Point) (Matrix, error) {
	r.calls++
	return NewHaversineRouter(1.3, 55).Matrix(context.Background(), points)
}
func TestCachedRouterIsBoundedByCoordinateKey(t *testing.T) {
	next := &countingRouter{}
	cached := NewCachedRouter(next, time.Hour, 2)
	points := []Point{{48.2, 14.2}, {48.3, 14.3}}
	if _, err := cached.Matrix(context.Background(), points); err != nil {
		t.Fatal(err)
	}
	if _, err := cached.Matrix(context.Background(), points); err != nil {
		t.Fatal(err)
	}
	if next.calls != 1 {
		t.Fatalf("provider calls=%d", next.calls)
	}
	_, _ = cached.Matrix(context.Background(), []Point{{48.2, 14.2}, {48.4, 14.4}})
	_, _ = cached.Matrix(context.Background(), []Point{{48.2, 14.2}, {48.5, 14.5}})
	cache := cached.(*CachedRouter)
	cache.mu.Lock()
	entries := len(cache.entries)
	cache.mu.Unlock()
	if entries != 2 {
		t.Fatalf("cache entries=%d", entries)
	}
}

type failingRouter struct{}

func (failingRouter) Matrix(context.Context, []Point) (Matrix, error) {
	return Matrix{}, errors.New("provider down")
}
func (failingRouter) Directions(context.Context, []Point) (RouteDirections, error) {
	return RouteDirections{}, errors.New("provider down")
}
func TestFallbackRouterUsesTransparentHaversine(t *testing.T) {
	router := FallbackRouter{Primary: failingRouter{}, Fallback: NewHaversineRouter(1.3, 55)}
	matrix, err := router.Matrix(context.Background(), []Point{{48.2, 14.2}, {48.3, 14.3}})
	if err != nil {
		t.Fatal(err)
	}
	if matrix.Source != "haversine" || !matrix.Estimated {
		t.Fatalf("matrix=%+v", matrix)
	}
}

func TestFallbackRouterDirectionsUseTransparentHaversine(t *testing.T) {
	router := FallbackRouter{Primary: failingRouter{}, Fallback: NewHaversineRouter(1.3, 55)}
	directions, err := router.Directions(t.Context(), []Point{{48.2, 14.2}, {48.3, 14.3}})
	if err != nil {
		t.Fatal(err)
	}
	if directions.Source != "haversine" || !directions.Estimated || len(directions.Legs) != 1 {
		t.Fatalf("directions=%+v", directions)
	}
}
