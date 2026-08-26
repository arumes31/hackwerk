//go:build integration

package integration_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"example.invalid/hackplan/internal/adapters/postgres"
	"example.invalid/hackplan/internal/appointment"
	"example.invalid/hackplan/internal/calendarfeed"
)

func TestCalendarFeedHashRotationRevocationOwnerAndFilters(t *testing.T) {
	fixture := newCalendarFixture(t)
	if _, err := fixture.pool.Exec(fixture.ctx, "UPDATE drivers SET user_id=$1 WHERE id=$2", fixture.admin.UserID, fixture.driver1); err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 9, 1, 6, 0, 0, 0, time.UTC)
	first := fixedFeedAppointment(t, fixture, fixture.job(t, "HW-FEED-1"), fixture.driver1, fixture.chipper1, start)
	_ = fixedFeedAppointment(t, fixture, fixture.job(t, "HW-FEED-2"), fixture.driver2, fixture.chipper2, start.AddDate(0, 0, 7))

	tokens := []string{strings.Repeat("a", 43), strings.Repeat("b", 43), strings.Repeat("c", 43)}
	index := 0
	service, err := calendarfeed.New(postgres.NewCalendarFeedStore(fixture.pool), calendarfeed.Config{
		BaseURL: "https://hackwerk.example", UIDDomain: "hackwerk.example", CalendarName: "HackWerk", ExportMaxDays: 366, HistoryDays: 30, FutureDays: 60,
	}, func() time.Time { return start }, func() (string, error) { value := tokens[index]; index++; return value, nil })
	if err != nil {
		t.Fatal(err)
	}
	material, err := service.Create(fixture.ctx, fixture.admin, calendarfeed.CreateInput{Name: "Alle", Scope: calendarfeed.ScopeAll, Detail: calendarfeed.DetailInternal})
	if err != nil {
		t.Fatal(err)
	}
	var storedHex string
	if err := fixture.pool.QueryRow(fixture.ctx, "SELECT encode(token_hash,'hex') FROM calendar_feeds WHERE id=$1", material.Feed.ID).Scan(&storedHex); err != nil {
		t.Fatal(err)
	}
	if storedHex == "" || strings.Contains(storedHex, material.RawToken) || material.RawToken != tokens[0] {
		t.Fatal("feed token was not hash-only")
	}
	calendar, err := service.Public(fixture.ctx, tokens[0])
	if err != nil || !strings.Contains(string(calendar.Bytes), first.ID+"@hackwerk.example") || strings.Count(string(calendar.Bytes), "BEGIN:VEVENT") != 2 {
		t.Fatalf("all feed = %v\n%s", err, calendar.Bytes)
	}

	rotated, err := service.Rotate(fixture.ctx, fixture.admin, material.Feed.ID, material.Feed.Version)
	if err != nil || rotated.Feed.TokenVersion != 2 {
		t.Fatalf("rotate = %#v/%v", rotated, err)
	}
	if _, err := service.Public(fixture.ctx, tokens[0]); !errors.Is(err, calendarfeed.ErrNotFound) {
		t.Fatalf("old token error = %v", err)
	}
	if _, err := service.Public(fixture.ctx, tokens[1]); err != nil {
		t.Fatal(err)
	}
	if err := service.Revoke(fixture.ctx, fixture.admin, rotated.Feed.ID, rotated.Feed.Version); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Public(fixture.ctx, tokens[1]); !errors.Is(err, calendarfeed.ErrNotFound) {
		t.Fatalf("revoked token error = %v", err)
	}

	own, err := service.Create(fixture.ctx, fixture.admin, calendarfeed.CreateInput{Name: "Eigene", Scope: calendarfeed.ScopeOwn, Detail: calendarfeed.DetailMinimal, ResourceTypes: []string{"chipper"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.CancelAppointment(fixture.ctx, fixture.admin, appointment.CancelInput{MutateInput: appointment.MutateInput{ID: first.ID, ExpectedVersion: first.Version, RequestID: "feed-cancel"}, Reason: "Wetter"}); err != nil {
		t.Fatal(err)
	}
	ownCalendar, err := service.Public(fixture.ctx, own.RawToken)
	if err != nil || strings.Count(string(ownCalendar.Bytes), "BEGIN:VEVENT") != 1 || !strings.Contains(string(ownCalendar.Bytes), first.ID) || !strings.Contains(string(ownCalendar.Bytes), "STATUS:CANCELLED") {
		t.Fatalf("own feed = %v\n%s", err, ownCalendar.Bytes)
	}
	if _, err := fixture.pool.Exec(fixture.ctx, "UPDATE users SET active=false WHERE id=$1", fixture.admin.UserID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Public(fixture.ctx, own.RawToken); !errors.Is(err, calendarfeed.ErrNotFound) {
		t.Fatalf("inactive owner error = %v", err)
	}
}

func fixedFeedAppointment(t *testing.T, fixture calendarFixture, jobID, driverID, resourceID string, start time.Time) appointment.Appointment {
	t.Helper()
	proposed := fixture.proposal(t, jobID, driverID, resourceID, start, 3*time.Hour)
	fixed, err := fixture.service.FixAppointment(fixture.ctx, fixture.admin, appointment.FixInput{MutateInput: appointment.MutateInput{ID: proposed.ID, ExpectedVersion: proposed.Version, RequestID: "feed-fix"}})
	if err != nil {
		t.Fatal(err)
	}
	return fixed
}
