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
	"example.invalid/hackplan/internal/routelocation"
)

type routeHTTPStore struct {
	candidates []planning.RouteCandidate
	options    planning.RouteOptions
	route      planning.RouteDraft
	savedOrder []string
	savedRoute planning.RouteDraft
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
	store.savedRoute = input.Route
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
		!strings.Contains(body, `value="custom"`) || !strings.Contains(body, "Anderen Startort verwenden") ||
		!strings.Contains(body, "Anderen Endort verwenden") || !strings.Contains(body, "Beim letzten Stopp") ||
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
		strings.Contains(body, "data-depot-latitude") ||
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
	body := page.Body.String()
	if page.Code != http.StatusOK || !strings.Contains(body, "Meine Route") || !strings.Contains(body, "Nur Fahrreihenfolge") ||
		!strings.Contains(body, "4 Routenpunkte") ||
		!strings.Contains(body, `data-route-own="true"`) || !strings.Contains(body, `type="submit" name="move_up" value="stop-2"`) ||
		!strings.Contains(body, `data-route-start-label="Betriebshof" data-route-start-latitude="48.200000"`) ||
		!strings.Contains(body, `data-route-end-label="Betriebshof" data-route-end-latitude="48.200000"`) ||
		!strings.Contains(body, "data-wake-lock") ||
		!strings.Contains(body, "data-route-navigation") || !strings.Contains(body, "data-route-call") {
		t.Fatalf("own route page=%d %s", page.Code, body)
	}
	nextStop := strings.Index(body, `class="route-stop-card route-next-stop"`)
	stopList := strings.Index(body, `class="route-summary route-stop-section"`)
	routeMap := strings.Index(body, `class="route-map-panel route-own-map"`)
	if nextStop < 0 || stopList <= nextStop || routeMap <= stopList {
		t.Fatalf("own route regions are not ordered next stop, stop list, map: next=%d stops=%d map=%d", nextStop, stopList, routeMap)
	}
	if !strings.Contains(body, `<details class="route-secondary-metrics">`) ||
		!strings.Contains(body, `class="route-order-actions route-stop-order-actions" data-route-order-actions`) ||
		!strings.Contains(body, `<div class="route-reorder-panel" data-route-order-save>`) ||
		!strings.Contains(body, `data-route-order-save-button`) ||
		!strings.Contains(body, `method="post" action="/my-route/route-1/reorder"`) {
		t.Fatalf("own route must keep native no-JavaScript moves and an always-visible save action: %s", body)
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

func TestRouteHTTPAdminPlansConfirmedCustomEndpoints(t *testing.T) {
	store := routeHTTPFixture()
	router, session, csrf := routeTestRouter(t, auth.RoleAdmin, "", store)
	form := url.Values{
		"csrf_token":                    {csrf},
		"departure":                     {"2026-08-27T08:00"},
		"driver_id":                     {"driver-1"},
		"chipper_resource_id":           {"resource-1"},
		"job_id":                        {"job-1"},
		"start_selection":               {"custom"},
		"start_custom_confirmed_native": {"true"},
		"start_custom_label":            {"Hof Süd"},
		"start_custom_address":          {"Hofstraße 1"},
		"start_latitude":                {"48,200000"},
		"start_longitude":               {"14,200000"},
		"end_selection":                 {"custom"},
		"end_custom_confirmed_native":   {"true"},
		"end_custom_label":              {"Lager Nord"},
		"end_custom_address":            {"Lagerweg 2"},
		"end_latitude":                  {"48,250000"},
		"end_longitude":                 {"14,250000"},
		"optimize":                      {"true"},
	}
	response := httptest.NewRecorder()
	router.ServeHTTP(response, authenticatedCustomerRequest(t, http.MethodPost, "/planning/routes", form, session, csrf))
	if response.Code != http.StatusSeeOther || !strings.HasPrefix(response.Header().Get("Location"), "/planning/routes?") ||
		!strings.Contains(response.Header().Get("Location"), "route_id=route-1") {
		t.Fatalf("plan=%d location=%q body=%s", response.Code, response.Header().Get("Location"), response.Body.String())
	}
	if store.savedRoute.StartLabel != "Hof Süd" || store.savedRoute.EndLabel != "Lager Nord" ||
		store.savedRoute.Start != (planning.Point{Latitude: 48.2, Longitude: 14.2}) || store.savedRoute.End != (planning.Point{Latitude: 48.25, Longitude: 14.25}) {
		t.Fatalf("saved route=%+v", store.savedRoute)
	}
}

func TestRouteHTTPPlanRejectsInvalidViennaDSTDepartures(t *testing.T) {
	tests := []struct {
		name      string
		departure string
		split     bool
	}{
		{name: "combined nonexistent local time", departure: "2026-03-29T02:30"},
		{name: "combined ambiguous local time", departure: "2026-10-25T02:30"},
		{name: "split nonexistent local time", departure: "2026-03-29T02:30", split: true},
		{name: "split ambiguous local time", departure: "2026-10-25T02:30", split: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := routeHTTPFixture()
			router, session, csrf := routeTestRouter(t, auth.RoleAdmin, "", store)
			form := url.Values{
				"csrf_token":                    {csrf},
				"driver_id":                     {"driver-1"},
				"chipper_resource_id":           {"resource-1"},
				"job_id":                        {"job-1"},
				"start_selection":               {"custom"},
				"start_custom_confirmed_native": {"true"},
				"start_custom_label":            {"Hof Süd"},
				"start_custom_address":          {"Hofstraße 1"},
				"start_latitude":                {"48.200000"},
				"start_longitude":               {"14.200000"},
				"end_selection":                 {"last_stop"},
			}
			if test.split {
				date, clock, _ := strings.Cut(test.departure, "T")
				form.Set("departure_date", date)
				form.Set("departure_time", clock)
			} else {
				form.Set("departure", test.departure)
			}

			response := httptest.NewRecorder()
			router.ServeHTTP(response, authenticatedCustomerRequest(t, http.MethodPost, "/planning/routes", form, session, csrf))
			if response.Code != http.StatusUnprocessableEntity ||
				!strings.Contains(response.Body.String(), "gültiges Abfahrtsdatum und gültige Abfahrtszeit eingeben") ||
				store.savedRoute.ID != "" {
				t.Fatalf("response=%d body=%s saved=%+v", response.Code, response.Body.String(), store.savedRoute)
			}
		})
	}
}

func TestRouteHTTPPlanRejectsUnconfirmedOrUnavailableEndpoints(t *testing.T) {
	tests := []struct {
		name   string
		form   url.Values
		status int
		body   string
	}{
		{
			name: "custom endpoint requires explicit confirmation",
			form: url.Values{
				"departure": {"2026-08-27T08:00"}, "driver_id": {"driver-1"}, "chipper_resource_id": {"resource-1"}, "job_id": {"job-1"},
				"start_selection": {"custom"}, "start_custom_label": {"Hof Süd"}, "start_custom_address": {"Hofstraße 1"}, "start_latitude": {"48.2"}, "start_longitude": {"14.2"},
				"end_selection": {"last_stop"},
			}, status: http.StatusUnprocessableEntity, body: "individuellen Startort ausdrücklich",
		},
		{
			name: "saved endpoint cannot be forged without configured locations",
			form: url.Values{
				"departure": {"2026-08-27T08:00"}, "driver_id": {"driver-1"}, "chipper_resource_id": {"resource-1"}, "job_id": {"job-1"},
				"start_selection": {"saved:location-1"}, "start_location_id": {"location-1"}, "start_location_version": {"1"}, "end_selection": {"last_stop"},
			}, status: http.StatusNotFound, body: "nicht mehr verfügbar",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := routeHTTPFixture()
			router, session, csrf := routeTestRouter(t, auth.RoleAdmin, "", store)
			test.form.Set("csrf_token", csrf)
			response := httptest.NewRecorder()
			router.ServeHTTP(response, authenticatedCustomerRequest(t, http.MethodPost, "/planning/routes", test.form, session, csrf))
			if response.Code != test.status || !strings.Contains(response.Body.String(), test.body) || store.savedRoute.ID != "" {
				t.Fatalf("response=%d body=%s saved=%+v", response.Code, response.Body.String(), store.savedRoute)
			}
		})
	}
}

func TestRouteHTTPValidationKeepsSubmittedSelectionAndNamesMissingFields(t *testing.T) {
	store := routeHTTPFixture()
	router, session, csrf := routeTestRouter(t, auth.RoleAdmin, "", store)
	form := url.Values{
		"csrf_token":           {csrf},
		"job_id":               {"job-1"},
		"start_selection":      {"custom"},
		"start_custom_label":   {"Lagerplatz Süd"},
		"start_custom_address": {"Hofstraße 1"},
		"start_latitude":       {"48.200000"},
		"start_longitude":      {"14.200000"},
		"end_selection":        {"last_stop"},
		"departure_date":       {"2026-08-27"},
		"departure_time":       {"07:00"},
	}
	response := httptest.NewRecorder()
	router.ServeHTTP(response, authenticatedCustomerRequest(t, http.MethodPost, "/planning/routes", form, session, csrf))
	body := response.Body.String()
	for _, expected := range []string{
		"Bitte ergänzen:",
		"individuellen Startort ausdrücklich",
		"Fahrer auswählen",
		"Hackmaschine auswählen",
		`value="job-1" aria-label="HA-2026-0042 für die Route auswählen" checked`,
		`name="start_selection" value="custom" checked`,
		`name="start_custom_label" maxlength="120" autocomplete="off" placeholder="z. B. Lagerplatz Nord" value="Lagerplatz Süd"`,
	} {
		if !strings.Contains(body, expected) {
			t.Fatalf("validation response missing %q: status=%d body=%s", expected, response.Code, body)
		}
	}
	if response.Code != http.StatusUnprocessableEntity || store.savedRoute.ID != "" {
		t.Fatalf("validation response=%d saved=%+v", response.Code, store.savedRoute)
	}
	confirmationStart := strings.Index(body, `name="start_custom_confirmed_native"`)
	if confirmationStart < 0 {
		t.Fatalf("rerendered custom endpoint has no native confirmation: %s", body)
	}
	confirmationEnd := strings.Index(body[confirmationStart:], ">")
	if confirmationEnd < 0 || strings.Contains(body[confirmationStart:confirmationStart+confirmationEnd], "checked") ||
		!strings.Contains(body, `name="start_custom_confirmed" value=""`) {
		t.Fatalf("rerendered custom endpoint remained implicitly confirmed: %s", body)
	}
}

func TestRouteHelperErrorAndComparisonMappings(t *testing.T) {
	route := &planning.RouteDraft{}
	applyRouteComparisonQuery(route, url.Values{
		"manual_distance": {"1200"}, "optimized_distance": {"900"}, "manual_duration": {"900"}, "optimized_duration": {"700"},
	})
	if route.Comparison.ManualDistanceMeters != 1200 || route.Comparison.OptimizedDuration != 700*time.Second {
		t.Fatalf("comparison=%+v", route.Comparison)
	}
	applyRouteComparisonQuery(route, url.Values{
		"manual_distance": {"-1"}, "optimized_distance": {"900"}, "manual_duration": {"900"}, "optimized_duration": {"700"},
	})
	if route.Comparison.ManualDistanceMeters != 1200 {
		t.Fatalf("invalid comparison overwrote valid value: %+v", route.Comparison)
	}

	tests := []struct {
		name   string
		err    error
		status int
		body   string
	}{
		{name: "forbidden", err: auth.ErrForbidden, status: http.StatusForbidden, body: "Berechtigung"},
		{name: "conflict", err: planning.ErrConflict, status: http.StatusConflict, body: "geändert"},
		{name: "capacity", err: planning.ErrNoCapacity, status: http.StatusUnprocessableEntity, body: "nicht verfügbar"},
		{name: "validation", err: planning.ErrValidation, status: http.StatusUnprocessableEntity, body: "vollständig"},
		{name: "not found", err: planning.ErrNotFound, status: http.StatusNotFound, body: "nicht gefunden"},
		{name: "location conflict", err: routelocation.ErrConflict, status: http.StatusConflict, body: "Start- oder Endort"},
		{name: "location missing", err: routelocation.ErrNotFound, status: http.StatusNotFound, body: "nicht mehr verfügbar"},
		{name: "location invalid", err: routelocation.ErrValidation, status: http.StatusUnprocessableEntity, body: "auswählen"},
		{name: "internal", err: errors.New("database unavailable"), status: http.StatusInternalServerError, body: "derzeit"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			status, body := routeError(test.err)
			if status != test.status || !strings.Contains(body, test.body) {
				t.Fatalf("routeError(%v)=(%d,%q)", test.err, status, body)
			}
		})
	}
}

func TestRouteHTTPDraftMutationAndOwnRouteErrorFlows(t *testing.T) {
	store := routeHTTPFixture()
	store.route.Status = planning.RouteStatusDraft
	router, session, csrf := routeTestRouter(t, auth.RoleAdmin, "", store)
	assign := httptest.NewRecorder()
	router.ServeHTTP(assign, authenticatedCustomerRequest(t, http.MethodPost, "/planning/routes/route-1/assign", url.Values{"csrf_token": {csrf}, "version": {"1"}}, session, csrf))
	if assign.Code != http.StatusSeeOther || !strings.Contains(assign.Header().Get("Location"), "route_id=route-1") {
		t.Fatalf("assign=%d location=%q", assign.Code, assign.Header().Get("Location"))
	}

	move := httptest.NewRecorder()
	router.ServeHTTP(move, authenticatedCustomerRequest(t, http.MethodPost, "/planning/routes/route-1/move-stop", url.Values{"csrf_token": {csrf}, "source_version": {"invalid"}, "target_version": {"1"}, "date": {"2026-08-27"}}, session, csrf))
	if move.Code != http.StatusSeeOther || !strings.Contains(move.Header().Get("Location"), "error=") {
		t.Fatalf("invalid move=%d location=%q", move.Code, move.Header().Get("Location"))
	}

	store.route = planning.RouteDraft{}
	driverRouter, driverSession, driverCSRF := routeTestRouter(t, auth.RoleDriver, "driver-1", store)
	missingOwnRoute := httptest.NewRecorder()
	driverRouter.ServeHTTP(missingOwnRoute, authenticatedCustomerRequest(t, http.MethodGet, "/my-route?date=2026-08-27", nil, driverSession, driverCSRF))
	if missingOwnRoute.Code != http.StatusOK || !strings.Contains(missingOwnRoute.Body.String(), "Keine zugewiesene Route") {
		t.Fatalf("missing own route=%d body=%s", missingOwnRoute.Code, missingOwnRoute.Body.String())
	}

	invalidReorder := httptest.NewRecorder()
	driverRouter.ServeHTTP(invalidReorder, authenticatedCustomerRequest(t, http.MethodPost, "/my-route/route-1/reorder", url.Values{"csrf_token": {driverCSRF}, "version": {"invalid"}}, driverSession, driverCSRF))
	if invalidReorder.Code != http.StatusSeeOther || !strings.Contains(invalidReorder.Header().Get("Location"), "error=") {
		t.Fatalf("invalid reorder=%d location=%q", invalidReorder.Code, invalidReorder.Header().Get("Location"))
	}
}

func TestDraftMoveTargetSupportsNoJavaScriptEncodedSelection(t *testing.T) {
	targetID, version, err := draftMoveTarget(url.Values{"target_route": {"route-2|7"}})
	if err != nil || targetID != "route-2" || version != 7 {
		t.Fatalf("draftMoveTarget() = %q, %d, %v", targetID, version, err)
	}
	if _, _, err := draftMoveTarget(url.Values{"target_route": {"route-2"}}); !errors.Is(err, planning.ErrValidation) {
		t.Fatalf("invalid encoded target error = %v", err)
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
		route:      planning.RouteDraft{ID: "route-1", DriverID: "driver-1", DriverName: "Anna Fahrerin", ChipperResourceID: "resource-1", ChipperName: "Hackmaschine 1", Status: planning.RouteStatusAssigned, Version: 1, Departure: departure, StartLabel: "Betriebshof", EndLabel: "Betriebshof", Start: planning.Point{Latitude: 48.2, Longitude: 14.2}, End: planning.Point{Latitude: 48.2, Longitude: 14.2}, Stops: stops, Directions: planning.RouteDirections{Geometry: []planning.Point{{Latitude: 48.2, Longitude: 14.2}, {Latitude: 48.3, Longitude: 14.3}}, Legs: []planning.RouteLeg{{Duration: 10 * time.Minute, DistanceMeters: 8000}, {Duration: 10 * time.Minute, DistanceMeters: 8000}, {Duration: 10 * time.Minute, DistanceMeters: 8000}}, Source: "osrm", DistanceMeters: 24000, Duration: 30 * time.Minute}, EstimatedEndAt: departure.Add(150 * time.Minute)},
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
	router, err := NewRouter(Dependencies{Config: webConfig, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Database: pinger{}, Build: buildinfo.Info{Version: "test"}, Identity: identity, Routes: service})
	if err != nil {
		t.Fatal(err)
	}
	return router, sessionToken, csrfToken
}
