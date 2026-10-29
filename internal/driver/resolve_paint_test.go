package driver

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"testing"
	"time"
)

// legacyOverlay is the previous quadratic overlay implementation, kept as a
// reference oracle for the boundary-sweep painter.
func legacyOverlay(intervals []Interval, applied Interval) []Interval {
	if !applied.EndsAt.After(applied.StartsAt) {
		return intervals
	}
	result := make([]Interval, 0, len(intervals)+2)
	for _, current := range intervals {
		if !current.EndsAt.After(applied.StartsAt) || !applied.EndsAt.After(current.StartsAt) {
			result = append(result, current)
			continue
		}
		if current.StartsAt.Before(applied.StartsAt) {
			left := current
			left.EndsAt = applied.StartsAt
			result = append(result, left)
		}
		middle := applied
		if middle.StartsAt.Before(current.StartsAt) {
			middle.StartsAt = current.StartsAt
		}
		if middle.EndsAt.After(current.EndsAt) {
			middle.EndsAt = current.EndsAt
		}
		result = append(result, middle)
		if current.EndsAt.After(applied.EndsAt) {
			right := current
			right.StartsAt = applied.EndsAt
			result = append(result, right)
		}
	}
	return result
}

func legacyPaint(base Interval, layers []Interval) []Interval {
	intervals := []Interval{base}
	for _, layer := range layers {
		intervals = legacyOverlay(intervals, layer)
	}
	return merge(intervals)
}

func TestPaintMatchesLegacyOverlayOnRandomLayers(t *testing.T) {
	random := rand.New(rand.NewSource(20261029)) // #nosec G404 -- deterministic test data.
	origin := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	statuses := []Status{StatusAvailable, StatusLimited, StatusUnavailable}
	sources := []Source{SourceRule, SourceException}
	for iteration := 0; iteration < 2000; iteration++ {
		span := 1 + random.Intn(48)
		from := origin.Add(time.Duration(random.Intn(4)) * time.Hour)
		to := from.Add(time.Duration(span) * time.Hour)
		base := Interval{StartsAt: from, EndsAt: to, Status: StatusUnavailable, Source: SourceNone, Reason: "base"}
		layers := make([]Interval, 0)
		for count := random.Intn(30); count > 0; count-- {
			start := origin.Add(time.Duration(random.Intn(60)) * time.Hour)
			end := start.Add(time.Duration(random.Intn(12)-1) * time.Hour)
			layer := Interval{
				StartsAt: start, EndsAt: end,
				Status:     statuses[random.Intn(len(statuses))],
				Source:     sources[random.Intn(len(sources))],
				SourceType: fmt.Sprintf("type-%d", random.Intn(2)),
				Reason:     fmt.Sprintf("reason-%d", random.Intn(2)),
			}
			layers = appendLayer(layers, clipped(layer, from, to))
		}
		got, err := paint(t.Context(), base, layers)
		if err != nil {
			t.Fatal(err)
		}
		want := legacyPaint(base, layers)
		if gotMerged := merge(got); !reflect.DeepEqual(gotMerged, want) {
			t.Fatalf("iteration %d: paint = %#v, legacy = %#v (layers %#v)", iteration, gotMerged, want, layers)
		}
	}
}

func TestResolveMatchesLegacyOverlayOnRandomSchedules(t *testing.T) {
	random := rand.New(rand.NewSource(7)) // #nosec G404 -- deterministic test data.
	location, err := time.LoadLocation("Europe/Vienna")
	if err != nil {
		t.Fatal(err)
	}
	service := &Service{location: location}
	types := []ExceptionType{ExceptionVacation, ExceptionSick, ExceptionUnavailable, ExceptionAvailableOverride, ExceptionOther}
	policies := []AvailabilityPolicy{PolicyLegacyRules, PolicyExplicitDates}
	from := time.Date(2026, 9, 7, 0, 0, 0, 0, location).UTC()
	to := from.AddDate(0, 0, 7)
	for iteration := 0; iteration < 300; iteration++ {
		data := Availability{Profile: Profile{ID: "driver-id", IsActive: true, AvailabilityPolicy: policies[random.Intn(len(policies))]}}
		for count := random.Intn(6); count > 0; count-- {
			start := random.Intn(20) * 60
			data.Rules = append(data.Rules, Rule{
				ID: fmt.Sprintf("rule-%d", count), Weekday: 1 + random.Intn(7), StartMinute: start, EndMinute: start + 30 + random.Intn(240),
				ValidFrom: "2026-01-01", Status: []RuleStatus{RuleAvailable, RuleLimited}[random.Intn(2)],
			})
		}
		for count := random.Intn(25); count > 0; count-- {
			exception := Exception{ID: fmt.Sprintf("exception-%02d", random.Intn(40)), Type: types[random.Intn(len(types))], InternalNote: fmt.Sprintf("note-%d", random.Intn(2))}
			if random.Intn(4) == 0 {
				exception.IsAllDay = true
				exception.LocalDate = from.In(location).AddDate(0, 0, random.Intn(8)-1).Format(time.DateOnly)
			} else {
				exception.StartsAt = from.Add(time.Duration(random.Intn(8*24*4)-4*12) * 15 * time.Minute)
				exception.EndsAt = exception.StartsAt.Add(time.Duration(1+random.Intn(40)) * 15 * time.Minute)
			}
			data.Exceptions = append(data.Exceptions, exception)
		}
		got, err := service.resolve(t.Context(), data, from, to)
		if err != nil {
			t.Fatal(err)
		}
		want := legacyResolve(t, service, data, from, to)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("iteration %d: resolve = %#v, legacy = %#v", iteration, got, want)
		}
	}
}

// legacyResolve is the previous resolve body (for active legacy/explicit-date
// profiles), folding each rule and exception through legacyOverlay.
func legacyResolve(t *testing.T, service *Service, data Availability, from, to time.Time) []Interval {
	t.Helper()
	base := Interval{StartsAt: from, EndsAt: to, Status: StatusUnavailable, Source: SourceNone, Reason: "keine gepflegte Verfügbarkeit"}
	intervals := []Interval{base}
	policy := profilePolicy(data.Profile)
	fromDate := localMidnight(from.In(service.location), service.location)
	toDate := localMidnight(to.Add(-time.Nanosecond).In(service.location), service.location)
	for date := fromDate; policy != PolicyExplicitDates && !date.After(toDate); date = date.AddDate(0, 0, 1) {
		dateText := date.Format(time.DateOnly)
		for _, rule := range data.Rules {
			if rule.Weekday != isoWeekday(date.Weekday()) || dateText < rule.ValidFrom || (rule.ValidUntil != "" && dateText > rule.ValidUntil) {
				continue
			}
			start, err := service.strictLocal(date, rule.StartMinute)
			if err != nil {
				t.Fatal(err)
			}
			end, err := service.strictLocal(date, rule.EndMinute)
			if err != nil {
				t.Fatal(err)
			}
			status := StatusAvailable
			if rule.Status == RuleLimited {
				status = StatusLimited
			}
			intervals = legacyOverlay(intervals, clipped(Interval{
				StartsAt: start.UTC(), EndsAt: end.UTC(), Status: status, Source: SourceRule,
				SourceType: string(rule.Status), Reason: ruleReason(rule.Status), InternalNote: rule.InternalNote,
			}, from, to))
		}
	}
	// resolve has already sorted data.Exceptions in place by priority and ID.
	for _, exception := range data.Exceptions {
		if policy == PolicyExplicitDates && exception.Type != ExceptionAvailableOverride {
			continue
		}
		start, end, err := service.exceptionRange(exception)
		if err != nil {
			t.Fatal(err)
		}
		status := StatusUnavailable
		if exception.Type == ExceptionAvailableOverride {
			status = StatusAvailable
		}
		intervals = legacyOverlay(intervals, clipped(Interval{
			StartsAt: start, EndsAt: end, Status: status, Source: SourceException,
			SourceType: string(exception.Type), Reason: exceptionReason(exception.Type), InternalNote: exception.InternalNote,
		}, from, to))
	}
	return merge(intervals)
}

func TestResolveHandlesManyDisjointExceptions(t *testing.T) {
	from := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	exceptions := make([]Exception, 0, 20000)
	for index := 0; index < cap(exceptions); index++ {
		start := from.Add(time.Duration(index) * 6 * time.Minute)
		exceptions = append(exceptions, Exception{
			ID: fmt.Sprintf("exception-%05d", index), Type: ExceptionUnavailable,
			StartsAt: start, EndsAt: start.Add(3 * time.Minute), InternalNote: fmt.Sprintf("%d", index),
		})
	}
	service := testService(t, Availability{Profile: Profile{ID: "driver-id", IsActive: true}, Exceptions: exceptions})
	to := from.Add(time.Duration(len(exceptions)) * 6 * time.Minute)
	started := time.Now()
	intervals, err := service.ResolveAvailability(t.Context(), adminActor(), "driver-id", from, to)
	if err != nil {
		t.Fatal(err)
	}
	if len(intervals) != 2*len(exceptions) {
		t.Fatalf("len(intervals) = %d, want %d", len(intervals), 2*len(exceptions))
	}
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Fatalf("resolve took %s", elapsed)
	}
}

func TestResolveHonorsContextCancellation(t *testing.T) {
	from := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	exceptions := make([]Exception, 0, 2*resolveCancelCheckInterval)
	for index := 0; index < cap(exceptions); index++ {
		start := from.Add(time.Duration(index) * time.Hour)
		exceptions = append(exceptions, Exception{ID: fmt.Sprintf("e-%04d", index), Type: ExceptionSick, StartsAt: start, EndsAt: start.Add(time.Minute)})
	}
	service := testService(t, Availability{Profile: Profile{ID: "driver-id", IsActive: true}, Exceptions: exceptions})
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := service.ResolveAvailability(ctx, adminActor(), "driver-id", from, from.AddDate(0, 0, 30)); !errors.Is(err, context.Canceled) {
		t.Fatalf("ResolveAvailability() canceled error = %v", err)
	}
	location, err := time.LoadLocation("Europe/Vienna")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := EvaluateAvailabilityContext(ctx, Availability{Profile: Profile{IsActive: true}, Exceptions: exceptions}, from, from.AddDate(0, 0, 30), location); !errors.Is(err, context.Canceled) {
		t.Fatalf("EvaluateAvailabilityContext() canceled error = %v", err)
	}
}

func TestPaintLaterLayerWinsAndEmptyBase(t *testing.T) {
	at := func(hour int) time.Time { return time.Date(2026, 9, 1, hour, 0, 0, 0, time.UTC) }
	base := Interval{StartsAt: at(0), EndsAt: at(10), Reason: "base"}
	layers := []Interval{
		{StartsAt: at(1), EndsAt: at(8), Reason: "wide"},
		{StartsAt: at(3), EndsAt: at(5), Reason: "narrow"},
		{StartsAt: at(4), EndsAt: at(9), Reason: "late"},
	}
	got, err := paint(t.Context(), base, layers)
	if err != nil {
		t.Fatal(err)
	}
	want := []Interval{
		{StartsAt: at(0), EndsAt: at(1), Reason: "base"},
		{StartsAt: at(1), EndsAt: at(3), Reason: "wide"},
		{StartsAt: at(3), EndsAt: at(4), Reason: "narrow"},
		{StartsAt: at(4), EndsAt: at(9), Reason: "late"},
		{StartsAt: at(9), EndsAt: at(10), Reason: "base"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("paint() = %#v", got)
	}
	if got, err := paint(t.Context(), Interval{StartsAt: at(2), EndsAt: at(2)}, nil); err != nil || len(got) != 0 {
		t.Fatalf("paint(empty) = %#v, %v", got, err)
	}
}

func TestExceptionDensityWindowCoversAnyOverlappingResolveRange(t *testing.T) {
	location, err := time.LoadLocation("Europe/Vienna")
	if err != nil {
		t.Fatal(err)
	}
	margin := MaxResolveDays * 24 * time.Hour
	timed := ExceptionInput{Type: ExceptionSick, StartsAt: time.Date(2026, 9, 10, 8, 0, 0, 0, time.UTC), EndsAt: time.Date(2026, 9, 10, 9, 0, 0, 0, time.UTC)}
	from, to, localFrom, localTo, err := ExceptionDensityWindow(timed, location)
	if err != nil || !from.Equal(timed.StartsAt.Add(-margin)) || !to.Equal(timed.EndsAt.Add(margin)) || localFrom != "2026-06-12" || localTo != "2026-12-09" {
		t.Fatalf("ExceptionDensityWindow(timed) = %s %s %s %s %v", from, to, localFrom, localTo, err)
	}
	allDay := ExceptionInput{Type: ExceptionVacation, IsAllDay: true, LocalDate: "2026-09-10"}
	day := time.Date(2026, 9, 10, 0, 0, 0, 0, location)
	from, to, _, _, err = ExceptionDensityWindow(allDay, location)
	if err != nil || !from.Equal(day.UTC().Add(-margin)) || !to.Equal(day.AddDate(0, 0, 1).UTC().Add(margin)) {
		t.Fatalf("ExceptionDensityWindow(all day) = %s %s %v", from, to, err)
	}
	if _, _, _, _, err := ExceptionDensityWindow(allDay, nil); !errors.Is(err, ErrValidation) {
		t.Fatalf("ExceptionDensityWindow(nil location) error = %v", err)
	}
	if _, _, _, _, err := ExceptionDensityWindow(ExceptionInput{IsAllDay: true, LocalDate: "bad"}, location); !errors.Is(err, ErrValidation) {
		t.Fatalf("ExceptionDensityWindow(bad date) error = %v", err)
	}
	if !errors.Is(ErrExceptionLimit, ErrValidation) {
		t.Fatal("ErrExceptionLimit must be a validation error")
	}
}
