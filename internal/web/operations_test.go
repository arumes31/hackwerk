package web

import (
	"context"
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
	"example.invalid/hackplan/internal/driver"
	"example.invalid/hackplan/internal/resource"
)

const (
	operationDriverID = "50000000-0000-0000-0000-000000000001"
	otherDriverID     = "50000000-0000-0000-0000-000000000002"
)

type driverHTTPStore struct {
	target            string
	rule              driver.RuleInput
	schedule          driver.Availability
	clearedWeekday    int
	clearedRefs       []driver.RuleRef
	createdExceptions []driver.ExceptionInput
}

func (store *driverHTTPStore) ListProfiles(context.Context) ([]driver.Profile, error) {
	return []driver.Profile{store.schedule.Profile}, nil
}
func (store *driverHTTPStore) CreateProfile(context.Context, auth.Actor, driver.ProfileInput, string) (string, error) {
	return operationDriverID, nil
}
func (store *driverHTTPStore) UpdateProfile(context.Context, auth.Actor, string, int32, driver.ProfileInput, string) error {
	return nil
}
func (store *driverHTTPStore) DeactivateProfile(context.Context, auth.Actor, string, int32, string) error {
	return nil
}
func (store *driverHTTPStore) Schedule(_ context.Context, target string) (driver.Availability, error) {
	store.target = target
	return store.schedule, nil
}
func (store *driverHTTPStore) Availability(_ context.Context, target string, _, _ time.Time, _, _ string) (driver.Availability, error) {
	store.target = target
	return store.schedule, nil
}
func (store *driverHTTPStore) CreateRule(_ context.Context, _ auth.Actor, target string, input driver.RuleInput, _ string) (string, error) {
	store.target, store.rule = target, input
	return "rule-id", nil
}
func (store *driverHTTPStore) UpdateRule(context.Context, auth.Actor, string, string, int32, driver.RuleInput, string) error {
	return nil
}
func (store *driverHTTPStore) DeleteRule(context.Context, auth.Actor, string, string, int32, string) error {
	return nil
}
func (store *driverHTTPStore) ClearRulesForDay(_ context.Context, _ auth.Actor, target string, weekday int, refs []driver.RuleRef, _ string) error {
	store.target, store.clearedWeekday, store.clearedRefs = target, weekday, append([]driver.RuleRef(nil), refs...)
	return nil
}
func (store *driverHTTPStore) CreateException(context.Context, auth.Actor, string, driver.ExceptionInput, string) (string, error) {
	return "exception-id", nil
}
func (store *driverHTTPStore) CreateExceptions(_ context.Context, _ auth.Actor, _ string, inputs []driver.ExceptionInput, _ string) error {
	store.createdExceptions = append([]driver.ExceptionInput(nil), inputs...)
	return nil
}
func (store *driverHTTPStore) UpdateException(context.Context, auth.Actor, string, string, int32, driver.ExceptionInput, string) error {
	return nil
}
func (store *driverHTTPStore) DeleteException(context.Context, auth.Actor, string, string, int32, string) error {
	return nil
}

type resourceHTTPStore struct {
	created resource.Input
}

func (store *resourceHTTPStore) List(context.Context) ([]resource.Resource, error) { return nil, nil }
func (store *resourceHTTPStore) Create(_ context.Context, _ auth.Actor, input resource.Input, _ string) (string, error) {
	store.created = input
	return "resource-id", nil
}
func (store *resourceHTTPStore) Update(context.Context, auth.Actor, string, int32, resource.Input, string) error {
	return nil
}
func (store *resourceHTTPStore) Deactivate(context.Context, auth.Actor, string, int32, string) error {
	return nil
}

func TestAvailabilityHTTPScopesDriverToSessionProfile(t *testing.T) {
	store := defaultDriverHTTPStore()
	router, sessionToken, csrfToken := operationsTestRouter(t, auth.RoleDriver, operationDriverID, store, &resourceHTTPStore{})
	form := url.Values{"csrf_token": {csrfToken}, "weekday": {"1"}, "local_start": {"08:00"}, "local_end": {"17:00"}, "valid_from": {"2026-01-01"}, "status": {"available"}}
	request := authenticatedCustomerRequest(t, http.MethodPost, "/availability/rules", form, sessionToken, csrfToken)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	if response.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, body = %q", response.Code, response.Body.String())
	}
	if store.target != operationDriverID || store.rule.Weekday != 1 {
		t.Fatalf("mutation target/input = %q/%#v", store.target, store.rule)
	}
}

func TestAvailabilityHTTPDriverWithoutProfileRedirectsToDashboard(t *testing.T) {
	store := defaultDriverHTTPStore()
	router, sessionToken, csrfToken := operationsTestRouter(t, auth.RoleDriver, "", store, &resourceHTTPStore{})
	request := authenticatedCustomerRequest(t, http.MethodGet, "/availability", nil, sessionToken, csrfToken)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	if response.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusSeeOther)
	}
	if location := response.Header().Get("Location"); location != "/dashboard" {
		t.Fatalf("location = %q, want /dashboard", location)
	}
	if store.target != "" {
		t.Fatalf("availability lookup target = %q, want no lookup", store.target)
	}
}

func TestAvailabilityHTTPDriverCannotUseForeignAdminRoute(t *testing.T) {
	store := defaultDriverHTTPStore()
	router, sessionToken, csrfToken := operationsTestRouter(t, auth.RoleDriver, operationDriverID, store, &resourceHTTPStore{})
	form := url.Values{"csrf_token": {csrfToken}, "weekday": {"1"}, "local_start": {"08:00"}, "local_end": {"17:00"}, "valid_from": {"2026-01-01"}, "status": {"available"}}
	request := authenticatedCustomerRequest(t, http.MethodPost, "/admin/drivers/"+otherDriverID+"/availability/rules", form, sessionToken, csrfToken)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	if response.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", response.Code)
	}
	if store.target != "" {
		t.Fatalf("foreign store mutation target = %q, want no call", store.target)
	}
}

func TestAvailabilityHTTPQuickActionsUseOwnProfile(t *testing.T) {
	store := defaultDriverHTTPStore()
	store.schedule.Rules = []driver.Rule{{
		ID: "rule-a", DriverID: operationDriverID, Weekday: 1, StartMinute: 8 * 60, EndMinute: 17 * 60,
		ValidFrom: "2026-01-01", Status: driver.RuleAvailable, Version: 3,
	}}
	router, sessionToken, csrfToken := operationsTestRouter(t, auth.RoleDriver, operationDriverID, store, &resourceHTTPStore{})

	requests := []struct {
		path string
		form url.Values
	}{
		{path: "/availability/rules/rule-a/duplicate", form: url.Values{"csrf_token": {csrfToken}, "weekday": {"2"}}},
		{path: "/availability/rules/clear-day", form: url.Values{"csrf_token": {csrfToken}, "weekday": {"1"}, "rule_id": {"rule-a"}, "rule_version": {"3"}}},
		{path: "/availability/exceptions/vacation-preset", form: url.Values{"csrf_token": {csrfToken}, "local_date": {"2026-09-04"}, "workweek": {"true"}}},
	}
	for _, test := range requests {
		response := httptest.NewRecorder()
		router.ServeHTTP(response, authenticatedCustomerRequest(t, http.MethodPost, test.path, test.form, sessionToken, csrfToken))
		if response.Code != http.StatusSeeOther {
			t.Fatalf("POST %s status = %d, body = %q", test.path, response.Code, response.Body.String())
		}
	}
	if store.target != operationDriverID || store.rule.Weekday != 2 {
		t.Fatalf("duplicate target/rule = %q/%#v", store.target, store.rule)
	}
	if store.clearedWeekday != 1 || len(store.clearedRefs) != 1 || store.clearedRefs[0].Version != 3 {
		t.Fatalf("clear day = %d/%#v", store.clearedWeekday, store.clearedRefs)
	}
	if len(store.createdExceptions) != 5 {
		t.Fatalf("vacation preset = %#v", store.createdExceptions)
	}
}

func TestResourcesHTTPAdminCreatesTypedResource(t *testing.T) {
	driverStore := defaultDriverHTTPStore()
	resourceStore := &resourceHTTPStore{}
	router, sessionToken, csrfToken := operationsTestRouter(t, auth.RoleAdmin, "", driverStore, resourceStore)
	form := url.Values{"csrf_token": {csrfToken}, "type": {"chipper"}, "name": {"Hackmaschine 2"}, "exclusive": {"true"}, "volume_m3": {"180.5"}}
	request := authenticatedCustomerRequest(t, http.MethodPost, "/admin/resources", form, sessionToken, csrfToken)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	if response.Code != http.StatusSeeOther || resourceStore.created.Type != resource.TypeChipper || resourceStore.created.Capacity.VolumeM3 == nil || *resourceStore.created.Capacity.VolumeM3 != 180.5 {
		t.Fatalf("status/input = %d/%#v", response.Code, resourceStore.created)
	}
}

func TestAvailabilityAPIRedactsInternalNote(t *testing.T) {
	store := defaultDriverHTTPStore()
	store.schedule.Exceptions = []driver.Exception{{ID: "exception-id", DriverID: operationDriverID, Type: driver.ExceptionSick, IsAllDay: true, LocalDate: "2026-08-26", InternalNote: "Diagnose darf nicht ins Overlay"}}
	router, sessionToken, csrfToken := operationsTestRouter(t, auth.RoleDriver, operationDriverID, store, &resourceHTTPStore{})
	request := authenticatedCustomerRequest(t, http.MethodGet, "/api/v1/me/availability?from=2026-08-26T00:00:00Z&to=2026-08-27T00:00:00Z", nil, sessionToken, csrfToken)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), "Diagnose") || strings.Contains(response.Body.String(), "internal_note") {
		t.Fatalf("private note leaked: %s", response.Body.String())
	}
	if !strings.Contains(response.Body.String(), `"status":"unavailable"`) || !strings.Contains(response.Body.String(), `"source":"exception"`) {
		t.Fatalf("missing minimal provenance: %s", response.Body.String())
	}
}

func defaultDriverHTTPStore() *driverHTTPStore {
	return &driverHTTPStore{schedule: driver.Availability{Profile: driver.Profile{ID: operationDriverID, DisplayName: "Franz Fahrer", IsActive: true}}}
}

func operationsTestRouter(t *testing.T, role auth.Role, driverID string, driverStore *driverHTTPStore, resourceStore *resourceHTTPStore) (http.Handler, string, string) {
	t.Helper()
	now := time.Date(2026, 8, 25, 10, 0, 0, 0, time.UTC)
	// #nosec G101 -- deterministic non-secret test fixture tokens.
	sessionToken, csrfToken := "test-session-token", "test-csrf-token"
	identityStore := &identityTestStore{
		user:    auth.User{ID: "40000000-0000-0000-0000-000000000001", Username: "intern", DisplayName: "Interner Benutzer", Role: role, Active: true, Version: 1},
		session: auth.Session{ID: "session-id", Actor: auth.Actor{UserID: "40000000-0000-0000-0000-000000000001", Username: "intern", DisplayName: "Interner Benutzer", Role: role, DriverID: driverID, UserVersion: 1}, CSRFTokenHash: auth.TokenHash(csrfToken), IdleExpiresAt: now.Add(time.Hour), AbsoluteExpiresAt: now.Add(8 * time.Hour), UserActive: true},
	}
	hasher, err := auth.NewPasswordHasher(auth.PasswordParameters{MemoryKiB: 8, Iterations: 1, Parallelism: 1, SaltLength: 16, KeyLength: 16, MinLength: 14})
	if err != nil {
		t.Fatal(err)
	}
	identity, err := auth.NewService(identityStore, hasher, func() time.Time { return now }, time.Hour, 8*time.Hour, 5)
	if err != nil {
		t.Fatal(err)
	}
	location, err := time.LoadLocation("Europe/Vienna")
	if err != nil {
		t.Fatal(err)
	}
	driverService, err := driver.New(driverStore, location)
	if err != nil {
		t.Fatal(err)
	}
	resourceService, err := resource.New(resourceStore)
	if err != nil {
		t.Fatal(err)
	}
	router, err := NewRouter(Dependencies{Config: configForWebTest(), Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Database: pinger{}, Build: buildinfo.Info{Version: "test"}, Identity: identity, Drivers: driverService, Resources: resourceService})
	if err != nil {
		t.Fatal(err)
	}
	return router, sessionToken, csrfToken
}
