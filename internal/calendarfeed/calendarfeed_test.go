package calendarfeed

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"example.invalid/hackplan/internal/auth"
	ics "github.com/arran4/golang-ical"
)

type feedStoreFake struct {
	createdHash []byte
	feed        Feed
	events      []Event
	query       Query
	touched     bool
}

func (store *feedStoreFake) Create(_ context.Context, owner string, hash []byte, input CreateInput) (Feed, error) {
	store.createdHash = append([]byte(nil), hash...)
	store.feed = Feed{ID: "feed", OwnerUserID: owner, Name: input.Name, Scope: input.Scope, Detail: input.Detail, ResourceTypes: input.ResourceTypes, Active: true, OwnerActive: true, Version: 1, TokenVersion: 1}
	return store.feed, nil
}
func (store *feedStoreFake) List(context.Context, string) ([]Feed, error) {
	return []Feed{store.feed}, nil
}
func (store *feedStoreFake) Rotate(context.Context, string, string, int32, []byte) (Feed, error) {
	return store.feed, nil
}
func (store *feedStoreFake) Revoke(context.Context, string, string, int32) (Feed, error) {
	return store.feed, nil
}
func (store *feedStoreFake) ByTokenHash(context.Context, []byte) (Feed, error) {
	return store.feed, nil
}
func (store *feedStoreFake) Touch(context.Context, string, time.Time) error {
	store.touched = true
	return nil
}
func (store *feedStoreFake) Events(_ context.Context, query Query) ([]Event, error) {
	store.query = query
	return store.events, nil
}

func TestCreateStoresOnlyHashAndDefaultsToAll(t *testing.T) {
	store := &feedStoreFake{}
	service, _ := New(store, Config{BaseURL: "https://hackwerk.example", UIDDomain: "hackwerk.example", CalendarName: "HackWerk", ExportMaxDays: 366, HistoryDays: 90, FutureDays: 366}, time.Now, func() (string, error) { return "raw-token-material-with-more-than-forty-characters-123", nil })
	material, err := service.Create(t.Context(), auth.Actor{UserID: "user", Role: auth.RoleDriver}, CreateInput{Name: "Mobil"})
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(store.createdHash, []byte(material.RawToken)) || len(store.createdHash) != 32 || material.Feed.Scope != ScopeAll || strings.Contains(string(store.createdHash), "raw-token") {
		t.Fatal("raw token was persisted or defaults are wrong")
	}
	if !strings.Contains(material.URL, "/feeds/") || material.Feed.Detail != DetailInternal {
		t.Fatalf("material = %#v", material)
	}
}

func TestPublicAppliesStoredFilterAndRejectsInactiveOwner(t *testing.T) {
	now := time.Date(2026, 8, 25, 10, 0, 0, 0, time.UTC)
	store := &feedStoreFake{feed: Feed{ID: "feed", OwnerUserID: "owner", Name: "Privat", Scope: ScopeOwn, Detail: DetailMinimal, ResourceTypes: []string{"chipper"}, Active: true, OwnerActive: true, UpdatedAt: now}}
	service, _ := New(store, Config{BaseURL: "https://hackwerk.example", UIDDomain: "hackwerk.example", CalendarName: "HackWerk", ExportMaxDays: 366, HistoryDays: 90, FutureDays: 366}, func() time.Time { return now }, nil)
	if _, err := service.Public(t.Context(), strings.Repeat("a", 43)); err != nil {
		t.Fatal(err)
	}
	if store.query.Scope != ScopeOwn || store.query.OwnerUserID != "owner" || len(store.query.ResourceTypes) != 1 || !store.touched {
		t.Fatalf("public query/touch = %#v/%v", store.query, store.touched)
	}
	store.feed.OwnerActive = false
	if _, err := service.Public(t.Context(), strings.Repeat("b", 43)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("inactive owner error = %v", err)
	}
}

func TestGenerateRFCTextFoldingPrivacyAndStableRevisions(t *testing.T) {
	created := time.Date(2026, 8, 1, 8, 0, 0, 0, time.UTC)
	modified := created.Add(time.Hour)
	event := Event{ID: "8b237be1-29a1-4f6f-84c3-9e0f6804b352", Lifecycle: "cancelled", JobNumber: "HW-2026-1", JobType: "chipping_only", VolumeM3: "80.00", CustomerName: "Müller, Eva; Wald", Street: "Sehr lange Straße mit Umlauten äöü und zusätzlichem Text 123", PostalCode: "4710", Locality: "Grieskirchen", CountryCode: "AT", Latitude: "48.123", Longitude: "13.456", StartsAt: created, EndsAt: created.Add(3 * time.Hour), CreatedAt: created, LastModified: modified, Sequence: 7}
	internal, err := Generate("HackWerk, Intern", "hackwerk.example", "https://hackwerk.example", DetailInternal, []Event{event}, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	text := string(internal.Bytes)
	for _, wanted := range []string{"BEGIN:VCALENDAR\r\n", "UID:" + event.ID + "@hackwerk.example", "DTSTART:20260801T080000Z", "SEQUENCE:7", "STATUS:CANCELLED", "Müller\\, Eva\\; Wald", "GEO:48.123;13.456"} {
		if !strings.Contains(text, wanted) {
			t.Fatalf("ICS missing %q:\n%s", wanted, text)
		}
	}
	if strings.Contains(strings.ReplaceAll(text, "\r\n", ""), "\n") {
		t.Fatal("calendar contains bare newline")
	}
	for _, line := range strings.Split(strings.TrimSuffix(text, "\r\n"), "\r\n") {
		if len([]byte(line)) > 75 {
			t.Fatalf("folded line has %d octets: %q", len([]byte(line)), line)
		}
	}
	minimal, _ := Generate("Minimal", "hackwerk.example", "https://hackwerk.example", DetailMinimal, []Event{event}, modified)
	minimalText := string(minimal.Bytes)
	for _, private := range []string{"Müller", "HW-2026-1", "Grieskirchen", "hackwerk.example/calendar", "GEO:"} {
		if strings.Contains(minimalText, private) {
			t.Fatalf("minimal feed leaked %q", private)
		}
	}
	again, _ := Generate("Minimal", "hackwerk.example", "https://hackwerk.example", DetailMinimal, []Event{event}, modified)
	if minimal.ETag != again.ETag || !bytes.Equal(minimal.Bytes, again.Bytes) {
		t.Fatal("unchanged calendar is not stable")
	}
}

func TestGeneratedCalendarParsesWithIndependentLibrary(t *testing.T) {
	start := time.Date(2026, 9, 1, 6, 0, 0, 0, time.UTC)
	value, err := Generate("HackWerk", "hackwerk.example", "https://hackwerk.example", DetailInternal, []Event{{
		ID: "8b237be1-29a1-4f6f-84c3-9e0f6804b352", Lifecycle: "fixed", JobNumber: "HW-1", JobType: "chipping_only", VolumeM3: "80",
		StartsAt: start, EndsAt: start.Add(3 * time.Hour), CreatedAt: start.Add(-time.Hour), LastModified: start, Sequence: 3,
	}}, start)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := ics.ParseCalendar(strings.NewReader(string(value.Bytes)))
	if err != nil {
		t.Fatalf("golang-ical v0.3.2 parse: %v", err)
	}
	events := parsed.Events()
	if len(events) != 1 || events[0].Id() != "8b237be1-29a1-4f6f-84c3-9e0f6804b352@hackwerk.example" {
		t.Fatalf("parsed events = %#v", events)
	}
	parsedStart, err := events[0].GetStartAt()
	if err != nil || !parsedStart.Equal(start) {
		t.Fatalf("parsed DTSTART = %s/%v", parsedStart, err)
	}
}
