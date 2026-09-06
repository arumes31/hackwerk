package planning

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"example.invalid/hackplan/internal/auth"
)

type planningStoreStub struct {
	snapshot       Snapshot
	listRun        Run
	clusters       []ClusterEntry
	loadedJobID    string
	saved          []Suggestion
	adoptedID      string
	adoptedRequest string
}

func (store *planningStoreStub) LoadSnapshot(_ context.Context, jobID string, _, _ time.Time) (Snapshot, error) {
	store.loadedJobID = jobID
	return store.snapshot, nil
}

func (store *planningStoreStub) SaveRun(_ context.Context, _ auth.Actor, _ Snapshot, _, _ time.Time, values []Suggestion, _ Config) (Run, error) {
	store.saved = append([]Suggestion(nil), values...)
	return Run{ID: "run", JobID: store.snapshot.Job.ID, Suggestions: values}, nil
}

func (store *planningStoreStub) ListRun(_ context.Context, _ string) (Run, error) {
	return store.listRun, nil
}

func (store *planningStoreStub) Adopt(_ context.Context, _ auth.Actor, suggestionID, requestID string) (string, error) {
	store.adoptedID, store.adoptedRequest = suggestionID, requestID
	return "appointment", nil
}

func (store *planningStoreStub) ClusterEntries(context.Context) ([]ClusterEntry, error) {
	return append([]ClusterEntry(nil), store.clusters...), nil
}

type planningAvailabilityStub struct {
	intervals []Interval
	resolved  []string
}

func (availability *planningAvailabilityStub) Resolve(_ context.Context, _ auth.Actor, driverID string, _, _ time.Time) ([]Interval, error) {
	availability.resolved = append(availability.resolved, driverID)
	return append([]Interval(nil), availability.intervals...), nil
}

type planningObserverStub struct {
	calls, candidates int
	fallback          bool
}

func (observer *planningObserverStub) ObservePlanning(_ time.Duration, candidates int, fallback bool) {
	observer.calls++
	observer.candidates, observer.fallback = candidates, fallback
}

func TestPlanningServiceSuggestionAndReadActions(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 1, 5, 1, 0, 0, time.UTC)
	snapshot := testSnapshot(now)
	store := &planningStoreStub{
		snapshot: snapshot,
		listRun:  Run{ID: "run"},
		clusters: []ClusterEntry{
			{JobID: "one", Region: "Linz", Location: Point{Latitude: 48.3, Longitude: 14.3}},
			{JobID: "two", Region: "Linz", Location: Point{Latitude: 48.301, Longitude: 14.301}},
			{JobID: "three", Region: "Linz", Location: Point{Latitude: 48.302, Longitude: 14.302}},
		},
	}
	availability := &planningAvailabilityStub{intervals: []Interval{{StartsAt: now.Add(-time.Hour), EndsAt: now.AddDate(0, 0, 8), Status: "available"}}}
	observer := &planningObserverStub{}
	service, err := New(store, availability, NewHaversineRouter(1.3, 55), testConfig(t), func() time.Time { return now }, WithDefaultStartProvider(defaultStartFake{point: Point{Latitude: 48.2, Longitude: 14.2}}), WithObserver(observer))
	if err != nil {
		t.Fatal(err)
	}

	actor := routeAdmin()
	run, err := service.Suggest(t.Context(), actor, " job ")
	if err != nil || run.ID != "run" || len(store.saved) == 0 {
		t.Fatalf("Suggest() run/error/saved = %#v/%v/%d", run, err, len(store.saved))
	}
	if store.loadedJobID != "job" || !reflect.DeepEqual(availability.resolved, []string{"b-driver", "a-driver"}) {
		t.Fatalf("loaded/resolved = %q/%v", store.loadedJobID, availability.resolved)
	}
	if observer.calls != 1 || observer.candidates != len(store.saved) || observer.fallback {
		t.Fatalf("observer = %#v", observer)
	}

	listed, err := service.ListRun(t.Context(), actor, " run ")
	if err != nil || listed.ID != "run" {
		t.Fatalf("ListRun() = %#v, %v", listed, err)
	}
	appointmentID, err := service.Adopt(t.Context(), actor, "suggestion", "request")
	if err != nil || appointmentID != "appointment" || store.adoptedID != "suggestion" || store.adoptedRequest != "request" {
		t.Fatalf("Adopt() result/state = %q/%v/%#v", appointmentID, err, store)
	}
	hints, err := service.ClusterHints(t.Context(), actor)
	if err != nil || len(hints) != 1 || hints[0].Count != 3 {
		t.Fatalf("ClusterHints() = %#v, %v", hints, err)
	}
}

func TestPlanningServiceRejectsUnauthorizedAndInvalidActions(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 1, 5, 1, 0, 0, time.UTC)
	service, err := New(&planningStoreStub{snapshot: testSnapshot(now)}, &planningAvailabilityStub{}, NewHaversineRouter(1.3, 55), testConfig(t), func() time.Time { return now }, WithDefaultStartProvider(defaultStartFake{point: Point{Latitude: 48.2, Longitude: 14.2}}))
	if err != nil {
		t.Fatal(err)
	}
	unauthorized := auth.Actor{UserID: "no-role"}
	if _, err := service.Suggest(t.Context(), unauthorized, "job"); !errors.Is(err, auth.ErrForbidden) {
		t.Fatalf("Suggest() error = %v", err)
	}
	if _, err := service.Suggest(t.Context(), routeAdmin(), "  "); !errors.Is(err, ErrValidation) {
		t.Fatalf("Suggest() empty ID error = %v", err)
	}
	if _, err := service.ListRun(t.Context(), unauthorized, "run"); !errors.Is(err, auth.ErrForbidden) {
		t.Fatalf("ListRun() error = %v", err)
	}
	if _, err := service.Adopt(t.Context(), routeAdmin(), " ", "request"); !errors.Is(err, ErrValidation) {
		t.Fatalf("Adopt() empty ID error = %v", err)
	}
	if _, err := service.ClusterHints(t.Context(), unauthorized); !errors.Is(err, auth.ErrForbidden) {
		t.Fatalf("ClusterHints() error = %v", err)
	}
}

func TestPlanningConfigurationAndHelpers(t *testing.T) {
	t.Parallel()
	cfg := testConfig(t)
	invalid := cfg
	invalid.SlotMinutes = 7
	if !errors.Is(invalid.Validate(), ErrValidation) {
		t.Fatalf("Validate() = %v", invalid.Validate())
	}
	invalid = cfg
	invalid.Weights.Travel = -1
	if !errors.Is(invalid.Validate(), ErrValidation) {
		t.Fatalf("negative weight Validate() = %v", invalid.Validate())
	}

	now := time.Date(2026, 9, 1, 5, 1, 1, 0, time.UTC)
	if got := ceilTime(now, 15*time.Minute); !got.Equal(time.Date(2026, 9, 1, 5, 15, 0, 0, time.UTC)) {
		t.Fatalf("ceilTime() = %s", got)
	}
	first, configJSON, err := Fingerprint(testSnapshot(now), cfg)
	if err != nil || len(first) != 32 || !strings.Contains(string(configJSON), "haversine") {
		t.Fatalf("Fingerprint() = %x/%s/%v", first, configJSON, err)
	}
	second, _, err := Fingerprint(testSnapshot(now), cfg)
	if err != nil || !reflect.DeepEqual(first, second) {
		t.Fatalf("Fingerprint is not deterministic: %x/%x/%v", first, second, err)
	}
	choices := selectResourceChoices(Job{Type: "chipping_with_transport", TransportMode: "external", ExternalTransportConfirmed: true}, []Resource{{ID: "b", Name: "B", Type: "chipper"}, {ID: "a", Name: "A", Type: "chipper"}})
	if len(choices) != 2 || !reflect.DeepEqual(choices[0].ids, []string{"a"}) {
		t.Fatalf("external choices = %#v", choices)
	}
	if choices := selectResourceChoices(Job{Type: "chipping_with_transport", TransportMode: "external"}, []Resource{{ID: "a", Type: "chipper"}}); choices != nil {
		t.Fatalf("unconfirmed external choices = %#v", choices)
	}
}

func TestRouteReadOperationsAndHelpers(t *testing.T) {
	t.Parallel()
	store := &routeStoreFake{
		candidates: []RouteCandidate{{JobID: "job", Location: Point{Latitude: 48.2, Longitude: 14.2}, WorkDuration: time.Hour, JobVersion: 1, WaitlistVersion: 1}},
		route:      assignedRouteFixture(),
	}
	service := newRouteTestService(t, store)
	if _, err := service.Candidates(t.Context(), routeDriver("driver")); !errors.Is(err, auth.ErrForbidden) {
		t.Fatalf("Candidates() error = %v", err)
	}
	values, err := service.Candidates(t.Context(), routeAdmin())
	if err != nil || len(values) != 1 {
		t.Fatalf("Candidates() = %#v, %v", values, err)
	}
	if _, err := service.Route(t.Context(), routeAdmin(), " "); !errors.Is(err, ErrValidation) {
		t.Fatalf("Route() blank ID error = %v", err)
	}
	if route, err := service.Route(t.Context(), routeAdmin(), "route"); err != nil || route.ID != "route" {
		t.Fatalf("Route() = %#v, %v", route, err)
	}
	store.route.Status = RouteStatusDraft
	if _, err := service.DraftsForDate(t.Context(), routeAdmin(), "bad-date"); !errors.Is(err, ErrValidation) {
		t.Fatalf("DraftsForDate() invalid error = %v", err)
	}
	drafts, err := service.DraftsForDate(t.Context(), routeAdmin(), "2026-09-01")
	if err != nil || len(drafts) != 1 || drafts[0].ID != "route" {
		t.Fatalf("DraftsForDate() = %#v, %v", drafts, err)
	}
	if got := routeCandidateLabel(RouteCandidate{JobNumber: " HA-1 ", Locality: " Linz "}); got != "HA-1 · Linz" {
		t.Fatalf("routeCandidateLabel() = %q", got)
	}
	if got := routeCandidateLabel(RouteCandidate{}); got != "Letzter Stopp" {
		t.Fatalf("empty routeCandidateLabel() = %q", got)
	}
	if next := (RouteDraft{Stops: []RouteStop{{ID: "done", EndsAt: time.Now().Add(-time.Hour)}}}).NextStop(time.Now()); next != nil {
		t.Fatalf("NextStop() = %#v", next)
	}
}

func TestRouteAndRouterValidationHelpers(t *testing.T) {
	t.Parallel()
	if _, err := NewRouteService(nil, NewHaversineRouter(1, 1), NewHaversineRouter(1, 1), DefaultRouteConfig()); !errors.Is(err, ErrValidation) {
		t.Fatalf("NewRouteService() error = %v", err)
	}
	if err := validateRouteMatrix(Matrix{Source: "x", Cells: [][]MatrixCell{{{DistanceMeters: -1}}}}, 1); !errors.Is(err, ErrValidation) {
		t.Fatalf("validateRouteMatrix() error = %v", err)
	}
	if err := validateRouteDirections(RouteDirections{Source: "x", Geometry: []Point{{Latitude: 48.2, Longitude: 14.2}}, Legs: nil}, 2); !errors.Is(err, ErrValidation) {
		t.Fatalf("validateRouteDirections() error = %v", err)
	}
	if router := NewCachedRouter(nil, 0, 0); router != nil {
		t.Fatalf("NewCachedRouter(nil) = %#v", router)
	}
	cached := NewCachedRouter(NewHaversineRouter(1.3, 55), 0, 0)
	directions, err := cached.(DirectionsRouter).Directions(t.Context(), []Point{{Latitude: 48.2, Longitude: 14.2}, {Latitude: 48.3, Longitude: 14.3}})
	if err != nil || directions.Source != "haversine" {
		t.Fatalf("CachedRouter.Directions() = %#v, %v", directions, err)
	}
	if _, err := (FallbackRouter{}).Matrix(t.Context(), []Point{{Latitude: 48.2, Longitude: 14.2}, {Latitude: 48.3, Longitude: 14.3}}); err == nil {
		t.Fatal("FallbackRouter without providers succeeded")
	}
}
