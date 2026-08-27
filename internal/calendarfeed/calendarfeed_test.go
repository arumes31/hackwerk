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
	createdHash  []byte
	feed         Feed
	events       []Event
	query        Query
	touched      bool
	createdOwner string
	createdInput CreateInput
	createError  error
	listOwner    string
	listError    error
	rotatedOwner string
	rotatedID    string
	rotatedVer   int32
	rotatedHash  []byte
	rotateError  error
	revokedOwner string
	revokedID    string
	revokedVer   int32
	revokeError  error
	tokenHash    []byte
	lookupError  error
	touchError   error
	eventsError  error
}

func (store *feedStoreFake) Create(_ context.Context, owner string, hash []byte, input CreateInput) (Feed, error) {
	store.createdOwner = owner
	store.createdHash = append([]byte(nil), hash...)
	store.createdInput = input
	store.feed = Feed{ID: "feed", OwnerUserID: owner, Name: input.Name, Scope: input.Scope, Detail: input.Detail, ResourceTypes: input.ResourceTypes, Active: true, OwnerActive: true, Version: 1, TokenVersion: 1}
	return store.feed, store.createError
}
func (store *feedStoreFake) List(_ context.Context, owner string) ([]Feed, error) {
	store.listOwner = owner
	return []Feed{store.feed}, store.listError
}
func (store *feedStoreFake) Rotate(_ context.Context, owner string, id string, version int32, hash []byte) (Feed, error) {
	store.rotatedOwner, store.rotatedID, store.rotatedVer = owner, id, version
	store.rotatedHash = append([]byte(nil), hash...)
	return store.feed, store.rotateError
}
func (store *feedStoreFake) Revoke(_ context.Context, owner string, id string, version int32) (Feed, error) {
	store.revokedOwner, store.revokedID, store.revokedVer = owner, id, version
	return store.feed, store.revokeError
}
func (store *feedStoreFake) ByTokenHash(_ context.Context, hash []byte) (Feed, error) {
	store.tokenHash = append([]byte(nil), hash...)
	return store.feed, store.lookupError
}
func (store *feedStoreFake) Touch(context.Context, string, time.Time) error {
	store.touched = true
	return store.touchError
}
func (store *feedStoreFake) Events(_ context.Context, query Query) ([]Event, error) {
	store.query = query
	return store.events, store.eventsError
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

func TestManageFeedsListRotateAndRevoke(t *testing.T) {
	t.Parallel()
	store := &feedStoreFake{feed: Feed{ID: "feed", OwnerUserID: "driver", Name: "Touren", Active: true, OwnerActive: true, Version: 2}}
	now := time.Date(2026, 8, 25, 10, 0, 0, 0, time.UTC)
	service, err := New(store, testConfig(), func() time.Time { return now }, func() (string, error) { return "rotated-token-material-with-more-than-forty-characters", nil })
	if err != nil {
		t.Fatal(err)
	}
	driver := auth.Actor{UserID: "driver", Role: auth.RoleDriver}
	feeds, err := service.List(t.Context(), driver)
	if err != nil || len(feeds) != 1 || store.listOwner != "driver" {
		t.Fatalf("List() = %#v, %v; owner=%q", feeds, err, store.listOwner)
	}
	material, err := service.Rotate(t.Context(), driver, "feed", 2)
	if err != nil || material.RawToken == "" || store.rotatedOwner != "driver" || store.rotatedID != "feed" || store.rotatedVer != 2 {
		t.Fatalf("Rotate() = %#v, %v; store=%#v", material, err, store)
	}
	if bytes.Equal(store.rotatedHash, []byte(material.RawToken)) || !bytes.Equal(store.rotatedHash, auth.TokenHash(material.RawToken)) {
		t.Fatal("Rotate() persisted the raw token or an incorrect hash")
	}
	if err := service.Revoke(t.Context(), driver, "feed", 2); err != nil || store.revokedOwner != "driver" || store.revokedID != "feed" || store.revokedVer != 2 {
		t.Fatalf("Revoke() = %v; store=%#v", err, store)
	}
}

func TestFeedManagementAuthorizationValidationAndStoreErrors(t *testing.T) {
	t.Parallel()
	store := &feedStoreFake{listError: errors.New("list failed"), rotateError: errors.New("rotate failed"), revokeError: errors.New("revoke failed"), createError: errors.New("create failed")}
	service, err := New(store, testConfig(), time.Now, func() (string, error) { return "token-material-with-more-than-forty-characters-123", nil })
	if err != nil {
		t.Fatal(err)
	}
	admin := auth.Actor{UserID: "admin", Role: auth.RoleAdmin}
	guest := auth.Actor{Role: auth.RoleDriver}
	if _, err := service.List(t.Context(), guest); !errors.Is(err, auth.ErrForbidden) {
		t.Fatalf("guest List() error = %v", err)
	}
	if _, err := service.List(t.Context(), admin); err == nil {
		t.Fatal("List() accepted a store error")
	}
	for _, input := range []CreateInput{
		{Name: "", Scope: ScopeAll, Detail: DetailInternal},
		{Name: "Test", Scope: "everybody", Detail: DetailInternal},
		{Name: "Test", Scope: ScopeAll, Detail: "full"},
		{Name: "Test", Scope: ScopeAll, Detail: DetailInternal, ResourceTypes: []string{"invalid"}},
	} {
		if _, err := service.Create(t.Context(), admin, input); !errors.Is(err, ErrInvalid) {
			t.Fatalf("Create(%#v) error = %v", input, err)
		}
	}
	if _, err := service.Create(t.Context(), admin, CreateInput{Name: "Test"}); err == nil {
		t.Fatal("Create() accepted a store error")
	}
	for _, version := range []int32{0, -1} {
		if _, err := service.Rotate(t.Context(), admin, "feed", version); !errors.Is(err, ErrInvalid) {
			t.Fatalf("Rotate() version %d error = %v", version, err)
		}
		if err := service.Revoke(t.Context(), admin, "feed", version); !errors.Is(err, ErrInvalid) {
			t.Fatalf("Revoke() version %d error = %v", version, err)
		}
	}
	if _, err := service.Rotate(t.Context(), admin, "", 1); !errors.Is(err, ErrInvalid) {
		t.Fatalf("empty Rotate() error = %v", err)
	}
	if err := service.Revoke(t.Context(), admin, "", 1); !errors.Is(err, ErrInvalid) {
		t.Fatalf("empty Revoke() error = %v", err)
	}
	if _, err := service.Rotate(t.Context(), admin, "feed", 1); err == nil {
		t.Fatal("Rotate() accepted a store error")
	}
	if err := service.Revoke(t.Context(), admin, "feed", 1); err == nil {
		t.Fatal("Revoke() accepted a store error")
	}
}

func TestExportUsesAdminScopeDatesAndDetail(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 25, 10, 0, 0, 0, time.UTC)
	store := &feedStoreFake{}
	service, err := New(store, testConfig(), func() time.Time { return now }, nil)
	if err != nil {
		t.Fatal(err)
	}
	admin := auth.Actor{UserID: "admin", Role: auth.RoleAdmin}
	from, to := now.In(time.FixedZone("local", 2*60*60)), now.Add(24*time.Hour).In(time.FixedZone("local", 2*60*60))
	calendar, err := service.Export(t.Context(), admin, from, to, DetailMinimal)
	if err != nil || calendar.ETag == "" || store.query.OwnerUserID != "admin" || store.query.Scope != ScopeAll || !store.query.From.Equal(from.UTC()) || !store.query.To.Equal(to.UTC()) {
		t.Fatalf("Export() = %#v, %v; query=%#v", calendar, err, store.query)
	}
	if strings.Contains(string(calendar.Bytes), "HackWerk, Intern") {
		t.Fatal("minimal export used the internal detail unexpectedly")
	}
	if _, err := service.Export(t.Context(), auth.Actor{}, from, to, DetailInternal); !errors.Is(err, auth.ErrForbidden) {
		t.Fatalf("anonymous Export() error = %v", err)
	}
	for _, rangeCase := range [][2]time.Time{{time.Time{}, to}, {to, from}, {from, from.Add(367 * 24 * time.Hour)}} {
		if _, err := service.Export(t.Context(), admin, rangeCase[0], rangeCase[1], DetailInternal); !errors.Is(err, ErrInvalid) {
			t.Fatalf("invalid Export() range %v..%v error = %v", rangeCase[0], rangeCase[1], err)
		}
	}
	store.eventsError = errors.New("events failed")
	if _, err := service.Export(t.Context(), admin, from, to, DetailInternal); err == nil {
		t.Fatal("Export() accepted an events error")
	}
}

func TestPublicTokenValidationAndFailures(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 25, 10, 0, 0, 0, time.UTC)
	token := strings.Repeat("t", 43)
	validFeed := Feed{ID: "feed", OwnerUserID: "owner", Name: "Privat", Scope: ScopeOwn, Detail: DetailMinimal, Active: true, OwnerActive: true, UpdatedAt: now}
	tests := []struct {
		name   string
		feed   Feed
		mutate func(*feedStoreFake)
		want   error
	}{
		{name: "lookup failure", feed: validFeed, mutate: func(s *feedStoreFake) { s.lookupError = errors.New("missing") }, want: ErrNotFound},
		{name: "inactive feed", feed: Feed{Active: false, OwnerActive: true}, mutate: func(*feedStoreFake) {}, want: ErrNotFound},
		{name: "expired", feed: Feed{Active: true, OwnerActive: true, ExpiresAt: now}, mutate: func(*feedStoreFake) {}, want: ErrNotFound},
		{name: "events failure", feed: validFeed, mutate: func(s *feedStoreFake) { s.eventsError = errors.New("events") }, want: nil},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := &feedStoreFake{feed: test.feed}
			test.mutate(store)
			service, err := New(store, testConfig(), func() time.Time { return now }, nil)
			if err != nil {
				t.Fatal(err)
			}
			_, err = service.Public(t.Context(), token)
			if test.want != nil {
				if !errors.Is(err, test.want) {
					t.Fatalf("Public() error = %v, want %v", err, test.want)
				}
			} else if err == nil {
				t.Fatal("Public() accepted an events error")
			}
		})
	}
	store := &feedStoreFake{feed: validFeed}
	service, _ := New(store, testConfig(), func() time.Time { return now }, nil)
	for _, raw := range []string{"", strings.Repeat("a", 39), strings.Repeat("a", 101)} {
		if _, err := service.Public(t.Context(), raw); !errors.Is(err, ErrNotFound) {
			t.Fatalf("Public(%q) error = %v", raw, err)
		}
	}
	if _, err := service.Public(t.Context(), token); err != nil || !bytes.Equal(store.tokenHash, auth.TokenHash(token)) {
		t.Fatalf("Public() = %v; hash=%x", err, store.tokenHash)
	}
}

func testConfig() Config {
	return Config{BaseURL: "https://hackwerk.example", UIDDomain: "hackwerk.example", CalendarName: "HackWerk", ExportMaxDays: 366, HistoryDays: 90, FutureDays: 366}
}
