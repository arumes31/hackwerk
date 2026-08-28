package postgres

import (
	"errors"
	"math"
	"testing"
	"time"

	"example.invalid/hackplan/internal/planning"
)

func TestRouteGeometryRoundTrip(t *testing.T) {
	t.Parallel()

	want := []planning.Point{
		{Latitude: 48.2, Longitude: 14.2},
		{Latitude: 48.234567, Longitude: 14.345678},
		{Latitude: 48.2, Longitude: 14.2},
	}
	encoded, err := encodeRouteGeometry(want)
	if err != nil {
		t.Fatalf("encodeRouteGeometry() error = %v", err)
	}
	got, err := decodeRouteGeometry(encoded)
	if err != nil {
		t.Fatalf("decodeRouteGeometry() error = %v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("decoded points = %d, want %d", len(got), len(want))
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("decoded point %d = %#v, want %#v", index, got[index], want[index])
		}
	}
}

func TestRouteGeometryRejectsInvalidProviderData(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		value []byte
	}{
		{name: "invalid json", value: []byte(`{`)},
		{name: "wrong geometry type", value: []byte(`{"type":"Point","coordinates":[14.2,48.2]}`)},
		{name: "missing coordinate", value: []byte(`{"type":"LineString","coordinates":[[14.2,48.2],[14.3]]}`)},
		{name: "out of range", value: []byte(`{"type":"LineString","coordinates":[[14.2,48.2],[14.3,148.3]]}`)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if _, err := decodeRouteGeometry(test.value); !errors.Is(err, planning.ErrValidation) {
				t.Fatalf("decodeRouteGeometry() error = %v, want validation", err)
			}
		})
	}
}

func TestRouteMetricConversionsRejectOverflowAndRoundProviderDurations(t *testing.T) {
	t.Parallel()

	if _, err := nonnegativeInt32(math.MaxInt32 + 1); !errors.Is(err, planning.ErrValidation) {
		t.Fatalf("nonnegativeInt32() error = %v, want validation", err)
	}
	if got, err := durationSeconds(time.Second + 600*time.Millisecond); err != nil || got != 2 {
		t.Fatalf("durationSeconds() = %d, %v, want 2, nil", got, err)
	}
	if got, err := durationSeconds(time.Second + 100*time.Millisecond); err != nil || got != 2 {
		t.Fatalf("durationSeconds() = %d, %v, want 2, nil", got, err)
	}
	if got, err := durationSeconds(90 * time.Second); err != nil || got != 90 {
		t.Fatalf("durationSeconds() = %d, %v, want 90, nil", got, err)
	}
	distance, duration, err := routeMetrics(planning.RouteDirections{
		DistanceMeters: 123,
		Duration:       2*time.Second + 200*time.Millisecond,
		Legs: []planning.RouteLeg{
			{Duration: time.Second + 100*time.Millisecond},
			{Duration: time.Second + 100*time.Millisecond},
		},
	})
	if err != nil || distance != 123 || duration != 4 {
		t.Fatalf("routeMetrics() = %d, %d, %v, want 123, 4, nil", distance, duration, err)
	}
}
