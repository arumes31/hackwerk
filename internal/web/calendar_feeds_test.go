package web

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"example.invalid/hackplan/internal/auth"
	"example.invalid/hackplan/internal/calendarfeed"
	"example.invalid/hackplan/web/templates"
	"github.com/go-chi/chi/v5"
)

type calendarFeedHTTPStore struct {
	feed      calendarfeed.Feed
	events    []calendarfeed.Event
	createErr error
	listErr   error
	rotateErr error
	revokeErr error
	tokenErr  error
	eventsErr error
}

func (store *calendarFeedHTTPStore) Create(context.Context, string, []byte, calendarfeed.CreateInput) (calendarfeed.Feed, error) {
	return store.feed, store.createErr
}
func (store *calendarFeedHTTPStore) List(context.Context, string) ([]calendarfeed.Feed, error) {
	return []calendarfeed.Feed{store.feed}, store.listErr
}
func (store *calendarFeedHTTPStore) Rotate(context.Context, string, string, int32, []byte) (calendarfeed.Feed, error) {
	return store.feed, store.rotateErr
}
func (store *calendarFeedHTTPStore) Revoke(context.Context, string, string, int32) (calendarfeed.Feed, error) {
	return store.feed, store.revokeErr
}
func (store *calendarFeedHTTPStore) ByTokenHash(context.Context, []byte) (calendarfeed.Feed, error) {
	return store.feed, store.tokenErr
}
func (store *calendarFeedHTTPStore) Touch(context.Context, string, time.Time) error { return nil }
func (store *calendarFeedHTTPStore) Events(context.Context, calendarfeed.Query) ([]calendarfeed.Event, error) {
	return store.events, store.eventsErr
}

func calendarFeedHTTPService(t *testing.T, store *calendarFeedHTTPStore, now time.Time) *calendarfeed.Service {
	t.Helper()
	service, err := calendarfeed.New(store, calendarfeed.Config{BaseURL: "https://hackwerk.example", UIDDomain: "hackwerk.example", CalendarName: "HackWerk", ExportMaxDays: 366, HistoryDays: 90, FutureDays: 366}, func() time.Time { return now }, func() (string, error) { return strings.Repeat("t", 43), nil })
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func calendarFeedHTTPRequest(t *testing.T, method, target string, form url.Values) *http.Request {
	t.Helper()
	var body *strings.Reader
	if form == nil {
		body = strings.NewReader("")
	} else {
		body = strings.NewReader(form.Encode())
	}
	request := httptest.NewRequestWithContext(t.Context(), method, target, body)
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if err := request.ParseForm(); err != nil {
		t.Fatal(err)
	}
	return request.WithContext(context.WithValue(request.Context(), sessionContextKey{}, auth.Session{Actor: auth.Actor{UserID: "driver", Role: auth.RoleDriver}}))
}

func TestPublicCalendarFeedHeadersConditionalRequestAndTokenRedaction(t *testing.T) {
	now := time.Date(2026, 8, 25, 10, 0, 0, 0, time.UTC)
	raw := strings.Repeat("s", 43)
	store := &calendarFeedHTTPStore{feed: calendarfeed.Feed{ID: "feed", OwnerUserID: "owner", Name: "Minimal", Scope: calendarfeed.ScopeAll, Detail: calendarfeed.DetailMinimal, Active: true, OwnerActive: true, UpdatedAt: now}, events: []calendarfeed.Event{{ID: "8b237be1-29a1-4f6f-84c3-9e0f6804b352", Lifecycle: "fixed", JobType: "chipping_only", VolumeM3: "80", StartsAt: now, EndsAt: now.Add(time.Hour), CreatedAt: now, LastModified: now, Sequence: 1}}}
	service, _ := calendarfeed.New(store, calendarfeed.Config{BaseURL: "https://hackwerk.example", UIDDomain: "hackwerk.example", CalendarName: "HackWerk", ExportMaxDays: 366, HistoryDays: 90, FutureDays: 366}, func() time.Time { return now }, nil)
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	router := chi.NewRouter()
	router.Use(requestLogger(logger, nil))
	router.Get("/feeds/{calendarFeedToken}/calendar.ics", publicCalendarFeed(service, newConfirmationRateLimiter(10, func() time.Time { return now }), logger))

	request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/feeds/"+raw+"/calendar.ics", nil)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Header().Get("Content-Type") != "text/calendar; charset=utf-8" || response.Header().Get("ETag") == "" || response.Header().Get("Set-Cookie") != "" || !strings.Contains(response.Header().Get("Cache-Control"), "private") {
		t.Fatalf("feed response = %d %#v", response.Code, response.Header())
	}
	if strings.Contains(logs.String(), raw) || !strings.Contains(logs.String(), "/feeds/{calendarFeedToken}/calendar.ics") {
		t.Fatalf("unsafe feed log = %s", logs.String())
	}
	conditional := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/feeds/"+raw+"/calendar.ics", nil)
	conditional.Header.Set("If-None-Match", response.Header().Get("ETag"))
	conditionalResponse := httptest.NewRecorder()
	router.ServeHTTP(conditionalResponse, conditional)
	if conditionalResponse.Code != http.StatusNotModified || conditionalResponse.Body.Len() != 0 {
		t.Fatalf("conditional response = %d %q", conditionalResponse.Code, conditionalResponse.Body.String())
	}
}

func TestCalendarExportRangeAndMinimalPrivacy(t *testing.T) {
	now := time.Date(2026, 8, 25, 10, 0, 0, 0, time.UTC)
	store := &calendarFeedHTTPStore{events: []calendarfeed.Event{{ID: "8b237be1-29a1-4f6f-84c3-9e0f6804b352", Lifecycle: "fixed", JobNumber: "SECRET-JOB", JobType: "chipping_only", VolumeM3: "80", CustomerName: "Private Person", Street: "Secret Street", StartsAt: now, EndsAt: now.Add(time.Hour), CreatedAt: now, LastModified: now, Sequence: 1}}}
	service, _ := calendarfeed.New(store, calendarfeed.Config{BaseURL: "https://hackwerk.example", UIDDomain: "hackwerk.example", CalendarName: "HackWerk", ExportMaxDays: 31, HistoryDays: 90, FutureDays: 366}, func() time.Time { return now }, nil)
	request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/calendar/export.ics?from=2026-08-01&to=2026-08-20&detail=minimal", nil)
	request = request.WithContext(context.WithValue(request.Context(), sessionContextKey{}, auth.Session{Actor: auth.Actor{UserID: "driver", Role: auth.RoleDriver}}))
	response := httptest.NewRecorder()
	calendarExport(service, "Europe/Vienna", slog.Default()).ServeHTTP(response, request)
	if response.Code != http.StatusOK || strings.Contains(response.Body.String(), "Private Person") || strings.Contains(response.Body.String(), "SECRET-JOB") {
		t.Fatalf("minimal export = %d %s", response.Code, response.Body.String())
	}
	badRequest := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/calendar/export.ics?from=2026-01-01&to=2026-12-31", nil)
	badRequest = badRequest.WithContext(context.WithValue(badRequest.Context(), sessionContextKey{}, auth.Session{Actor: auth.Actor{UserID: "driver", Role: auth.RoleDriver}}))
	bad := httptest.NewRecorder()
	calendarExport(service, "Europe/Vienna", slog.Default()).ServeHTTP(bad, badRequest)
	if bad.Code != http.StatusUnprocessableEntity {
		t.Fatalf("unbounded export = %d", bad.Code)
	}
}

func TestCalendarFeedAPIHappyPaths(t *testing.T) {
	now := time.Date(2026, 8, 25, 10, 0, 0, 0, time.UTC)
	store := &calendarFeedHTTPStore{feed: calendarfeed.Feed{ID: "feed-1", OwnerUserID: "driver", Name: "Meine Termine", Scope: calendarfeed.ScopeOwn, Detail: calendarfeed.DetailMinimal, TokenVersion: 2, Version: 3, Active: true, OwnerActive: true}}
	service := calendarFeedHTTPService(t, store, now)
	logger := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))
	router := chi.NewRouter()
	router.Get("/feeds", listCalendarFeedsAPI(service, logger))
	router.Post("/feeds", createCalendarFeedAPI(service, logger))
	router.Post("/feeds/{calendarFeedID}/rotate", rotateCalendarFeedAPI(service, logger))
	router.Delete("/feeds/{calendarFeedID}", revokeCalendarFeedAPI(service, logger))

	tests := []struct {
		name   string
		method string
		target string
		form   url.Values
		status int
		body   string
	}{
		{name: "list", method: http.MethodGet, target: "/feeds", status: http.StatusOK, body: `"feeds"`},
		{name: "create", method: http.MethodPost, target: "/feeds", form: url.Values{"name": {"Meine Termine"}, "scope": {calendarfeed.ScopeOwn}, "detail": {calendarfeed.DetailMinimal}}, status: http.StatusCreated, body: `"token_version":2`},
		{name: "rotate", method: http.MethodPost, target: "/feeds/feed-1/rotate", form: url.Values{"version": {"3"}}, status: http.StatusOK, body: `"version":3`},
		{name: "revoke", method: http.MethodDelete, target: "/feeds/feed-1?version=3", status: http.StatusNoContent},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			router.ServeHTTP(response, calendarFeedHTTPRequest(t, test.method, test.target, test.form))
			if response.Code != test.status || (test.body != "" && !strings.Contains(response.Body.String(), test.body)) {
				t.Fatalf("response = %d %q", response.Code, response.Body.String())
			}
		})
	}
}

func TestCalendarFeedAPIErrorsAreMapped(t *testing.T) {
	now := time.Date(2026, 8, 25, 10, 0, 0, 0, time.UTC)
	logger := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))
	tests := []struct {
		name   string
		store  *calendarFeedHTTPStore
		method string
		target string
		form   url.Values
		status int
		code   string
	}{
		{name: "list internal", store: &calendarFeedHTTPStore{listErr: errors.New("database")}, method: http.MethodGet, target: "/feeds", status: http.StatusInternalServerError, code: "internal_error"},
		{name: "create invalid", store: &calendarFeedHTTPStore{}, method: http.MethodPost, target: "/feeds", form: url.Values{"name": {""}}, status: http.StatusUnprocessableEntity, code: "validation_failed"},
		{name: "rotate invalid version", store: &calendarFeedHTTPStore{}, method: http.MethodPost, target: "/feeds/feed-1/rotate", form: url.Values{"version": {"x"}}, status: http.StatusUnprocessableEntity, code: "validation_failed"},
		{name: "rotate conflict", store: &calendarFeedHTTPStore{rotateErr: calendarfeed.ErrConflict}, method: http.MethodPost, target: "/feeds/feed-1/rotate", form: url.Values{"version": {"1"}}, status: http.StatusConflict, code: "version_conflict"},
		{name: "revoke invalid version", store: &calendarFeedHTTPStore{}, method: http.MethodDelete, target: "/feeds/feed-1?version=0", status: http.StatusUnprocessableEntity, code: "validation_failed"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := calendarFeedHTTPService(t, test.store, now)
			router := chi.NewRouter()
			router.Get("/feeds", listCalendarFeedsAPI(service, logger))
			router.Post("/feeds", createCalendarFeedAPI(service, logger))
			router.Post("/feeds/{calendarFeedID}/rotate", rotateCalendarFeedAPI(service, logger))
			router.Delete("/feeds/{calendarFeedID}", revokeCalendarFeedAPI(service, logger))
			response := httptest.NewRecorder()
			router.ServeHTTP(response, calendarFeedHTTPRequest(t, test.method, test.target, test.form))
			if response.Code != test.status || !strings.Contains(response.Body.String(), test.code) {
				t.Fatalf("response = %d %q", response.Code, response.Body.String())
			}
		})
	}
}

func TestCalendarFeedHTMLHandlersRenderAndMapMutations(t *testing.T) {
	now := time.Date(2026, 8, 25, 10, 0, 0, 0, time.UTC)
	page := templates.PageData{AppName: "HackWerk", Version: "test"}
	logger := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))
	feed := calendarfeed.Feed{
		ID: "feed-1", OwnerUserID: "driver", Name: "Meine Termine", Scope: calendarfeed.ScopeOwn,
		Detail: calendarfeed.DetailMinimal, TokenVersion: 2, Version: 3, Active: true, OwnerActive: true,
	}

	tests := []struct {
		name      string
		store     *calendarFeedHTTPStore
		method    string
		target    string
		form      url.Values
		want      int
		wantBody  string
		wantRoute string
	}{
		{
			name: "page renders feeds", store: &calendarFeedHTTPStore{feed: feed}, method: http.MethodGet, target: "/calendar/feeds",
			want: http.StatusOK, wantBody: "Meine Termine",
		},
		{
			name: "page reports unavailable list", store: &calendarFeedHTTPStore{listErr: errors.New("database")}, method: http.MethodGet, target: "/calendar/feeds",
			want: http.StatusInternalServerError, wantBody: "Kalenderfeeds nicht verfügbar",
		},
		{
			name: "create renders material", store: &calendarFeedHTTPStore{feed: feed}, method: http.MethodPost, target: "/calendar/feeds",
			form: url.Values{"name": {"Meine Termine"}, "scope": {calendarfeed.ScopeOwn}, "detail": {calendarfeed.DetailMinimal}, "resource_type": {"chipper"}},
			want: http.StatusOK, wantBody: "Privaten Link sicher speichern",
		},
		{
			name: "invalid create retains form error", store: &calendarFeedHTTPStore{feed: feed}, method: http.MethodPost, target: "/calendar/feeds",
			form: url.Values{"name": {""}, "scope": {calendarfeed.ScopeOwn}, "detail": {calendarfeed.DetailMinimal}},
			want: http.StatusUnprocessableEntity, wantBody: "ungültig oder veraltet",
		},
		{
			name: "rotate invalid version", store: &calendarFeedHTTPStore{feed: feed}, method: http.MethodPost, target: "/calendar/feeds/feed-1/rotate",
			form: url.Values{"version": {"0"}}, want: http.StatusUnprocessableEntity, wantBody: "ungültig oder veraltet",
		},
		{
			name: "rotate conflict", store: &calendarFeedHTTPStore{feed: feed, rotateErr: calendarfeed.ErrConflict}, method: http.MethodPost, target: "/calendar/feeds/feed-1/rotate",
			form: url.Values{"version": {"3"}}, want: http.StatusUnprocessableEntity, wantBody: "ungültig oder veraltet",
		},
		{
			name: "revoke redirects", store: &calendarFeedHTTPStore{feed: feed}, method: http.MethodPost, target: "/calendar/feeds/feed-1/revoke",
			form: url.Values{"version": {"3"}}, want: http.StatusSeeOther, wantRoute: "/calendar/feeds",
		},
		{
			name: "revoke internal error", store: &calendarFeedHTTPStore{feed: feed, revokeErr: errors.New("database")}, method: http.MethodPost, target: "/calendar/feeds/feed-1/revoke",
			form: url.Values{"version": {"3"}}, want: http.StatusInternalServerError, wantBody: "derzeit nicht gespeichert",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := calendarFeedHTTPService(t, test.store, now)
			router := chi.NewRouter()
			router.Get("/calendar/feeds", calendarFeedPage(service, page, "csrf", logger, nil))
			router.Post("/calendar/feeds", createCalendarFeed(service, page, "csrf", logger))
			router.Post("/calendar/feeds/{calendarFeedID}/rotate", rotateCalendarFeed(service, page, "csrf", logger))
			router.Post("/calendar/feeds/{calendarFeedID}/revoke", revokeCalendarFeed(service, page, "csrf", logger))
			response := httptest.NewRecorder()
			router.ServeHTTP(response, calendarFeedHTTPRequest(t, test.method, test.target, test.form))
			if response.Code != test.want {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			if test.wantBody != "" && !strings.Contains(response.Body.String(), test.wantBody) {
				t.Fatalf("body=%s, want %q", response.Body.String(), test.wantBody)
			}
			if test.wantRoute != "" && response.Header().Get("Location") != test.wantRoute {
				t.Fatalf("redirect=%q", response.Header().Get("Location"))
			}
		})
	}
}

func TestCalendarExportAndPublicFeedErrors(t *testing.T) {
	now := time.Date(2026, 8, 25, 10, 0, 0, 0, time.UTC)
	logger := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))
	service := calendarFeedHTTPService(t, &calendarFeedHTTPStore{tokenErr: calendarfeed.ErrNotFound}, now)

	badTimezone := httptest.NewRecorder()
	calendarExport(service, "invalid/timezone", logger).ServeHTTP(badTimezone, calendarFeedHTTPRequest(t, http.MethodGet, "/calendar/export.ics?from=2026-08-01&to=2026-08-02", nil))
	if badTimezone.Code != http.StatusInternalServerError {
		t.Fatalf("bad timezone status = %d", badTimezone.Code)
	}
	badDate := httptest.NewRecorder()
	calendarExport(service, "Europe/Vienna", logger).ServeHTTP(badDate, calendarFeedHTTPRequest(t, http.MethodGet, "/calendar/export.ics?from=x&to=2026-08-02", nil))
	if badDate.Code != http.StatusUnprocessableEntity {
		t.Fatalf("bad date status = %d", badDate.Code)
	}

	router := chi.NewRouter()
	router.Get("/feeds/{calendarFeedToken}/calendar.ics", publicCalendarFeed(service, newConfirmationRateLimiter(1, func() time.Time { return now }), logger))
	missing := httptest.NewRecorder()
	router.ServeHTTP(missing, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/feeds/"+strings.Repeat("m", 43)+"/calendar.ics", nil))
	if missing.Code != http.StatusNotFound {
		t.Fatalf("missing feed status = %d", missing.Code)
	}
	limited := httptest.NewRecorder()
	router.ServeHTTP(limited, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/feeds/"+strings.Repeat("m", 43)+"/calendar.ics", nil))
	if limited.Code != http.StatusTooManyRequests || limited.Header().Get("Retry-After") != "60" {
		t.Fatalf("limited feed = %d %#v", limited.Code, limited.Header())
	}
}
