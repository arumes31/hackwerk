package web

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"example.invalid/hackplan/internal/auth"
	"example.invalid/hackplan/internal/calendarfeed"
	"github.com/go-chi/chi/v5"
)

type calendarFeedHTTPStore struct {
	feed   calendarfeed.Feed
	events []calendarfeed.Event
}

func (store *calendarFeedHTTPStore) Create(context.Context, string, []byte, calendarfeed.CreateInput) (calendarfeed.Feed, error) {
	return store.feed, nil
}
func (store *calendarFeedHTTPStore) List(context.Context, string) ([]calendarfeed.Feed, error) {
	return []calendarfeed.Feed{store.feed}, nil
}
func (store *calendarFeedHTTPStore) Rotate(context.Context, string, string, int32, []byte) (calendarfeed.Feed, error) {
	return store.feed, nil
}
func (store *calendarFeedHTTPStore) Revoke(context.Context, string, string, int32) (calendarfeed.Feed, error) {
	return store.feed, nil
}
func (store *calendarFeedHTTPStore) ByTokenHash(context.Context, []byte) (calendarfeed.Feed, error) {
	return store.feed, nil
}
func (store *calendarFeedHTTPStore) Touch(context.Context, string, time.Time) error { return nil }
func (store *calendarFeedHTTPStore) Events(context.Context, calendarfeed.Query) ([]calendarfeed.Event, error) {
	return store.events, nil
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
