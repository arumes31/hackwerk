package web

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"example.invalid/hackplan/internal/auth"
	"example.invalid/hackplan/internal/buildinfo"
	"example.invalid/hackplan/internal/planning"
)

type routeHTTPStore struct {
	candidates []planning.RouteCandidate
	options    planning.RouteOptions
	route      planning.RouteDraft
	savedOrder []string
}

func (store *routeHTTPStore) LoadRouteCandidates(context.Context, []string) ([]planning.RouteCandidate, error) {
	return append([]planning.RouteCandidate(nil), store.candidates...), nil
}

func (store *routeHTTPStore) LoadRouteMissingLocations(context.Context) ([]planning.RouteMissingLocation, error) {
	return []planning.RouteMissingLocation{{JobID: "job-missing", JobNumber: "HA-2026-0043", CustomerName: "Ohne Standort", Region: "Forsttal"}}, nil
}
func (store *routeHTTPStore) LoadRouteOptions(context.Context) (planning.RouteOptions, error) {
	return store.options, nil
}
func (store *routeHTTPStore) SaveRouteDraft(_ context.Context, _ auth.Actor, input planning.SaveRouteDraftInput) (planning.RouteDraft, error) {
	input.Route.ID, input.Route.Version = "route-1", 1
	return input.Route, nil
}
func (store *routeHTTPStore) GetRoute(context.Context, string) (planning.RouteDraft, error) {
	return store.route, nil
}
func (store *routeHTTPStore) LatestAssignedRouteForDriver(context.Context, string, string) (planning.RouteDraft, error) {
	if store.route.ID == "" {
		return planning.RouteDraft{}, planning.ErrNotFound
	}
	return store.route, nil
}
func (store *routeHTTPStore) AssignRoute(context.Context, auth.Actor, planning.AssignRouteInput) (planning.RouteDraft, error) {
	return store.route, nil
}
func (store *routeHTTPStore) SaveRouteOrder(_ context.Context, _ auth.Actor, input planning.SaveRouteOrderInput) (planning.RouteDraft, error) {
	store.savedOrder = append([]string(nil), input.StopIDs...)
	input.Route.Version++
	store.route = input.Route
	return input.Route, nil
}
func (store *routeHTTPStore) ListDraftRouteIDsForDate(context.Context, string) ([]string, error) {
	if store.route.ID == "" || store.route.Status != planning.RouteStatusDraft {
		return nil, nil
	}
	return []string{store.route.ID}, nil
}
func (store *routeHTTPStore) SaveMovedDraftStop(context.Context, auth.Actor, planning.SaveMovedDraftStopInput) error {
	return nil
}

func TestRouteHTTPAdminSeesRoutableJobsAndDriverCannotPlan(t *testing.T) {
	store := routeHTTPFixture()
	adminRouter, adminSession, adminCSRF := routeTestRouter(t, auth.RoleAdmin, "", store)
	response := httptest.NewRecorder()
	adminRouter.ServeHTTP(response, authenticatedCustomerRequest(t, http.MethodGet, "/planning/routes", nil, adminSession, adminCSRF))
	body := response.Body.String()
	if response.Code != http.StatusOK || !strings.Contains(body, "Auftragskarte &amp; Route") ||
		!strings.Contains(body, "HA-2026-0042") || !strings.Contains(body, "data-route-candidate") ||
		!strings.Contains(body, `data-depot-latitude="48.200000"`) ||
		!strings.Contains(body, `data-route-admin="true"`) || !strings.Contains(body, "Aufträge ohne Haufenstandort") {
		t.Fatalf("admin route page=%d %s", response.Code, response.Body.String())
	}

	driverRouter, driverSession, driverCSRF := routeTestRouter(t, auth.RoleDriver, "driver-1", store)
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		var form url.Values
		if method == http.MethodPost {
			form = url.Values{"csrf_token": {driverCSRF}}
		}
		forbidden := httptest.NewRecorder()
		driverRouter.ServeHTTP(forbidden, authenticatedCustomerRequest(t, method, "/planning/routes", form, driverSession, driverCSRF))
		if forbidden.Code != http.StatusForbidden {
			t.Fatalf("driver %s planning status=%d", method, forbidden.Code)
		}
	}
}

func TestRouteHTTPAdminRendersInteractiveBaseMapWithoutCandidates(t *testing.T) {
	store := routeHTTPFixture()
	store.candidates = nil
	router, session, csrf := routeTestRouter(t, auth.RoleAdmin, "", store)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, authenticatedCustomerRequest(t, http.MethodGet, "/planning/routes", nil, session, csrf))
	body := response.Body.String()
	if response.Code != http.StatusOK || !strings.Contains(body, `aria-label="Interaktive Karte der offenen Aufträge"`) ||
		!strings.Contains(body, `data-depot-latitude="48.200000"`) ||
		!strings.Contains(body, "Keine routierbaren Aufträge") {
		t.Fatalf("empty route map page=%d %s", response.Code, body)
	}
}

func TestRouteHTTPAdminSeesScheduledMapPointButCannotSelectIt(t *testing.T) {
	store := routeHTTPFixture()
	store.candidates[0].UnavailableReason = "Bereits eingeplant"
	router, session, csrf := routeTestRouter(t, auth.RoleAdmin, "", store)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, authenticatedCustomerRequest(t, http.MethodGet, "/planning/routes", nil, session, csrf))
	body := response.Body.String()
	if response.Code != http.StatusOK || !strings.Contains(body, `data-route-candidate`) ||
		!strings.Contains(body, `data-unavailable-reason="Bereits eingeplant"`) ||
		!strings.Contains(body, `value="job-1" aria-label="HA-2026-0042 für die Route auswählen" disabled`) ||
		!strings.Contains(body, `type="submit" disabled`) {
		t.Fatalf("scheduled map point page=%d %s", response.Code, body)
	}
}

func TestRouteHTTPDriverCanReorderOnlyOwnAssignedRoute(t *testing.T) {
	store := routeHTTPFixture()
	router, session, csrf := routeTestRouter(t, auth.RoleDriver, "driver-1", store)
	page := httptest.NewRecorder()
	router.ServeHTTP(page, authenticatedCustomerRequest(t, http.MethodGet, "/my-route?date=2026-08-27", nil, session, csrf))
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), "Meine Route") || !strings.Contains(page.Body.String(), "Nur Fahrreihenfolge") ||
		!strings.Contains(page.Body.String(), `data-route-own="true"`) || !strings.Contains(page.Body.String(), `type="submit" name="move_up" value="stop-2"`) ||
		!strings.Contains(page.Body.String(), "data-wake-lock") ||
		!strings.Contains(page.Body.String(), "data-route-navigation") || !strings.Contains(page.Body.String(), "data-route-call") {
		t.Fatalf("own route page=%d %s", page.Code, page.Body.String())
	}
	form := url.Values{"csrf_token": {csrf}, "version": {"1"}, "stop_id": {"stop-1", "stop-2"}, "move_up": {"stop-2"}}
	response := httptest.NewRecorder()
	router.ServeHTTP(response, authenticatedCustomerRequest(t, http.MethodPost, "/my-route/route-1/reorder", form, session, csrf))
	if response.Code != http.StatusSeeOther || strings.Join(store.savedOrder, ",") != "stop-2,stop-1" {
		t.Fatalf("reorder=%d order=%v body=%s", response.Code, store.savedOrder, response.Body.String())
	}
}

func TestApplyOwnRouteStepRejectsAmbiguousOrInvalidMove(t *testing.T) {
	tests := []struct {
		name     string
		moveUp   string
		moveDown string
	}{
		{name: "both directions", moveUp: "stop-2", moveDown: "stop-1"},
		{name: "unknown stop", moveUp: "stop-3"},
		{name: "past first stop", moveUp: "stop-1"},
		{name: "past last stop", moveDown: "stop-2"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := applyOwnRouteStep([]string{"stop-1", "stop-2"}, test.moveUp, test.moveDown); !errors.Is(err, planning.ErrValidation) {
				t.Fatalf("applyOwnRouteStep() error = %v, want validation", err)
			}
		})
	}
}

func routeHTTPFixture() *routeHTTPStore {
	departure := time.Date(2026, 8, 27, 5, 0, 0, 0, time.UTC)
	stops := []planning.RouteStop{
		{ID: "stop-1", JobID: "job-1", JobNumber: "HA-2026-0042", CustomerName: "Maria Maier", CustomerPhone: "+43660123456", Region: "Forsttal", VolumeM3: "80.00", Position: 1, Location: planning.Point{Latitude: 48.2, Longitude: 14.2}, WorkDuration: time.Hour, LegDuration: 10 * time.Minute, LegDistanceMeters: 8000, EstimatedArrivalAt: departure.Add(10 * time.Minute), StartsAt: departure.Add(10 * time.Minute), EndsAt: departure.Add(70 * time.Minute), AppointmentID: "appointment-1", JobVersion: 1, WaitlistVersion: 1},
		{ID: "stop-2", JobID: "job-2", JobNumber: "HA-2026-0043", CustomerName: "Franz Huber", Region: "Forsttal", VolumeM3: "60.00", Position: 2, Location: planning.Point{Latitude: 48.3, Longitude: 14.3}, WorkDuration: time.Hour, LegDuration: 10 * time.Minute, LegDistanceMeters: 8000, EstimatedArrivalAt: departure.Add(80 * time.Minute), StartsAt: departure.Add(80 * time.Minute), EndsAt: departure.Add(140 * time.Minute), AppointmentID: "appointment-2", JobVersion: 1, WaitlistVersion: 1},
	}
	return &routeHTTPStore{
		candidates: []planning.RouteCandidate{{JobID: "job-1", JobNumber: "HA-2026-0042", CustomerName: "Maria Maier", Region: "Forsttal", VolumeM3: "80.00", JobType: "chipping_only", Location: stops[0].Location, WorkDuration: time.Hour, JobVersion: 1, WaitlistVersion: 1}},
		options:    planning.RouteOptions{Drivers: []planning.RouteDriverOption{{ID: "driver-1", Name: "Anna Fahrerin"}}, Resources: []planning.RouteResourceOption{{ID: "resource-1", Name: "Hackmaschine 1", Type: "chipper"}}},
		route:      planning.RouteDraft{ID: "route-1", DriverID: "driver-1", DriverName: "Anna Fahrerin", ChipperResourceID: "resource-1", ChipperName: "Hackmaschine 1", Status: planning.RouteStatusAssigned, Version: 1, Departure: departure, Start: planning.Point{Latitude: 48.2, Longitude: 14.2}, End: planning.Point{Latitude: 48.2, Longitude: 14.2}, Stops: stops, Directions: planning.RouteDirections{Geometry: []planning.Point{{Latitude: 48.2, Longitude: 14.2}, {Latitude: 48.3, Longitude: 14.3}}, Legs: []planning.RouteLeg{{Duration: 10 * time.Minute, DistanceMeters: 8000}, {Duration: 10 * time.Minute, DistanceMeters: 8000}, {Duration: 10 * time.Minute, DistanceMeters: 8000}}, Source: "osrm", DistanceMeters: 24000, Duration: 30 * time.Minute}, EstimatedEndAt: departure.Add(150 * time.Minute)},
	}
}

func routeTestRouter(t *testing.T, role auth.Role, driverID string, store *routeHTTPStore) (http.Handler, string, string) {
	t.Helper()
	now := time.Date(2026, 8, 26, 8, 0, 0, 0, time.UTC)
	// #nosec G101 -- deterministic non-secret test fixture tokens.
	sessionToken, csrfToken := "route-session", "route-csrf"
	actor := auth.Actor{UserID: "user-1", Username: "intern", DisplayName: "Intern", Role: role, DriverID: driverID, UserVersion: 1}
	identityStore := &identityTestStore{user: auth.User{ID: actor.UserID, Username: actor.Username, DisplayName: actor.DisplayName, Role: role, DriverID: driverID, Active: true, Version: 1}, session: auth.Session{ID: "session", Actor: actor, CSRFTokenHash: auth.TokenHash(csrfToken), IdleExpiresAt: now.Add(time.Hour), AbsoluteExpiresAt: now.Add(8 * time.Hour), UserActive: true}}
	hasher, _ := auth.NewPasswordHasher(auth.PasswordParameters{MemoryKiB: 8, Iterations: 1, Parallelism: 1, SaltLength: 16, KeyLength: 16, MinLength: 14})
	identity, err := auth.NewService(identityStore, hasher, func() time.Time { return now }, time.Hour, 8*time.Hour, 5)
	if err != nil {
		t.Fatal(err)
	}
	haversine := planning.NewHaversineRouter(1.3, 55)
	service, err := planning.NewRouteService(store, haversine, haversine, planning.DefaultRouteConfig())
	if err != nil {
		t.Fatal(err)
	}
	webConfig := configForWebTest()
	webConfig.Planning.DepotLatitude = 48.2
	webConfig.Planning.DepotLongitude = 14.2
	router, err := NewRouter(Dependencies{Config: webConfig, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Database: pinger{}, Build: buildinfo.Info{Version: "test"}, Identity: identity, Routes: service})
	if err != nil {
		t.Fatal(err)
	}
	return router, sessionToken, csrfToken
}
