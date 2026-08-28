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

type routeLocationHTTPStore struct {
	locations        []routelocation.Location
	created, updated routelocation.Input
	deactivated      string
	listErr          error
}

func (s *routeLocationHTTPStore) List(context.Context) ([]routelocation.Location, error) {
	return append([]routelocation.Location(nil), s.locations...), s.listErr
}

func (s *routeLocationHTTPStore) ListActive(context.Context) ([]routelocation.Location, error) {
	result := make([]routelocation.Location, 0, len(s.locations))
	for _, location := range s.locations {
		if location.Active {
			result = append(result, location)
		}
	}
	return result, nil
}

func (s *routeLocationHTTPStore) DefaultStart(context.Context) (routelocation.Location, error) {
	for _, location := range s.locations {
		if location.Active && location.DefaultStart {
			return location, nil
		}
	}
	return routelocation.Location{}, routelocation.ErrNotFound
}

func (s *routeLocationHTTPStore) Resolve(_ context.Context, id string, version int32) (routelocation.Location, error) {
	for _, location := range s.locations {
		if location.ID != id || !location.Active {
			continue
		}
		if location.Version != version {
			return routelocation.Location{}, routelocation.ErrConflict
		}
		return location, nil
	}
	return routelocation.Location{}, routelocation.ErrNotFound
}

func (s *routeLocationHTTPStore) Create(_ context.Context, _ auth.Actor, input routelocation.Input, _ string) (routelocation.Location, error) {
	s.created = input
	return routeLocationFromInput("created", input), nil
}

func (s *routeLocationHTTPStore) Update(_ context.Context, _ auth.Actor, id string, version int32, input routelocation.Input, _ string) (routelocation.Location, error) {
	if _, err := s.Resolve(context.Background(), id, version); err != nil {
		return routelocation.Location{}, err
	}
	s.updated = input
	return routeLocationFromInput(id, input), nil
}

func (s *routeLocationHTTPStore) Deactivate(_ context.Context, _ auth.Actor, id string, version int32, _ string) error {
	if _, err := s.Resolve(context.Background(), id, version); err != nil {
		return err
	}
	s.deactivated = id
	return nil
}

func TestRouteLocationSettingsAdminCanManageAndDriverCannotOpen(t *testing.T) {
	store := routeLocationHTTPFixture()
	router, session, csrf := routeLocationTestRouter(t, auth.RoleAdmin, store)

	page := httptest.NewRecorder()
	router.ServeHTTP(page, authenticatedCustomerRequest(t, http.MethodGet, "/settings/route-locations", nil, session, csrf))
	if page.Code != http.StatusOK || !containsAll(page.Body.String(), "Routenorte", "Betriebshof", "Standard-Start", "Standard-Ende", "Position anklicken", "data-route-location-map", "data-map-assets", "data-route-location-confirm", "class=\"check-label\"") {
		t.Fatalf("settings page=%d %s", page.Code, page.Body.String())
	}

	create := url.Values{
		"csrf_token": {csrf}, "confirmed": {"true"}, "name": {"Lager Nord"}, "address": {"Waldweg 3"},
		"latitude": {"48,250000"}, "longitude": {"14,250000"}, "default_end": {"true"},
	}
	created := httptest.NewRecorder()
	router.ServeHTTP(created, authenticatedCustomerRequest(t, http.MethodPost, "/settings/route-locations", create, session, csrf))
	if created.Code != http.StatusSeeOther || store.created.Label != "Lager Nord" || !store.created.DefaultEnd {
		t.Fatalf("create=%d input=%+v body=%s", created.Code, store.created, created.Body.String())
	}

	nativeCreate := url.Values{
		"csrf_token": {csrf}, "confirmed_native": {"true"}, "name": {"Lager West"}, "address": {"Feldweg 4"},
		"latitude": {"48,260000"}, "longitude": {"14,260000"},
	}
	nativeCreated := httptest.NewRecorder()
	router.ServeHTTP(nativeCreated, authenticatedCustomerRequest(t, http.MethodPost, "/settings/route-locations", nativeCreate, session, csrf))
	if nativeCreated.Code != http.StatusSeeOther || store.created.Label != "Lager West" {
		t.Fatalf("native create=%d input=%+v body=%s", nativeCreated.Code, store.created, nativeCreated.Body.String())
	}

	withoutConfirmation := url.Values{
		"csrf_token": {csrf}, "name": {"Unbestätigt"}, "address": {"Waldweg 4"}, "latitude": {"48.3"}, "longitude": {"14.3"},
	}
	rejected := httptest.NewRecorder()
	router.ServeHTTP(rejected, authenticatedCustomerRequest(t, http.MethodPost, "/settings/route-locations", withoutConfirmation, session, csrf))
	if rejected.Code != http.StatusUnprocessableEntity {
		t.Fatalf("unconfirmed create=%d %s", rejected.Code, rejected.Body.String())
	}

	driverRouter, driverSession, driverCSRF := routeLocationTestRouter(t, auth.RoleDriver, routeLocationHTTPFixture())
	forbidden := httptest.NewRecorder()
	driverRouter.ServeHTTP(forbidden, authenticatedCustomerRequest(t, http.MethodGet, "/settings/route-locations", nil, driverSession, driverCSRF))
	if forbidden.Code != http.StatusForbidden {
		t.Fatalf("driver settings=%d %s", forbidden.Code, forbidden.Body.String())
	}
}

func TestRouteLocationSettingsRequireExplicitDefaultDeactivationConfirmation(t *testing.T) {
	store := routeLocationHTTPFixture()
	router, session, csrf := routeLocationTestRouter(t, auth.RoleAdmin, store)
	path := "/settings/route-locations/location-1/deactivate"

	rejected := httptest.NewRecorder()
	router.ServeHTTP(rejected, authenticatedCustomerRequest(t, http.MethodPost, path, url.Values{"csrf_token": {csrf}, "version": {"2"}}, session, csrf))
	if rejected.Code != http.StatusUnprocessableEntity || store.deactivated != "" {
		t.Fatalf("unconfirmed deactivate=%d id=%q", rejected.Code, store.deactivated)
	}

	accepted := httptest.NewRecorder()
	router.ServeHTTP(accepted, authenticatedCustomerRequest(t, http.MethodPost, path, url.Values{"csrf_token": {csrf}, "version": {"2"}, "confirm_without_default": {"true"}}, session, csrf))
	if accepted.Code != http.StatusSeeOther || store.deactivated != "location-1" {
		t.Fatalf("confirmed deactivate=%d id=%q body=%s", accepted.Code, store.deactivated, accepted.Body.String())
	}
}

func TestRouteLocationSettingsUpdateMapsValidationConflictAndUnavailableList(t *testing.T) {
	tests := []struct {
		name        string
		store       *routeLocationHTTPStore
		path        string
		form        url.Values
		wantStatus  int
		wantBody    string
		wantUpdated string
	}{
		{
			name:        "updates confirmed location",
			store:       routeLocationHTTPFixture(),
			path:        "/settings/route-locations/location-1",
			form:        url.Values{"csrf_token": {"route-location-csrf"}, "version": {"2"}, "confirmed": {"true"}, "name": {" Lager Süd "}, "address": {" Feldweg 2 "}, "latitude": {"48,25"}, "longitude": {"14,25"}, "default_end": {"true"}},
			wantStatus:  http.StatusSeeOther,
			wantUpdated: "Lager Süd",
		},
		{
			name:       "invalid version is displayed",
			store:      routeLocationHTTPFixture(),
			path:       "/settings/route-locations/location-1",
			form:       url.Values{"csrf_token": {"route-location-csrf"}, "version": {"0"}},
			wantStatus: http.StatusUnprocessableEntity,
			wantBody:   "Bezeichnung, bestätigte Adresse",
		},
		{
			name:       "stale location is displayed",
			store:      routeLocationHTTPFixture(),
			path:       "/settings/route-locations/location-1",
			form:       url.Values{"csrf_token": {"route-location-csrf"}, "version": {"1"}, "confirmed": {"true"}, "name": {"Lager Süd"}, "address": {"Feldweg 2"}, "latitude": {"48.25"}, "longitude": {"14.25"}},
			wantStatus: http.StatusConflict,
			wantBody:   "zwischenzeitlich geändert",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			router, session, csrf := routeLocationTestRouter(t, auth.RoleAdmin, test.store)
			response := httptest.NewRecorder()
			router.ServeHTTP(response, authenticatedCustomerRequest(t, http.MethodPost, test.path, test.form, session, csrf))
			if response.Code != test.wantStatus {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			if test.wantBody != "" && !strings.Contains(response.Body.String(), test.wantBody) {
				t.Fatalf("body=%s, want %q", response.Body.String(), test.wantBody)
			}
			if test.wantUpdated != "" && test.store.updated.Label != test.wantUpdated {
				t.Fatalf("updated=%+v", test.store.updated)
			}
		})
	}

	store := routeLocationHTTPFixture()
	store.listErr = errors.New("database unavailable")
	router, session, csrf := routeLocationTestRouter(t, auth.RoleAdmin, store)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, authenticatedCustomerRequest(t, http.MethodGet, "/settings/route-locations", nil, session, csrf))
	if response.Code != http.StatusInternalServerError || !strings.Contains(response.Body.String(), "Routenorte nicht verfügbar") {
		t.Fatalf("unavailable list=%d %s", response.Code, response.Body.String())
	}
}

func TestRouteLocationErrorMapsPublicResponses(t *testing.T) {
	tests := []struct {
		name    string
		err     error
		status  int
		message string
	}{
		{name: "forbidden", err: auth.ErrForbidden, status: http.StatusForbidden, message: "Berechtigung"},
		{name: "conflict", err: routelocation.ErrConflict, status: http.StatusConflict, message: "zwischenzeitlich"},
		{name: "not found", err: routelocation.ErrNotFound, status: http.StatusNotFound, message: "nicht gefunden"},
		{name: "invalid", err: routelocation.ErrValidation, status: http.StatusUnprocessableEntity, message: "Koordinaten"},
		{name: "internal", err: errors.New("database canary"), status: http.StatusInternalServerError, message: "derzeit"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			status, message := routeLocationError(test.err)
			if status != test.status || !strings.Contains(message, test.message) {
				t.Fatalf("routeLocationError(%v)=(%d,%q)", test.err, status, message)
			}
		})
	}
}

func TestRouteEndpointSupportsSavedCustomAndLastStop(t *testing.T) {
	store := routeLocationHTTPFixture()
	service, err := routelocation.New(store)
	if err != nil {
		t.Fatal(err)
	}
	actor := auth.Actor{UserID: "admin", Role: auth.RoleAdmin}

	saved := routeEndpointRequest(t, url.Values{
		"start_selection": {"saved:location-1"}, "start_location_id": {"location-1"}, "start_location_version": {"2"},
		"start_latitude": {"1"}, "start_longitude": {"1"},
	})
	point, label, lastStop, err := routeEndpoint(saved, service, actor, "start", false)
	if err != nil || point != (planning.Point{Latitude: 48.2, Longitude: 14.2}) || label != "Betriebshof" || lastStop {
		t.Fatalf("saved point=%+v label=%q last=%v err=%v", point, label, lastStop, err)
	}

	custom := routeEndpointRequest(t, url.Values{
		"end_selection": {"custom"}, "end_custom_confirmed": {"true"}, "end_custom_label": {"Lager Nord"},
		"end_custom_address": {"Waldweg 3"}, "end_latitude": {"48,25"}, "end_longitude": {"14,25"},
	})
	point, label, lastStop, err = routeEndpoint(custom, service, actor, "end", true)
	if err != nil || point != (planning.Point{Latitude: 48.25, Longitude: 14.25}) || label != "Lager Nord" || lastStop {
		t.Fatalf("custom point=%+v label=%q last=%v err=%v", point, label, lastStop, err)
	}

	last := routeEndpointRequest(t, url.Values{"end_selection": {"last_stop"}})
	point, label, lastStop, err = routeEndpoint(last, service, actor, "end", true)
	if err != nil || point.Valid() || label != "Letzter Stopp" || !lastStop {
		t.Fatalf("last point=%+v label=%q last=%v err=%v", point, label, lastStop, err)
	}
}

func TestRouteEndpointRejectsUnconfirmedOrStaleSelection(t *testing.T) {
	service, err := routelocation.New(routeLocationHTTPFixture())
	if err != nil {
		t.Fatal(err)
	}
	actor := auth.Actor{UserID: "admin", Role: auth.RoleAdmin}
	tests := []struct {
		name string
		form url.Values
		want error
	}{
		{name: "custom not confirmed", form: url.Values{"start_selection": {"custom"}, "start_custom_label": {"Ort"}, "start_custom_address": {"Adresse"}, "start_latitude": {"48.2"}, "start_longitude": {"14.2"}}, want: routelocation.ErrValidation},
		{name: "stale saved version", form: url.Values{"start_selection": {"saved:location-1"}, "start_location_id": {"location-1"}, "start_location_version": {"1"}}, want: routelocation.ErrConflict},
		{name: "last stop as start", form: url.Values{"start_selection": {"last_stop"}}, want: routelocation.ErrValidation},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, _, _, gotErr := routeEndpoint(routeEndpointRequest(t, test.form), service, actor, "start", false)
			if !errors.Is(gotErr, test.want) {
				t.Fatalf("error=%v want=%v", gotErr, test.want)
			}
		})
	}
}

func routeEndpointRequest(t *testing.T, form url.Values) *http.Request {
	t.Helper()
	request := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/planning/routes", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if err := request.ParseForm(); err != nil {
		t.Fatal(err)
	}
	return request
}

func routeLocationHTTPFixture() *routeLocationHTTPStore {
	return &routeLocationHTTPStore{locations: []routelocation.Location{{
		ID: "location-1", Label: "Betriebshof", Address: "Waldweg 1", Latitude: 48.2, Longitude: 14.2,
		Active: true, DefaultStart: true, DefaultEnd: true, Version: 2,
	}}}
}

func routeLocationFromInput(id string, input routelocation.Input) routelocation.Location {
	return routelocation.Location{
		ID: id, Label: input.Label, Address: input.Address, Latitude: input.Latitude, Longitude: input.Longitude,
		Active: true, DefaultStart: input.DefaultStart, DefaultEnd: input.DefaultEnd, Version: 1,
	}
}

func routeLocationTestRouter(t *testing.T, role auth.Role, store *routeLocationHTTPStore) (http.Handler, string, string) {
	t.Helper()
	now := time.Date(2026, 8, 27, 8, 0, 0, 0, time.UTC)
	sessionToken, csrfToken := "route-location-session", "route-location-csrf"
	actor := auth.Actor{UserID: "user-1", Username: "intern", DisplayName: "Intern", Role: role, UserVersion: 1}
	identityStore := &identityTestStore{
		user:    auth.User{ID: actor.UserID, Username: actor.Username, DisplayName: actor.DisplayName, Role: role, Active: true, Version: 1},
		session: auth.Session{ID: "session", Actor: actor, CSRFTokenHash: auth.TokenHash(csrfToken), IdleExpiresAt: now.Add(time.Hour), AbsoluteExpiresAt: now.Add(8 * time.Hour), UserActive: true},
	}
	hasher, _ := auth.NewPasswordHasher(auth.PasswordParameters{MemoryKiB: 8, Iterations: 1, Parallelism: 1, SaltLength: 16, KeyLength: 16, MinLength: 14})
	identity, err := auth.NewService(identityStore, hasher, func() time.Time { return now }, time.Hour, 8*time.Hour, 5)
	if err != nil {
		t.Fatal(err)
	}
	locations, err := routelocation.New(store)
	if err != nil {
		t.Fatal(err)
	}
	router, err := NewRouter(Dependencies{Config: configForWebTest(), Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Database: pinger{}, Build: buildinfo.Info{Version: "test"}, Identity: identity, RouteLocations: locations})
	if err != nil {
		t.Fatal(err)
	}
	return router, sessionToken, csrfToken
}

func containsAll(value string, parts ...string) bool {
	for _, part := range parts {
		if !strings.Contains(value, part) {
			return false
		}
	}
	return true
}
