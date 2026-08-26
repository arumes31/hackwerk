package planning

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"example.invalid/hackplan/internal/auth"
)

type routeStoreFake struct {
	candidates      []RouteCandidate
	missing         []RouteMissingLocation
	options         RouteOptions
	route           RouteDraft
	routes          map[string]RouteDraft
	moved           SaveMovedDraftStopInput
	savedDraft      SaveRouteDraftInput
	savedOrder      SaveRouteOrderInput
	assigned        AssignRouteInput
	latestDriverID  string
	latestLocalDate string
	draftSaves      int
	orderSaves      int
	assignmentSaves int
}

func (f *routeStoreFake) LoadRouteCandidates(context.Context, []string) ([]RouteCandidate, error) {
	return append([]RouteCandidate(nil), f.candidates...), nil
}

func (f *routeStoreFake) LoadRouteMissingLocations(context.Context) ([]RouteMissingLocation, error) {
	return append([]RouteMissingLocation(nil), f.missing...), nil
}

func (f *routeStoreFake) LoadRouteOptions(context.Context) (RouteOptions, error) {
	return f.options, nil
}

func (f *routeStoreFake) SaveRouteDraft(_ context.Context, _ auth.Actor, input SaveRouteDraftInput) (RouteDraft, error) {
	f.draftSaves++
	f.savedDraft = input
	input.Route.ID = "route-1"
	input.Route.Version = 1
	return input.Route, nil
}

func (f *routeStoreFake) GetRoute(_ context.Context, id string) (RouteDraft, error) {
	if f.routes != nil {
		if route, ok := f.routes[id]; ok {
			return route, nil
		}
		return RouteDraft{}, ErrNotFound
	}
	return f.route, nil
}

func (f *routeStoreFake) LatestAssignedRouteForDriver(_ context.Context, driverID, localDate string) (RouteDraft, error) {
	f.latestDriverID = driverID
	f.latestLocalDate = localDate
	return f.route, nil
}

func (f *routeStoreFake) AssignRoute(_ context.Context, _ auth.Actor, input AssignRouteInput) (RouteDraft, error) {
	f.assignmentSaves++
	f.assigned = input
	result := f.route
	result.Status = RouteStatusAssigned
	result.Version++
	return result, nil
}

func (f *routeStoreFake) SaveRouteOrder(_ context.Context, _ auth.Actor, input SaveRouteOrderInput) (RouteDraft, error) {
	f.orderSaves++
	f.savedOrder = input
	input.Route.Version++
	return input.Route, nil
}

func (f *routeStoreFake) ListDraftRouteIDsForDate(context.Context, string) ([]string, error) {
	if f.route.ID == "" || f.route.Status != RouteStatusDraft {
		return nil, nil
	}
	return []string{f.route.ID}, nil
}

func (f *routeStoreFake) SaveMovedDraftStop(_ context.Context, _ auth.Actor, input SaveMovedDraftStopInput) error {
	f.moved = input
	if f.routes != nil {
		input.Source.Version++
		input.Target.Version++
		f.routes[input.Source.ID] = input.Source
		f.routes[input.Target.ID] = input.Target
	}
	return nil
}

func TestRouteServiceMovesDraftStopBetweenRoutes(t *testing.T) {
	t.Parallel()
	source := assignedRouteFixture()
	source.ID, source.Status, source.Version = "source", RouteStatusDraft, 2
	target := source
	target.ID, target.DriverID, target.Version = "target", "driver-2", 5
	target.Stops = append([]RouteStop(nil), source.Stops[:1]...)
	target.Stops[0].ID, target.Stops[0].JobID = "target-stop", "target-job"
	store := &routeStoreFake{routes: map[string]RouteDraft{"source": source, "target": target}}
	service := newRouteTestService(t, store)
	values, err := service.MoveDraftStop(t.Context(), routeAdmin(), MoveDraftStopInput{SourceRouteID: "source", TargetRouteID: "target", StopID: "stop-b", SourceVersion: 2, TargetVersion: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(values) != 2 || len(store.moved.Source.Stops) != 1 || len(store.moved.Target.Stops) != 2 || store.moved.Target.Stops[1].JobID != "job-b" {
		t.Fatalf("move result = %#v, saved=%#v", values, store.moved)
	}
	if store.moved.Source.Stops[0].Position != 1 || store.moved.Target.Stops[1].Position != 2 {
		t.Fatalf("positions not normalized: %#v", store.moved)
	}
}

type matrixRouterFunc func(context.Context, []Point) (Matrix, error)

func (f matrixRouterFunc) Matrix(ctx context.Context, points []Point) (Matrix, error) {
	return f(ctx, points)
}

type directionsRouterFunc func(context.Context, []Point) (RouteDirections, error)

func (f directionsRouterFunc) Directions(ctx context.Context, points []Point) (RouteDirections, error) {
	return f(ctx, points)
}

func TestRouteServiceOptionsRequireAdminPlanningPermission(t *testing.T) {
	t.Parallel()
	store := &routeStoreFake{options: RouteOptions{Drivers: []RouteDriverOption{{ID: "driver", Name: "Anna"}}}}
	service := newRouteTestService(t, store)

	if _, err := service.Options(t.Context(), routeDriver("driver")); !errors.Is(err, auth.ErrForbidden) {
		t.Fatalf("driver Options() error = %v, want forbidden", err)
	}
	options, err := service.Options(t.Context(), routeAdmin())
	if err != nil || len(options.Drivers) != 1 {
		t.Fatalf("admin Options() = %#v, %v", options, err)
	}
}

func TestRouteServiceMissingLocationsRequireAdminPlanningPermission(t *testing.T) {
	t.Parallel()
	store := &routeStoreFake{missing: []RouteMissingLocation{{JobID: "job", JobNumber: "HA-1"}}}
	service := newRouteTestService(t, store)

	if _, err := service.MissingLocations(t.Context(), routeDriver("driver")); !errors.Is(err, auth.ErrForbidden) {
		t.Fatalf("driver MissingLocations() error = %v, want forbidden", err)
	}
	items, err := service.MissingLocations(t.Context(), routeAdmin())
	if err != nil || len(items) != 1 || items[0].JobID != "job" {
		t.Fatalf("admin MissingLocations() = %#v, %v", items, err)
	}
}

func TestRouteServicePlanOptimizesDeterministicallyAndBuildsTimeline(t *testing.T) {
	t.Parallel()
	departure := time.Date(2026, 9, 1, 6, 0, 0, 0, time.UTC)
	store := &routeStoreFake{candidates: []RouteCandidate{
		{JobID: "job-b", JobType: "chipping_only", Location: Point{Latitude: 48.3, Longitude: 14.3}, WorkDuration: 45 * time.Minute, JobVersion: 2, WaitlistVersion: 3},
		{JobID: "job-a", JobType: "chipping_only", Location: Point{Latitude: 48.2, Longitude: 14.2}, WorkDuration: time.Hour, JobVersion: 4, WaitlistVersion: 5},
	}}
	matrix := matrixRouterFunc(func(_ context.Context, points []Point) (Matrix, error) {
		cells := make([][]MatrixCell, len(points))
		for row := range cells {
			cells[row] = make([]MatrixCell, len(points))
			for column := range cells[row] {
				cells[row][column] = MatrixCell{DistanceMeters: 1000, Duration: 10 * time.Minute}
			}
		}
		cells[0][1] = MatrixCell{DistanceMeters: 2000, Duration: 20 * time.Minute}
		cells[0][2] = MatrixCell{DistanceMeters: 1000, Duration: 10 * time.Minute}
		return Matrix{Cells: cells, Source: "fake"}, nil
	})
	directions := directionsRouterFunc(func(_ context.Context, points []Point) (RouteDirections, error) {
		duration := 15 * time.Minute
		if len(points) > 1 && points[1].Latitude == 48.3 {
			duration = 25 * time.Minute
		}
		return testDirections(points, duration), nil
	})
	service, err := NewRouteService(store, matrix, directions, DefaultRouteConfig())
	if err != nil {
		t.Fatal(err)
	}

	route, err := service.Plan(t.Context(), routeAdmin(), PlanRouteInput{
		Departure: departure, DriverID: "driver", ChipperResourceID: "chipper",
		Start: Point{Latitude: 48.1, Longitude: 14.1}, End: Point{Latitude: 48.1, Longitude: 14.1},
		JobIDs: []string{"job-b", "job-a"}, Optimize: true, RequestID: "request",
	})
	if err != nil {
		t.Fatal(err)
	}
	if route.ID != "route-1" || store.draftSaves != 1 {
		t.Fatalf("route/save = %#v/%d", route, store.draftSaves)
	}
	if got := []string{route.Stops[0].JobID, route.Stops[1].JobID}; !reflect.DeepEqual(got, []string{"job-a", "job-b"}) {
		t.Fatalf("optimized order = %v", got)
	}
	if !route.Stops[0].StartsAt.Equal(departure.Add(15*time.Minute)) || !route.Stops[0].EndsAt.Equal(departure.Add(75*time.Minute)) {
		t.Fatalf("first timeline = %s-%s", route.Stops[0].StartsAt, route.Stops[0].EndsAt)
	}
	wantEnd := departure.Add(15*time.Minute + time.Hour + 15*time.Minute + 45*time.Minute + 15*time.Minute)
	if !route.EstimatedEndAt.Equal(wantEnd) {
		t.Fatalf("EstimatedEndAt = %s, want %s", route.EstimatedEndAt, wantEnd)
	}
	if route.Comparison.ManualDuration <= route.Comparison.OptimizedDuration {
		t.Fatalf("comparison = %#v, want optimized duration below manual", route.Comparison)
	}
}

func TestRouteServiceOptimizeKeepsFixedCandidateAtItsPosition(t *testing.T) {
	t.Parallel()
	candidates := []RouteCandidate{
		{JobID: "job-a", Location: Point{Latitude: 48.1, Longitude: 14.1}},
		{JobID: "job-b", Location: Point{Latitude: 48.2, Longitude: 14.2}},
		{JobID: "job-c", Location: Point{Latitude: 48.3, Longitude: 14.3}},
	}
	matrix := matrixRouterFunc(func(_ context.Context, points []Point) (Matrix, error) {
		cells := make([][]MatrixCell, len(points))
		for row := range cells {
			cells[row] = make([]MatrixCell, len(points))
			for column := range cells[row] {
				cells[row][column] = MatrixCell{DistanceMeters: 1000, Duration: 10 * time.Minute}
			}
		}
		cells[0][3] = MatrixCell{DistanceMeters: 100, Duration: time.Minute}
		cells[0][1] = MatrixCell{DistanceMeters: 900, Duration: 9 * time.Minute}
		return Matrix{Cells: cells, Source: "fake"}, nil
	})
	service, err := NewRouteService(&routeStoreFake{}, matrix, directionsRouterFunc(func(_ context.Context, points []Point) (RouteDirections, error) {
		return testDirections(points, time.Minute), nil
	}), DefaultRouteConfig())
	if err != nil {
		t.Fatal(err)
	}

	ordered, err := service.optimize(t.Context(), Point{Latitude: 48, Longitude: 14}, Point{Latitude: 49, Longitude: 15}, candidates, []string{"job-b"})
	if err != nil {
		t.Fatal(err)
	}
	if got := []string{ordered[0].JobID, ordered[1].JobID, ordered[2].JobID}; !reflect.DeepEqual(got, []string{"job-c", "job-b", "job-a"}) {
		t.Fatalf("fixed order = %v", got)
	}
}

func TestRouteServicePlanCanEndAtLastStop(t *testing.T) {
	t.Parallel()
	input := validPlanRouteInput()
	last := Point{Latitude: 48.25, Longitude: 14.25}
	store := &routeStoreFake{candidates: []RouteCandidate{{
		JobID: input.JobIDs[0], JobType: "chipping_only", Location: last,
		WorkDuration: time.Hour, JobVersion: 1, WaitlistVersion: 1,
	}}}
	service := newRouteTestService(t, store)
	input.EndAtLastStop = true
	route, err := service.Plan(t.Context(), routeAdmin(), input)
	if err != nil {
		t.Fatal(err)
	}
	if route.End != last {
		t.Fatalf("route end = %#v, want last stop %#v", route.End, last)
	}
}

func TestRouteDraftNextStopSkipsCompletedStops(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	route := RouteDraft{Stops: []RouteStop{
		{ID: "past", EndsAt: now.Add(-time.Minute)},
		{ID: "current", EndsAt: now.Add(time.Hour)},
		{ID: "future", EndsAt: now.Add(2 * time.Hour)},
	}}
	if next := route.NextStop(now); next == nil || next.ID != "current" {
		t.Fatalf("NextStop() = %#v, want current", next)
	}
}

func TestRouteServicePlanRejectsDriverAndIncompleteCandidates(t *testing.T) {
	t.Parallel()
	store := &routeStoreFake{}
	service := newRouteTestService(t, store)
	input := validPlanRouteInput()
	if _, err := service.Plan(t.Context(), routeDriver("driver"), input); !errors.Is(err, auth.ErrForbidden) {
		t.Fatalf("driver Plan() error = %v", err)
	}
	if store.draftSaves != 0 {
		t.Fatalf("forbidden plan reached store: %d", store.draftSaves)
	}
	store.candidates = []RouteCandidate{{JobID: input.JobIDs[0], JobType: "chipping_only", WorkDuration: time.Hour, JobVersion: 1, WaitlistVersion: 1}}
	if _, err := service.Plan(t.Context(), routeAdmin(), input); !errors.Is(err, ErrValidation) {
		t.Fatalf("invalid candidate Plan() error = %v", err)
	}
	store.candidates[0].Location = Point{Latitude: 48.2, Longitude: 14.2}
	store.candidates[0].UnavailableReason = "Bereits eingeplant"
	if _, err := service.Plan(t.Context(), routeAdmin(), input); !errors.Is(err, ErrConflict) {
		t.Fatalf("unavailable candidate Plan() error = %v", err)
	}
}

func TestRouteServicePlanRequiresResolvedTransport(t *testing.T) {
	t.Parallel()
	input := validPlanRouteInput()
	tests := []struct {
		name      string
		candidate RouteCandidate
		transport string
		wantErr   bool
	}{
		{name: "internal without vehicle", candidate: RouteCandidate{JobType: "chipping_with_transport", TransportMode: "internal"}, wantErr: true},
		{name: "internal with vehicle", candidate: RouteCandidate{JobType: "chipping_with_transport", TransportMode: "internal"}, transport: "transport-1"},
		{name: "external unconfirmed", candidate: RouteCandidate{JobType: "chipping_with_transport", TransportMode: "external"}, wantErr: true},
		{name: "external confirmed", candidate: RouteCandidate{JobType: "chipping_with_transport", TransportMode: "external", ExternalTransportConfirmed: true}},
		{name: "undecided", candidate: RouteCandidate{JobType: "chipping_with_transport", TransportMode: "undecided"}, wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := test.candidate
			candidate.JobID, candidate.Location = input.JobIDs[0], Point{Latitude: 48.2, Longitude: 14.2}
			candidate.WorkDuration, candidate.JobVersion, candidate.WaitlistVersion = time.Hour, 1, 1
			store := &routeStoreFake{candidates: []RouteCandidate{candidate}}
			service := newRouteTestService(t, store)
			request := input
			request.TransportResourceID = test.transport
			_, err := service.Plan(t.Context(), routeAdmin(), request)
			if test.wantErr != errors.Is(err, ErrValidation) {
				t.Fatalf("Plan() error=%v want validation=%v", err, test.wantErr)
			}
		})
	}
}

func TestRouteServiceAssignIsAdminOnlyAndDraftOnly(t *testing.T) {
	t.Parallel()
	store := &routeStoreFake{route: assignedRouteFixture()}
	store.route.Status = RouteStatusDraft
	service := newRouteTestService(t, store)
	input := AssignRouteInput{ID: store.route.ID, ExpectedVersion: store.route.Version, RequestID: "assign"}
	if _, err := service.Assign(t.Context(), routeDriver(store.route.DriverID), input); !errors.Is(err, auth.ErrForbidden) {
		t.Fatalf("driver Assign() error = %v", err)
	}
	if _, err := service.Assign(t.Context(), routeAdmin(), input); err != nil {
		t.Fatal(err)
	}
	if store.assignmentSaves != 1 || store.assigned.ID != store.route.ID {
		t.Fatalf("assign calls/input = %d/%#v", store.assignmentSaves, store.assigned)
	}
	store.route.Status = RouteStatusAssigned
	if _, err := service.Assign(t.Context(), routeAdmin(), input); !errors.Is(err, ErrConflict) {
		t.Fatalf("assigned route Assign() error = %v", err)
	}
}

func TestRouteServiceOwnRouteScopesToSessionDriver(t *testing.T) {
	t.Parallel()
	store := &routeStoreFake{route: assignedRouteFixture()}
	service := newRouteTestService(t, store)
	if _, err := service.OwnRoute(t.Context(), routeDriver("other"), store.route.ID); !errors.Is(err, auth.ErrForbidden) {
		t.Fatalf("foreign OwnRoute() error = %v", err)
	}
	if _, err := service.OwnRoute(t.Context(), auth.Actor{UserID: "user", Role: auth.RoleDriver}, store.route.ID); !errors.Is(err, auth.ErrForbidden) {
		t.Fatalf("profileless OwnRoute() error = %v", err)
	}
	if _, err := service.OwnRoute(t.Context(), routeDriver(store.route.DriverID), store.route.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.OwnRouteForDate(t.Context(), routeDriver(store.route.DriverID), "2026-09-01"); err != nil {
		t.Fatal(err)
	}
	if store.latestDriverID != store.route.DriverID || store.latestLocalDate != "2026-09-01" {
		t.Fatalf("latest route scope = %q/%q", store.latestDriverID, store.latestLocalDate)
	}
}

func TestRouteServiceReorderOwnPreservesAppointmentsAndTimes(t *testing.T) {
	t.Parallel()
	store := &routeStoreFake{route: assignedRouteFixture()}
	before := make(map[string]RouteStop, len(store.route.Stops))
	for _, stop := range store.route.Stops {
		before[stop.ID] = stop
	}
	service := newRouteTestService(t, store)
	order := []string{store.route.Stops[1].ID, store.route.Stops[0].ID}
	result, err := service.ReorderOwn(t.Context(), routeDriver(store.route.DriverID), ReorderOwnRouteInput{
		ID: store.route.ID, ExpectedVersion: store.route.Version, StopIDs: order, RequestID: "reorder",
	})
	if err != nil {
		t.Fatal(err)
	}
	if store.orderSaves != 1 || !reflect.DeepEqual(store.savedOrder.StopIDs, order) {
		t.Fatalf("order save = %d/%v", store.orderSaves, store.savedOrder.StopIDs)
	}
	for _, stop := range result.Stops {
		original := before[stop.ID]
		if stop.AppointmentID != original.AppointmentID || !stop.StartsAt.Equal(original.StartsAt) || !stop.EndsAt.Equal(original.EndsAt) {
			t.Fatalf("appointment mutation for %s: before=%#v after=%#v", stop.ID, original, stop)
		}
	}
	if result.Stops[0].ID != order[0] || result.Stops[0].Position != 1 || result.Stops[0].LegDuration <= 0 {
		t.Fatalf("reordered route = %#v", result.Stops)
	}
}

func TestRouteServiceReorderOwnRejectsForeignStaleAndInvalidOrder(t *testing.T) {
	t.Parallel()
	store := &routeStoreFake{route: assignedRouteFixture()}
	service := newRouteTestService(t, store)
	input := ReorderOwnRouteInput{ID: store.route.ID, ExpectedVersion: store.route.Version, StopIDs: []string{"stop-b", "stop-a"}}
	if _, err := service.ReorderOwn(t.Context(), routeDriver("other"), input); !errors.Is(err, auth.ErrForbidden) {
		t.Fatalf("foreign reorder error = %v", err)
	}
	input.ExpectedVersion--
	if _, err := service.ReorderOwn(t.Context(), routeDriver(store.route.DriverID), input); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale reorder error = %v", err)
	}
	input.ExpectedVersion = store.route.Version
	input.StopIDs = []string{"stop-a", "stop-a"}
	if _, err := service.ReorderOwn(t.Context(), routeDriver(store.route.DriverID), input); !errors.Is(err, ErrValidation) {
		t.Fatalf("duplicate reorder error = %v", err)
	}
	if store.orderSaves != 0 {
		t.Fatalf("invalid reorder reached store: %d", store.orderSaves)
	}
}

func newRouteTestService(t *testing.T, store *routeStoreFake) *RouteService {
	t.Helper()
	matrix := matrixRouterFunc(func(_ context.Context, points []Point) (Matrix, error) {
		cells := make([][]MatrixCell, len(points))
		for row := range cells {
			cells[row] = make([]MatrixCell, len(points))
		}
		return Matrix{Cells: cells, Source: "fake"}, nil
	})
	directions := directionsRouterFunc(func(_ context.Context, points []Point) (RouteDirections, error) {
		return testDirections(points, 10*time.Minute), nil
	})
	service, err := NewRouteService(store, matrix, directions, DefaultRouteConfig())
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func testDirections(points []Point, duration time.Duration) RouteDirections {
	result := RouteDirections{Geometry: append([]Point(nil), points...), Source: "fake", FreshAt: time.Now().UTC()}
	for range len(points) - 1 {
		result.Legs = append(result.Legs, RouteLeg{DistanceMeters: 1000, Duration: duration})
		result.DistanceMeters += 1000
		result.Duration += duration
	}
	return result
}

func validPlanRouteInput() PlanRouteInput {
	return PlanRouteInput{
		Departure: time.Date(2026, 9, 1, 6, 0, 0, 0, time.UTC), DriverID: "driver", ChipperResourceID: "chipper",
		Start: Point{Latitude: 48.1, Longitude: 14.1}, End: Point{Latitude: 48.1, Longitude: 14.1}, JobIDs: []string{"job-a"},
	}
}

func assignedRouteFixture() RouteDraft {
	firstStart := time.Date(2026, 9, 1, 7, 0, 0, 0, time.UTC)
	secondStart := firstStart.Add(2 * time.Hour)
	return RouteDraft{
		ID: "route", DriverID: "driver", ChipperResourceID: "chipper", Status: RouteStatusAssigned, Version: 4,
		Departure: firstStart.Add(-time.Hour), Start: Point{Latitude: 48.1, Longitude: 14.1}, End: Point{Latitude: 48.1, Longitude: 14.1},
		Stops: []RouteStop{
			{ID: "stop-a", JobID: "job-a", AppointmentID: "appointment-a", Position: 1, Location: Point{Latitude: 48.2, Longitude: 14.2}, WorkDuration: time.Hour, StartsAt: firstStart, EndsAt: firstStart.Add(time.Hour), JobVersion: 1, WaitlistVersion: 1},
			{ID: "stop-b", JobID: "job-b", AppointmentID: "appointment-b", Position: 2, Location: Point{Latitude: 48.3, Longitude: 14.3}, WorkDuration: time.Hour, StartsAt: secondStart, EndsAt: secondStart.Add(time.Hour), JobVersion: 1, WaitlistVersion: 1},
		},
	}
}

func routeAdmin() auth.Actor {
	return auth.Actor{UserID: "admin", Role: auth.RoleAdmin}
}

func routeDriver(driverID string) auth.Actor {
	return auth.Actor{UserID: "user", Role: auth.RoleDriver, DriverID: driverID}
}
