//go:build integration

package integration_test

import (
	"errors"
	"strings"
	"testing"

	"example.invalid/hackplan/internal/adapters/postgres"
	"example.invalid/hackplan/internal/routelocation"
)

func TestRouteLocationsEnforceDefaultsVersionsAndRedactedAudit(t *testing.T) {
	fixture := newCalendarFixture(t)
	if _, err := fixture.pool.Exec(fixture.ctx, "DELETE FROM route_locations"); err != nil {
		t.Fatal(err)
	}
	service, err := routelocation.New(postgres.NewRouteLocationStore(fixture.pool))
	if err != nil {
		t.Fatal(err)
	}
	first, err := service.Create(fixture.ctx, fixture.admin, routelocation.Input{
		Label: "Betriebshof Nord", Address: "Hofgasse 1, 4710 Grieskirchen", Latitude: 48.235, Longitude: 13.986, DefaultStart: true,
	}, "route-location-first")
	if err != nil || !first.DefaultStart || first.Version != 1 {
		t.Fatalf("first Create() = %#v, %v", first, err)
	}
	second, err := service.Create(fixture.ctx, fixture.admin, routelocation.Input{
		Label: "Betriebshof Süd", Address: "Wiesenweg 2, 4710 Grieskirchen", Latitude: 48.236, Longitude: 13.987, DefaultStart: true, DefaultEnd: true,
	}, "route-location-second")
	if err != nil || !second.DefaultStart || !second.DefaultEnd {
		t.Fatalf("second Create() = %#v, %v", second, err)
	}
	locations, err := service.List(fixture.ctx, fixture.admin)
	if err != nil || len(locations) != 2 {
		t.Fatalf("List() = %#v, %v", locations, err)
	}
	var defaultStarts, defaultEnds int
	for _, location := range locations {
		if location.DefaultStart {
			defaultStarts++
		}
		if location.DefaultEnd {
			defaultEnds++
		}
	}
	if defaultStarts != 1 || defaultEnds != 1 {
		t.Fatalf("defaults = %d starts, %d ends; want exactly one each", defaultStarts, defaultEnds)
	}
	if _, err := service.Resolve(fixture.ctx, fixture.admin, first.ID, first.Version); !errors.Is(err, routelocation.ErrConflict) {
		t.Fatalf("Resolve(stale default) error = %v, want conflict", err)
	}
	defaultStart, err := postgres.NewRouteLocationStore(fixture.pool).DefaultStart(fixture.ctx)
	if err != nil || defaultStart.ID != second.ID || defaultStart.Label != second.Label {
		t.Fatalf("DefaultStart() = %#v, %v", defaultStart, err)
	}
	if err := service.Deactivate(fixture.ctx, fixture.admin, second.ID, second.Version, true, "route-location-deactivate"); err != nil {
		t.Fatalf("Deactivate() error = %v", err)
	}
	if _, err := postgres.NewRouteLocationStore(fixture.pool).DefaultStart(fixture.ctx); !errors.Is(err, routelocation.ErrNotFound) {
		t.Fatalf("DefaultStart() after deactivation error = %v, want not found", err)
	}
	var metadata string
	if err := fixture.pool.QueryRow(fixture.ctx, "SELECT metadata::text FROM audit_events WHERE action='route_location.created' ORDER BY id DESC LIMIT 1").Scan(&metadata); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(metadata, "Hofgasse") || strings.Contains(metadata, "Wiesenweg") || strings.Contains(metadata, "48.236") {
		t.Fatalf("route location audit includes a location snapshot: %s", metadata)
	}
}
