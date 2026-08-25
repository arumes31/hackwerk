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

	"example.invalid/hackplan/internal/appointment"
	"example.invalid/hackplan/internal/auth"
	"example.invalid/hackplan/internal/buildinfo"
	"example.invalid/hackplan/internal/driver"
)

const testAppointmentID = "60000000-0000-0000-0000-000000000001"

func TestAppointmentHTTPDriverCannotMoveDirectly(t *testing.T) {
	store := &appointmentHTTPStore{current: appointment.Appointment{ID: testAppointmentID, Lifecycle: appointment.LifecycleProposal, Version: 4}}
	router, sessionToken, csrfToken := appointmentTestRouter(t, auth.RoleDriver, store)
	form := url.Values{"csrf_token": {csrfToken}, "version": {"4"}, "starts_at": {"2026-09-01T06:00:00Z"}, "ends_at": {"2026-09-01T09:00:00Z"}}
	response := httptest.NewRecorder()
	router.ServeHTTP(response, authenticatedCustomerRequest(t, http.MethodPost, "/api/v1/appointments/"+testAppointmentID+"/move", form, sessionToken, csrfToken))
	if response.Code != http.StatusForbidden || store.rescheduleCalls != 0 {
		t.Fatalf("driver move status/calls = %d/%d", response.Code, store.rescheduleCalls)
	}
}

func TestAppointmentHTTPStaleMoveReturnsStableConflict(t *testing.T) {
	store := &appointmentHTTPStore{current: appointment.Appointment{ID: testAppointmentID, Lifecycle: appointment.LifecycleProposal, Version: 5}}
	router, sessionToken, csrfToken := appointmentTestRouter(t, auth.RoleAdmin, store)
	form := url.Values{"csrf_token": {csrfToken}, "version": {"4"}, "starts_at": {"2026-09-01T06:00:00Z"}, "ends_at": {"2026-09-01T09:00:00Z"}}
	response := httptest.NewRecorder()
	router.ServeHTTP(response, authenticatedCustomerRequest(t, http.MethodPost, "/api/v1/appointments/"+testAppointmentID+"/move", form, sessionToken, csrfToken))
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), `"code":"reservation_conflict"`) || store.rescheduleCalls != 0 {
		t.Fatalf("stale response/calls = %d %q/%d", response.Code, response.Body.String(), store.rescheduleCalls)
	}
}

func TestCalendarFeedIsBoundedAndContainsNoContactData(t *testing.T) {
	store := &appointmentHTTPStore{events: []appointment.CalendarEvent{{
		Appointment: appointment.Appointment{ID: testAppointmentID, JobID: testJobID, JobNumber: "HW-2026-0001", Lifecycle: appointment.LifecycleFixed, Confirmation: appointment.ConfirmationPending, StartsAt: time.Date(2026, 9, 1, 6, 0, 0, 0, time.UTC), EndsAt: time.Date(2026, 9, 1, 9, 0, 0, 0, time.UTC), Version: 4},
		CustomerID:  testCustomerID, CustomerName: "Franz Huber", Locality: "Grieskirchen", VolumeM3: "80.00",
	}}}
	router, sessionToken, csrfToken := appointmentTestRouter(t, auth.RoleDriver, store)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, authenticatedCustomerRequest(t, http.MethodGet, "/api/v1/calendar?from=2026-09-01T00:00:00Z&to=2026-09-08T00:00:00Z", nil, sessionToken, csrfToken))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "Franz Huber") {
		t.Fatalf("calendar response = %d %q", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), "phone") || strings.Contains(response.Body.String(), "email") {
		t.Fatalf("calendar feed leaked contact fields: %s", response.Body.String())
	}

	response = httptest.NewRecorder()
	router.ServeHTTP(response, authenticatedCustomerRequest(t, http.MethodGet, "/api/v1/calendar?from=2026-01-01T00:00:00Z&to=2026-12-31T00:00:00Z", nil, sessionToken, csrfToken))
	if response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("oversized range status = %d", response.Code)
	}
}

func appointmentTestRouter(t *testing.T, role auth.Role, store *appointmentHTTPStore) (http.Handler, string, string) {
	t.Helper()
	now := time.Date(2026, 8, 25, 10, 0, 0, 0, time.UTC)
	sessionToken, csrfToken := "test-session-token", "test-csrf-token"
	identityStore := &identityTestStore{
		user:    auth.User{ID: "40000000-0000-0000-0000-000000000001", Username: "intern", DisplayName: "Interner Benutzer", Role: role, Active: true, Version: 1},
		session: auth.Session{ID: "session-id", Actor: auth.Actor{UserID: "40000000-0000-0000-0000-000000000001", Username: "intern", DisplayName: "Interner Benutzer", Role: role, DriverID: operationDriverID, UserVersion: 1}, CSRFTokenHash: auth.TokenHash(csrfToken), IdleExpiresAt: now.Add(time.Hour), AbsoluteExpiresAt: now.Add(8 * time.Hour), UserActive: true},
	}
	hasher, err := auth.NewPasswordHasher(auth.PasswordParameters{MemoryKiB: 8, Iterations: 1, Parallelism: 1, SaltLength: 16, KeyLength: 16, MinLength: 14})
	if err != nil {
		t.Fatal(err)
	}
	identity, err := auth.NewService(identityStore, hasher, func() time.Time { return now }, time.Hour, 8*time.Hour, 5)
	if err != nil {
		t.Fatal(err)
	}
	service, err := appointment.New(store, appointmentHTTPAvailability{}, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	router, err := NewRouter(Dependencies{Config: configForWebTest(), Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Database: pinger{}, Build: buildinfo.Info{Version: "test"}, Identity: identity, Appointments: service})
	if err != nil {
		t.Fatal(err)
	}
	return router, sessionToken, csrfToken
}

type appointmentHTTPAvailability struct{}

func (appointmentHTTPAvailability) IsAvailable(context.Context, auth.Actor, string, time.Time, time.Time) (driver.Status, []string, error) {
	return driver.StatusAvailable, nil, nil
}

type appointmentHTTPStore struct {
	current         appointment.Appointment
	events          []appointment.CalendarEvent
	rescheduleCalls int
}

func (store *appointmentHTTPStore) CreateDraft(context.Context, auth.Actor, appointment.CreateDraftInput) (appointment.Appointment, error) {
	return store.current, nil
}
func (store *appointmentHTTPStore) Get(context.Context, string) (appointment.Appointment, error) {
	return store.current, nil
}
func (store *appointmentHTTPStore) Assign(context.Context, auth.Actor, appointment.AssignInput) (appointment.Appointment, error) {
	return store.current, nil
}
func (store *appointmentHTTPStore) Propose(context.Context, auth.Actor, appointment.MutateInput, string) (appointment.Appointment, error) {
	return store.current, nil
}
func (store *appointmentHTTPStore) Reschedule(context.Context, auth.Actor, appointment.MoveInput, string) (appointment.Appointment, error) {
	store.rescheduleCalls++
	return store.current, nil
}
func (store *appointmentHTTPStore) Fix(context.Context, auth.Actor, appointment.MutateInput) (appointment.Appointment, error) {
	return store.current, nil
}
func (store *appointmentHTTPStore) Cancel(context.Context, auth.Actor, appointment.CancelInput) (appointment.Appointment, error) {
	return store.current, nil
}
func (store *appointmentHTTPStore) Complete(context.Context, auth.Actor, appointment.CompleteInput) (appointment.Appointment, error) {
	return store.current, nil
}
func (store *appointmentHTTPStore) ListCalendar(context.Context, time.Time, time.Time) ([]appointment.CalendarEvent, error) {
	return store.events, nil
}
func (store *appointmentHTTPStore) PlanningOptions(context.Context) (appointment.PlanningOptions, error) {
	return appointment.PlanningOptions{}, nil
}
func (store *appointmentHTTPStore) ListConflicts(context.Context, time.Time, time.Time, []string, []string, string) ([]appointment.Conflict, error) {
	return nil, nil
}
func (store *appointmentHTTPStore) DriverCanComplete(context.Context, string, string) (bool, error) {
	return true, nil
}
