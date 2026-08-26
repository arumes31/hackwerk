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
	"example.invalid/hackplan/internal/planning"
)

const planningJobID = "70000000-0000-0000-0000-000000000001"
const planningRunID = "70000000-0000-0000-0000-000000000002"
const planningSuggestionID = "70000000-0000-0000-0000-000000000003"

type planningHTTPStore struct {
	saveCalls, adoptCalls int
	listCalls             int
	now                   time.Time
	run                   planning.Run
	adoptErr              error
}

func (s *planningHTTPStore) LoadSnapshot(context.Context, string, time.Time, time.Time) (planning.Snapshot, error) {
	return planning.Snapshot{Job: planning.Job{ID: planningJobID, Type: "chipping_only", TransportMode: "none", Urgency: "normal", Version: 1, WaitlistVersion: 1, HackMinutes: 60, EnteredAt: s.now.Add(-24 * time.Hour), Location: planning.Point{Latitude: 48.2, Longitude: 14.2}}, Drivers: []planning.Driver{{ID: "70000000-0000-0000-0000-000000000010", Name: "Anna"}}, Resources: []planning.Resource{{ID: "70000000-0000-0000-0000-000000000020", Name: "Hackmaschine 1", Type: "chipper", Exclusive: true}}}, nil
}
func (s *planningHTTPStore) SaveRun(_ context.Context, _ auth.Actor, snapshot planning.Snapshot, _ time.Time, _ time.Time, values []planning.Suggestion, _ planning.Config) (planning.Run, error) {
	s.saveCalls++
	for i := range values {
		values[i].ID = planningSuggestionID
		values[i].RunID = planningRunID
	}
	return planning.Run{ID: planningRunID, JobID: snapshot.Job.ID, Suggestions: values}, nil
}
func (s *planningHTTPStore) ListRun(context.Context, string) (planning.Run, error) {
	s.listCalls++
	if s.run.ID != "" {
		return s.run, nil
	}
	return planning.Run{ID: planningRunID, JobID: planningJobID}, nil
}
func (s *planningHTTPStore) Adopt(context.Context, auth.Actor, string, string) (string, error) {
	s.adoptCalls++
	return testAppointmentID, s.adoptErr
}
func (s *planningHTTPStore) ClusterEntries(context.Context) ([]planning.ClusterEntry, error) {
	return nil, nil
}

type planningHTTPAvailability struct{ now time.Time }

func (a planningHTTPAvailability) Resolve(context.Context, auth.Actor, string, time.Time, time.Time) ([]planning.Interval, error) {
	return []planning.Interval{{StartsAt: a.now.Add(-time.Hour), EndsAt: a.now.AddDate(0, 0, 10), Status: "available"}}, nil
}

func TestPlanningHTTPAdminCreatesSuggestionsWithoutAppointmentMutation(t *testing.T) {
	store := &planningHTTPStore{now: time.Date(2026, 8, 31, 4, 0, 0, 0, time.UTC)}
	router, session, csrf := planningTestRouter(t, auth.RoleAdmin, store)
	form := url.Values{"csrf_token": {csrf}, "job_id": {planningJobID}}
	response := httptest.NewRecorder()
	router.ServeHTTP(response, authenticatedCustomerRequest(t, http.MethodPost, "/api/v1/planning/suggestions", form, session, csrf))
	if response.Code != http.StatusCreated || store.saveCalls != 1 || store.adoptCalls != 0 || !strings.Contains(response.Body.String(), planningRunID) {
		t.Fatalf("response=%d %s calls=%d/%d", response.Code, response.Body.String(), store.saveCalls, store.adoptCalls)
	}
}

func TestPlanningHTTPDriverCannotCalculateOrAdopt(t *testing.T) {
	store := &planningHTTPStore{now: time.Date(2026, 8, 31, 4, 0, 0, 0, time.UTC)}
	router, session, csrf := planningTestRouter(t, auth.RoleDriver, store)
	pageResponse := httptest.NewRecorder()
	router.ServeHTTP(pageResponse, authenticatedCustomerRequest(t, http.MethodGet, "/planning", nil, session, csrf))
	if pageResponse.Code != http.StatusForbidden || !strings.Contains(pageResponse.Body.String(), "Zugriff verweigert") {
		t.Fatalf("driver planning page=%d %s", pageResponse.Code, pageResponse.Body.String())
	}
	for _, path := range []string{"/api/v1/planning/suggestions", "/api/v1/planning/suggestions/" + planningSuggestionID + "/adopt"} {
		form := url.Values{"csrf_token": {csrf}, "job_id": {planningJobID}}
		response := httptest.NewRecorder()
		router.ServeHTTP(response, authenticatedCustomerRequest(t, http.MethodPost, path, form, session, csrf))
		if response.Code != http.StatusForbidden {
			t.Fatalf("path %s status=%d body=%s", path, response.Code, response.Body.String())
		}
	}
	if store.saveCalls != 0 || store.adoptCalls != 0 {
		t.Fatalf("driver calls=%d/%d", store.saveCalls, store.adoptCalls)
	}
}

func TestPlanningHTTPStaleAdoptionKeepsRunContextForRecalculation(t *testing.T) {
	now := time.Date(2026, 8, 31, 4, 0, 0, 0, time.UTC)
	store := &planningHTTPStore{
		now:      now,
		adoptErr: planning.ErrConflict,
		run: planning.Run{
			ID:    planningRunID,
			JobID: planningJobID,
			Suggestions: []planning.Suggestion{{
				ID:        planningSuggestionID,
				RunID:     planningRunID,
				JobID:     planningJobID,
				Rank:      1,
				StartsAt:  now.Add(24 * time.Hour),
				EndsAt:    now.Add(25 * time.Hour),
				ExpiresAt: now.Add(30 * time.Minute),
				Status:    "pending",
			}},
		},
	}
	router, session, csrf := planningTestRouter(t, auth.RoleAdmin, store)
	form := url.Values{
		"csrf_token": {csrf},
		"run_id":     {planningRunID},
		"job_id":     {planningJobID},
	}
	response := httptest.NewRecorder()
	router.ServeHTTP(
		response,
		authenticatedCustomerRequest(
			t,
			http.MethodPost,
			"/planning/suggestions/"+planningSuggestionID+"/adopt",
			form,
			session,
			csrf,
		),
	)
	body := response.Body.String()
	if response.Code != http.StatusConflict || store.adoptCalls != 1 || store.listCalls != 1 {
		t.Fatalf("stale adoption=%d calls=%d/%d body=%s", response.Code, store.adoptCalls, store.listCalls, body)
	}
	for _, expected := range []string{planningRunID, planningJobID, "Status: veraltet", "Neu berechnen"} {
		if !strings.Contains(body, expected) {
			t.Fatalf("stale adoption missing %q: %s", expected, body)
		}
	}
	if !strings.Contains(body, "disabled") {
		t.Fatalf("stale suggestion remains actionable: %s", body)
	}
}

func planningTestRouter(t *testing.T, role auth.Role, store *planningHTTPStore) (http.Handler, string, string) {
	t.Helper()
	now := store.now
	sessionToken, csrfToken := "planning-session", "planning-csrf"
	identityStore := &identityTestStore{user: auth.User{ID: "40000000-0000-0000-0000-000000000001", Username: "intern", DisplayName: "Intern", Role: role, Active: true, Version: 1}, session: auth.Session{ID: "session", Actor: auth.Actor{UserID: "40000000-0000-0000-0000-000000000001", Username: "intern", DisplayName: "Intern", Role: role, UserVersion: 1}, CSRFTokenHash: auth.TokenHash(csrfToken), IdleExpiresAt: now.Add(time.Hour), AbsoluteExpiresAt: now.Add(8 * time.Hour), UserActive: true}}
	hasher, _ := auth.NewPasswordHasher(auth.PasswordParameters{MemoryKiB: 8, Iterations: 1, Parallelism: 1, SaltLength: 16, KeyLength: 16, MinLength: 14})
	identity, err := auth.NewService(identityStore, hasher, func() time.Time { return now }, time.Hour, 8*time.Hour, 5)
	if err != nil {
		t.Fatal(err)
	}
	location, _ := time.LoadLocation("Europe/Vienna")
	cfg := planning.DefaultConfig(location)
	cfg.HorizonDays = 7
	cfg.CandidateLimit = 100
	service, err := planning.New(store, planningHTTPAvailability{now: now}, planning.NewHaversineRouter(1.3, 55), cfg, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	webCfg := configForWebTest()
	router, err := NewRouter(Dependencies{Config: webCfg, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Database: pinger{}, Build: buildinfo.Info{Version: "test"}, Identity: identity, Planning: service})
	if err != nil {
		t.Fatal(err)
	}
	return router, sessionToken, csrfToken
}
