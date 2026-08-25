package driver

import (
	"context"
	"errors"
	"testing"
	"time"

	"example.invalid/hackplan/internal/auth"
)

type storeStub struct{ availability Availability }

func (s *storeStub) ListProfiles(context.Context) ([]Profile, error) { return nil, nil }
func (s *storeStub) CreateProfile(context.Context, auth.Actor, ProfileInput, string) (string, error) {
	return "driver-id", nil
}
func (s *storeStub) UpdateProfile(context.Context, auth.Actor, string, int32, ProfileInput, string) error {
	return nil
}
func (s *storeStub) DeactivateProfile(context.Context, auth.Actor, string, int32, string) error {
	return nil
}
func (s *storeStub) Availability(context.Context, string, time.Time, time.Time, string, string) (Availability, error) {
	return s.availability, nil
}
func (s *storeStub) Schedule(context.Context, string) (Availability, error) {
	return s.availability, nil
}
func (s *storeStub) CreateRule(context.Context, auth.Actor, string, RuleInput, string) (string, error) {
	return "rule-id", nil
}
func (s *storeStub) UpdateRule(context.Context, auth.Actor, string, string, int32, RuleInput, string) error {
	return nil
}
func (s *storeStub) DeleteRule(context.Context, auth.Actor, string, string, int32, string) error {
	return nil
}
func (s *storeStub) CreateException(context.Context, auth.Actor, string, ExceptionInput, string) (string, error) {
	return "exception-id", nil
}
func (s *storeStub) UpdateException(context.Context, auth.Actor, string, string, int32, ExceptionInput, string) error {
	return nil
}
func (s *storeStub) DeleteException(context.Context, auth.Actor, string, string, int32, string) error {
	return nil
}

func TestResolveAvailabilityCombinesRuleVacationAndGaps(t *testing.T) {
	service := testService(t, Availability{
		Profile: Profile{ID: "driver-id", IsActive: true},
		Rules: []Rule{{
			ID: "monday", Weekday: 1, StartMinute: 8 * 60, EndMinute: 17 * 60,
			ValidFrom: "2026-01-01", Status: RuleAvailable,
		}},
		Exceptions: []Exception{{
			ID: "vacation", Type: ExceptionVacation, IsAllDay: true, LocalDate: "2026-09-07",
		}},
	})
	location, err := time.LoadLocation("Europe/Vienna")
	if err != nil {
		t.Fatal(err)
	}
	localDay := time.Date(2026, 9, 7, 0, 0, 0, 0, location)
	from := localDay.UTC()
	to := localDay.AddDate(0, 0, 1).UTC()
	intervals, err := service.ResolveAvailability(t.Context(), adminActor(), "driver-id", from, to)
	if err != nil {
		t.Fatal(err)
	}
	if len(intervals) != 1 || intervals[0].Status != StatusUnavailable || intervals[0].SourceType != string(ExceptionVacation) {
		t.Fatalf("intervals = %#v", intervals)
	}
}

func TestResolveAvailabilityOverrideOnlyChangesItsWindow(t *testing.T) {
	service := testService(t, Availability{
		Profile: Profile{ID: "driver-id", IsActive: true},
		Exceptions: []Exception{{
			ID: "override", Type: ExceptionAvailableOverride,
			StartsAt: time.Date(2026, 9, 10, 10, 0, 0, 0, time.UTC),
			EndsAt:   time.Date(2026, 9, 10, 15, 0, 0, 0, time.UTC),
		}},
	})
	from := time.Date(2026, 9, 10, 8, 0, 0, 0, time.UTC)
	to := time.Date(2026, 9, 10, 17, 0, 0, 0, time.UTC)
	intervals, err := service.ResolveAvailability(t.Context(), adminActor(), "driver-id", from, to)
	if err != nil {
		t.Fatal(err)
	}
	if len(intervals) != 3 || intervals[0].Status != StatusUnavailable || intervals[1].Status != StatusAvailable || intervals[2].Status != StatusUnavailable {
		t.Fatalf("intervals = %#v", intervals)
	}
}

func TestResolveAvailabilityNegativeExceptionWinsOverOverride(t *testing.T) {
	start := time.Date(2026, 9, 10, 10, 0, 0, 0, time.UTC)
	end := start.Add(5 * time.Hour)
	service := testService(t, Availability{
		Profile: Profile{ID: "driver-id", IsActive: true},
		Exceptions: []Exception{
			{ID: "override", Type: ExceptionAvailableOverride, StartsAt: start, EndsAt: end},
			{ID: "sick", Type: ExceptionSick, StartsAt: start.Add(time.Hour), EndsAt: end.Add(-time.Hour)},
		},
	})
	intervals, err := service.ResolveAvailability(t.Context(), adminActor(), "driver-id", start, end)
	if err != nil {
		t.Fatal(err)
	}
	if len(intervals) != 3 || intervals[1].Status != StatusUnavailable || intervals[1].SourceType != string(ExceptionSick) || intervals[1].Reason == "Krank" {
		t.Fatalf("intervals = %#v", intervals)
	}
}

func TestResolveAvailabilityRejectsDSTGapAndAmbiguity(t *testing.T) {
	tests := []struct {
		name string
		date time.Time
	}{
		{name: "spring gap", date: time.Date(2026, 3, 29, 0, 0, 0, 0, time.UTC)},
		{name: "autumn ambiguity", date: time.Date(2026, 10, 25, 0, 0, 0, 0, time.UTC)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := testService(t, Availability{
				Profile: Profile{ID: "driver-id", IsActive: true},
				Rules: []Rule{{
					ID: "dst", Weekday: 7, StartMinute: 2*60 + 30, EndMinute: 4 * 60,
					ValidFrom: test.date.Format(time.DateOnly), ValidUntil: test.date.Format(time.DateOnly), Status: RuleAvailable,
				}},
			})
			_, err := service.ResolveAvailability(t.Context(), adminActor(), "driver-id", test.date, test.date.Add(24*time.Hour))
			if !errors.Is(err, ErrLocalTime) {
				t.Fatalf("ResolveAvailability() error = %v", err)
			}
		})
	}
}

func TestAllDayExceptionUsesViennaDSTDayLength(t *testing.T) {
	location, err := time.LoadLocation("Europe/Vienna")
	if err != nil {
		t.Fatal(err)
	}
	service := testService(t, Availability{Profile: Profile{ID: "driver-id", IsActive: true}})
	for _, test := range []struct {
		date     string
		expected time.Duration
	}{
		{date: "2026-03-29", expected: 23 * time.Hour},
		{date: "2026-10-25", expected: 25 * time.Hour},
	} {
		exception := Exception{Type: ExceptionVacation, IsAllDay: true, LocalDate: test.date}
		start, end, rangeErr := service.exceptionRange(exception)
		if rangeErr != nil {
			t.Fatal(rangeErr)
		}
		if end.Sub(start) != test.expected || start.In(location).Format(time.DateOnly) != test.date {
			t.Fatalf("%s range = %s to %s (%s)", test.date, start, end, end.Sub(start))
		}
	}
}

func TestDriverCannotTargetOtherAvailability(t *testing.T) {
	service := testService(t, Availability{Profile: Profile{ID: "other", IsActive: true}})
	actor := auth.Actor{UserID: "user", DriverID: "own", Role: auth.RoleDriver}
	_, err := service.CreateRule(t.Context(), actor, "other", validRuleInput(), "request")
	if !errors.Is(err, auth.ErrForbidden) {
		t.Fatalf("CreateRule() error = %v", err)
	}
}

func testService(t *testing.T, availability Availability) *Service {
	t.Helper()
	location, err := time.LoadLocation("Europe/Vienna")
	if err != nil {
		t.Fatal(err)
	}
	service, err := New(&storeStub{availability: availability}, location)
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func adminActor() auth.Actor {
	return auth.Actor{UserID: "admin", Role: auth.RoleAdmin}
}

func validRuleInput() RuleInput {
	return RuleInput{Weekday: 1, LocalStart: "08:00", LocalEnd: "17:00", ValidFrom: "2026-01-01", Status: RuleAvailable}
}
