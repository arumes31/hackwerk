package web

import (
	"bytes"
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
	"example.invalid/hackplan/internal/resource"
	"example.invalid/hackplan/web/templates"
)

const testAppointmentID = "60000000-0000-0000-0000-000000000001"
const testAppointmentResourceID = "70000000-0000-0000-0000-000000000001"

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

func TestAppointmentHTTPLocalMoveUsesViennaTime(t *testing.T) {
	store := &appointmentHTTPStore{current: appointment.Appointment{ID: testAppointmentID, Lifecycle: appointment.LifecycleProposal, Version: 4}}
	router, sessionToken, csrfToken := appointmentTestRouter(t, auth.RoleAdmin, store)
	form := url.Values{
		"csrf_token":       {csrfToken},
		"version":          {"4"},
		"starts_at_local":  {"2026-09-01T08:30"},
		"duration_minutes": {"195"},
	}
	response := httptest.NewRecorder()
	router.ServeHTTP(response, authenticatedCustomerRequest(t, http.MethodPost, "/api/v1/appointments/"+testAppointmentID+"/move", form, sessionToken, csrfToken))
	if response.Code != http.StatusOK || store.rescheduleCalls != 1 {
		t.Fatalf("local move status/calls = %d/%d, body %q", response.Code, store.rescheduleCalls, response.Body.String())
	}
	wantStart := time.Date(2026, 9, 1, 6, 30, 0, 0, time.UTC)
	wantEnd := time.Date(2026, 9, 1, 9, 45, 0, 0, time.UTC)
	if !store.lastMove.StartsAt.Equal(wantStart) || !store.lastMove.EndsAt.Equal(wantEnd) {
		t.Fatalf("local move range = %s--%s, want %s--%s", store.lastMove.StartsAt, store.lastMove.EndsAt, wantStart, wantEnd)
	}
}

func TestAppointmentHTTPLocalMoveRejectsNonexistentViennaTime(t *testing.T) {
	store := &appointmentHTTPStore{current: appointment.Appointment{ID: testAppointmentID, Lifecycle: appointment.LifecycleProposal, Version: 4}}
	router, sessionToken, csrfToken := appointmentTestRouter(t, auth.RoleAdmin, store)
	form := url.Values{
		"csrf_token":       {csrfToken},
		"version":          {"4"},
		"starts_at_local":  {"2026-03-29T02:30"},
		"duration_minutes": {"180"},
	}
	response := httptest.NewRecorder()
	router.ServeHTTP(response, authenticatedCustomerRequest(t, http.MethodPost, "/api/v1/appointments/"+testAppointmentID+"/move", form, sessionToken, csrfToken))
	if response.Code != http.StatusUnprocessableEntity || store.rescheduleCalls != 0 {
		t.Fatalf("DST-gap move status/calls = %d/%d, body %q", response.Code, store.rescheduleCalls, response.Body.String())
	}
}

func TestAppointmentHTTPAdminCanReopenCancelledAppointmentAsProposal(t *testing.T) {
	store := &appointmentHTTPStore{current: appointment.Appointment{
		ID: testAppointmentID, Lifecycle: appointment.LifecycleCancelled, Version: 7,
		Drivers:   []appointment.DriverAssignment{{ID: operationDriverID, Name: "Anna Fahrerin", Primary: true}},
		Resources: []appointment.AssignedResource{{ID: testAppointmentResourceID, Name: "Hacker 1", Type: resource.TypeChipper, Purpose: appointment.PurposeChipping, Exclusive: true}},
		StartsAt:  time.Date(2026, 9, 1, 6, 0, 0, 0, time.UTC), EndsAt: time.Date(2026, 9, 1, 9, 0, 0, 0, time.UTC),
	}}
	router, sessionToken, csrfToken := appointmentTestRouter(t, auth.RoleAdmin, store)
	form := url.Values{"csrf_token": {csrfToken}, "version": {"7"}, "reason": {"Kunde möchte neu planen"}}
	response := httptest.NewRecorder()
	router.ServeHTTP(response, authenticatedCustomerRequest(t, http.MethodPost, "/api/v1/appointments/"+testAppointmentID+"/reopen", form, sessionToken, csrfToken))
	if response.Code != http.StatusOK || store.reopenCalls != 1 || store.lastReopen.Reason != "Kunde möchte neu planen" {
		t.Fatalf("reopen response/calls/input = %d %q/%d/%#v", response.Code, response.Body.String(), store.reopenCalls, store.lastReopen)
	}
}

func TestAppointmentHTTPDriverCannotReopenAppointment(t *testing.T) {
	store := &appointmentHTTPStore{current: appointment.Appointment{ID: testAppointmentID, Lifecycle: appointment.LifecycleCancelled, Version: 7}}
	router, sessionToken, csrfToken := appointmentTestRouter(t, auth.RoleDriver, store)
	form := url.Values{"csrf_token": {csrfToken}, "version": {"7"}, "reason": {"Nicht erlaubt"}}
	response := httptest.NewRecorder()
	router.ServeHTTP(response, authenticatedCustomerRequest(t, http.MethodPost, "/api/v1/appointments/"+testAppointmentID+"/reopen", form, sessionToken, csrfToken))
	if response.Code != http.StatusForbidden || store.reopenCalls != 0 {
		t.Fatalf("driver reopen status/calls = %d/%d", response.Code, store.reopenCalls)
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

func TestAppointmentDetailReturnsContactOnlyOnAuthenticatedDetailRoute(t *testing.T) {
	store := &appointmentHTTPStore{detail: appointment.Detail{
		CalendarEvent: appointment.CalendarEvent{
			Appointment:  appointment.Appointment{ID: testAppointmentID, JobID: testJobID, JobNumber: "HW-2026-0001", Lifecycle: appointment.LifecycleProposal, Confirmation: appointment.ConfirmationNotRequested, StartsAt: time.Date(2026, 9, 1, 6, 0, 0, 0, time.UTC), EndsAt: time.Date(2026, 9, 1, 9, 0, 0, 0, time.UTC), Version: 4},
			CustomerID:   testCustomerID,
			CustomerName: "Franz Huber", Locality: "Grieskirchen", VolumeM3: "80.00",
		},
		Phone: "+43 664 123456", Email: "franz@example.test", NotificationPreference: "email",
		Notes: []appointment.Note{{AuthorName: "Fahrerin", Body: "Zufahrt geprüft", CreatedAt: time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)}},
	}}
	router, sessionToken, csrfToken := appointmentTestRouter(t, auth.RoleDriver, store)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, authenticatedCustomerRequest(t, http.MethodGet, "/api/v1/appointments/"+testAppointmentID, nil, sessionToken, csrfToken))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "franz@example.test") ||
		!strings.Contains(response.Body.String(), `"notification_channels":["E-Mail"]`) ||
		!strings.Contains(response.Body.String(), `"customer_id":"`+testCustomerID+`"`) ||
		!strings.Contains(response.Body.String(), `"job_id":"`+testJobID+`"`) ||
		!strings.Contains(response.Body.String(), `"Body":"Zufahrt geprüft"`) {
		t.Fatalf("detail response = %d %q", response.Code, response.Body.String())
	}
}

func TestCalendarTemplateShowsReadOnlyNoticeOnlyToDriver(t *testing.T) {
	tests := []struct {
		name       string
		role       auth.Role
		wantNotice bool
	}{
		{name: "admin", role: auth.RoleAdmin, wantNotice: false},
		{name: "driver", role: auth.RoleDriver, wantNotice: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var output bytes.Buffer
			data := templates.CalendarData{
				Shell: templates.ShellData{
					Actor: auth.Actor{Role: test.role},
					Page:  templates.PageData{AppName: "HackWerk"},
				},
				Timezone: "Europe/Vienna",
			}
			if err := templates.Calendar(data).Render(t.Context(), &output); err != nil {
				t.Fatal(err)
			}
			hasNotice := strings.Contains(output.String(), "Nur lesen – Planung nur durch Administration")
			if hasNotice != test.wantNotice {
				t.Fatalf("read-only notice present = %v, want %v", hasNotice, test.wantNotice)
			}
		})
	}
}

func appointmentTestRouter(t *testing.T, role auth.Role, store *appointmentHTTPStore) (http.Handler, string, string) {
	t.Helper()
	now := time.Date(2026, 8, 25, 10, 0, 0, 0, time.UTC)
	// #nosec G101 -- deterministic non-secret test fixture tokens.
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
	cfg := configForWebTest()
	cfg.Mail.Enabled = true
	router, err := NewRouter(Dependencies{Config: cfg, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Database: pinger{}, Build: buildinfo.Info{Version: "test"}, Identity: identity, Appointments: service})
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
	detail          appointment.Detail
	events          []appointment.CalendarEvent
	rescheduleCalls int
	reopenCalls     int
	lastMove        appointment.MoveInput
	lastReopen      appointment.ReopenInput
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
func (store *appointmentHTTPStore) Reschedule(_ context.Context, _ auth.Actor, input appointment.MoveInput, _ string) (appointment.Appointment, error) {
	store.rescheduleCalls++
	store.lastMove = input
	return store.current, nil
}
func (store *appointmentHTTPStore) Fix(context.Context, auth.Actor, appointment.FixInput) (appointment.Appointment, error) {
	return store.current, nil
}
func (store *appointmentHTTPStore) Cancel(context.Context, auth.Actor, appointment.CancelInput) (appointment.Appointment, error) {
	return store.current, nil
}
func (store *appointmentHTTPStore) Reopen(_ context.Context, _ auth.Actor, input appointment.ReopenInput) (appointment.Appointment, error) {
	store.reopenCalls++
	store.lastReopen = input
	value := store.current
	value.Lifecycle = appointment.LifecycleProposal
	value.Version++
	return value, nil
}
func (store *appointmentHTTPStore) Complete(context.Context, auth.Actor, appointment.CompleteInput) (appointment.Appointment, error) {
	return store.current, nil
}
func (store *appointmentHTTPStore) Detail(context.Context, string) (appointment.Detail, error) {
	return store.detail, nil
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
func (store *appointmentHTTPStore) Swap(context.Context, auth.Actor, appointment.SwapInput) ([]appointment.Appointment, error) {
	return []appointment.Appointment{store.current}, nil
}
