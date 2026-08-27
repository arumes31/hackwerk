package driver

import (
	"context"
	"errors"
	"testing"
	"time"

	"example.invalid/hackplan/internal/auth"
)

type storeStub struct {
	availability      Availability
	availabilityErr   error
	availabilityFrom  time.Time
	availabilityTo    time.Time
	availabilityStart string
	availabilityEnd   string
	scheduleErr       error

	profiles        []Profile
	listProfilesErr error

	createdProfile    ProfileInput
	createdProfileErr error
	updatedProfile    ProfileInput
	updatedProfileErr error
	deactivateErr     error

	createdRule       RuleInput
	createdRuleDriver string
	createdRuleErr    error
	updatedRule       RuleInput
	updatedRuleErr    error
	deletedRuleErr    error
	clearedWeekday    int
	clearedRefs       []RuleRef
	clearRulesErr     error

	createdException    ExceptionInput
	createdExceptionErr error
	createdExceptions   []ExceptionInput
	createExceptionsErr error
	updatedException    ExceptionInput
	updatedExceptionErr error
	deletedExceptionErr error
}

func (s *storeStub) ListProfiles(context.Context) ([]Profile, error) {
	return s.profiles, s.listProfilesErr
}
func (s *storeStub) CreateProfile(_ context.Context, _ auth.Actor, input ProfileInput, _ string) (string, error) {
	s.createdProfile = input
	if s.createdProfileErr != nil {
		return "", s.createdProfileErr
	}
	return "driver-id", nil
}
func (s *storeStub) UpdateProfile(_ context.Context, _ auth.Actor, _ string, _ int32, input ProfileInput, _ string) error {
	s.updatedProfile = input
	return s.updatedProfileErr
}
func (s *storeStub) DeactivateProfile(context.Context, auth.Actor, string, int32, string) error {
	return s.deactivateErr
}
func (s *storeStub) Availability(_ context.Context, _ string, from, to time.Time, localFrom, localTo string) (Availability, error) {
	s.availabilityFrom, s.availabilityTo = from, to
	s.availabilityStart, s.availabilityEnd = localFrom, localTo
	return s.availability, s.availabilityErr
}
func (s *storeStub) Schedule(context.Context, string) (Availability, error) {
	return s.availability, s.scheduleErr
}

func (s *storeStub) CreateRule(_ context.Context, _ auth.Actor, driverID string, input RuleInput, _ string) (string, error) {
	s.createdRuleDriver, s.createdRule = driverID, input
	if s.createdRuleErr != nil {
		return "", s.createdRuleErr
	}
	return "rule-id", nil
}
func (s *storeStub) UpdateRule(_ context.Context, _ auth.Actor, _ string, _ string, _ int32, input RuleInput, _ string) error {
	s.updatedRule = input
	return s.updatedRuleErr
}
func (s *storeStub) DeleteRule(context.Context, auth.Actor, string, string, int32, string) error {
	return s.deletedRuleErr
}
func (s *storeStub) ClearRulesForDay(_ context.Context, _ auth.Actor, _ string, weekday int, refs []RuleRef, _ string) error {
	s.clearedWeekday, s.clearedRefs = weekday, append([]RuleRef(nil), refs...)
	return s.clearRulesErr
}
func (s *storeStub) CreateException(_ context.Context, _ auth.Actor, _ string, input ExceptionInput, _ string) (string, error) {
	s.createdException = input
	if s.createdExceptionErr != nil {
		return "", s.createdExceptionErr
	}
	return "exception-id", nil
}
func (s *storeStub) CreateExceptions(_ context.Context, _ auth.Actor, _ string, inputs []ExceptionInput, _ string) error {
	s.createdExceptions = append([]ExceptionInput(nil), inputs...)
	return s.createExceptionsErr
}
func (s *storeStub) UpdateException(_ context.Context, _ auth.Actor, _ string, _ string, _ int32, input ExceptionInput, _ string) error {
	s.updatedException = input
	return s.updatedExceptionErr
}
func (s *storeStub) DeleteException(context.Context, auth.Actor, string, string, int32, string) error {
	return s.deletedExceptionErr
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

func TestDuplicateRuleCopiesRuleToSelectedWeekday(t *testing.T) {
	t.Parallel()
	store := &storeStub{availability: Availability{Rules: []Rule{{
		ID: "rule-a", DriverID: "driver-id", Weekday: 1, StartMinute: 8 * 60, EndMinute: 17 * 60,
		ValidFrom: "2026-01-01", Status: RuleLimited, InternalNote: "Werkstatt",
	}}}}
	service := newDriverTestService(t, store)

	id, err := service.DuplicateRule(t.Context(), adminActor(), "driver-id", "rule-a", 3, "request")
	if err != nil {
		t.Fatal(err)
	}
	if id != "rule-id" || store.createdRuleDriver != "driver-id" || store.createdRule.Weekday != 3 || store.createdRule.LocalStart != "08:00" || store.createdRule.LocalEnd != "17:00" || store.createdRule.Status != RuleLimited {
		t.Fatalf("duplicated rule = %q/%q/%#v", id, store.createdRuleDriver, store.createdRule)
	}
}

func TestClearRulesForDayValidatesSnapshot(t *testing.T) {
	t.Parallel()
	store := &storeStub{}
	service := newDriverTestService(t, store)
	refs := []RuleRef{{ID: "rule-a", Version: 2}, {ID: "rule-b", Version: 4}}

	if err := service.ClearRulesForDay(t.Context(), adminActor(), "driver-id", 2, refs, "request"); err != nil {
		t.Fatal(err)
	}
	if store.clearedWeekday != 2 || len(store.clearedRefs) != 2 {
		t.Fatalf("clear snapshot = %d/%#v", store.clearedWeekday, store.clearedRefs)
	}
	if err := service.ClearRulesForDay(t.Context(), adminActor(), "driver-id", 2, []RuleRef{{ID: "same", Version: 1}, {ID: "same", Version: 1}}, "request"); !errors.Is(err, ErrValidation) {
		t.Fatalf("duplicate snapshot error = %v", err)
	}
}

func TestCreateVacationPresetBuildsFiveViennaWorkdays(t *testing.T) {
	t.Parallel()
	store := &storeStub{}
	service := newDriverTestService(t, store)

	if err := service.CreateVacationPreset(t.Context(), adminActor(), "driver-id", "2026-09-04", true, "Urlaub", "request"); err != nil {
		t.Fatal(err)
	}
	wantDates := []string{"2026-09-04", "2026-09-07", "2026-09-08", "2026-09-09", "2026-09-10"}
	if len(store.createdExceptions) != len(wantDates) {
		t.Fatalf("vacation preset = %#v", store.createdExceptions)
	}
	for index, input := range store.createdExceptions {
		if input.Type != ExceptionVacation || !input.IsAllDay || input.LocalDate != wantDates[index] || input.InternalNote != "Urlaub" {
			t.Fatalf("vacation[%d] = %#v", index, input)
		}
	}
}

func TestNewRequiresStoreAndViennaLocation(t *testing.T) {
	location, err := time.LoadLocation("Europe/Vienna")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := New(nil, location); err == nil {
		t.Fatal("New(nil, Vienna) succeeded")
	}
	if _, err := New(&storeStub{}, time.UTC); err == nil {
		t.Fatal("New(store, UTC) succeeded")
	}
	if _, err := New(&storeStub{}, location); err != nil {
		t.Fatalf("New(store, Vienna) error = %v", err)
	}
}

func TestProfileOperationsAuthorizeNormalizeAndPropagateStoreErrors(t *testing.T) {
	store := &storeStub{profiles: []Profile{{ID: "driver-id", DisplayName: "Fahrerin"}}}
	service := newDriverTestService(t, store)

	profiles, err := service.ListProfiles(t.Context(), adminActor())
	if err != nil || len(profiles) != 1 || profiles[0].ID != "driver-id" {
		t.Fatalf("ListProfiles() = %#v, %v", profiles, err)
	}
	if _, err := service.ListProfiles(t.Context(), driverActor()); !errors.Is(err, auth.ErrForbidden) {
		t.Fatalf("driver ListProfiles() error = %v", err)
	}
	store.listProfilesErr = errors.New("database unavailable")
	if _, err := service.ListProfiles(t.Context(), adminActor()); !errors.Is(err, store.listProfilesErr) {
		t.Fatalf("ListProfiles() store error = %v", err)
	}

	input := ProfileInput{UserID: " user-id ", DisplayName: "  Fahrerin  ", Phone: " +43 123 ", Email: " fahrerin@example.test ", InternalNote: "  Einsatzbereit  "}
	id, err := service.CreateProfile(t.Context(), adminActor(), input, "request")
	if err != nil || id != "driver-id" {
		t.Fatalf("CreateProfile() = %q, %v", id, err)
	}
	if store.createdProfile.UserID != "user-id" || store.createdProfile.DisplayName != "Fahrerin" || store.createdProfile.Phone != "+43 123" || store.createdProfile.Email != "fahrerin@example.test" || store.createdProfile.InternalNote != "Einsatzbereit" {
		t.Fatalf("stored profile = %#v", store.createdProfile)
	}
	if _, err := service.CreateProfile(t.Context(), driverActor(), input, "request"); !errors.Is(err, auth.ErrForbidden) {
		t.Fatalf("driver CreateProfile() error = %v", err)
	}
	if _, err := service.CreateProfile(t.Context(), adminActor(), ProfileInput{DisplayName: "F", Email: "not an address"}, "request"); !errors.Is(err, ErrValidation) {
		t.Fatalf("invalid CreateProfile() error = %v", err)
	}
	store.createdProfileErr = errors.New("write failed")
	if _, err := service.CreateProfile(t.Context(), adminActor(), validProfileInput(), "request"); !errors.Is(err, store.createdProfileErr) {
		t.Fatalf("CreateProfile() store error = %v", err)
	}

	store.createdProfileErr = nil
	if err := service.UpdateProfile(t.Context(), adminActor(), "driver-id", 3, input, "request"); err != nil {
		t.Fatalf("UpdateProfile() error = %v", err)
	}
	if store.updatedProfile.DisplayName != "Fahrerin" || store.updatedProfile.Email != "fahrerin@example.test" {
		t.Fatalf("stored update = %#v", store.updatedProfile)
	}
	if err := service.UpdateProfile(t.Context(), adminActor(), " ", 3, validProfileInput(), "request"); !errors.Is(err, ErrValidation) {
		t.Fatalf("invalid UpdateProfile() error = %v", err)
	}
	store.updatedProfileErr = errors.New("version conflict")
	if err := service.UpdateProfile(t.Context(), adminActor(), "driver-id", 3, validProfileInput(), "request"); !errors.Is(err, store.updatedProfileErr) {
		t.Fatalf("UpdateProfile() store error = %v", err)
	}

	if err := service.DeactivateProfile(t.Context(), adminActor(), "driver-id", 3, "request"); err != nil {
		t.Fatalf("DeactivateProfile() error = %v", err)
	}
	if err := service.DeactivateProfile(t.Context(), adminActor(), "driver-id", 0, "request"); !errors.Is(err, ErrValidation) {
		t.Fatalf("invalid DeactivateProfile() error = %v", err)
	}
	store.deactivateErr = errors.New("deactivate failed")
	if err := service.DeactivateProfile(t.Context(), adminActor(), "driver-id", 3, "request"); !errors.Is(err, store.deactivateErr) {
		t.Fatalf("DeactivateProfile() store error = %v", err)
	}
}

func TestAvailabilityOperationsRespectOwnershipAndRange(t *testing.T) {
	store := &storeStub{availability: Availability{Profile: Profile{ID: "driver-id", IsActive: true}}}
	service := newDriverTestService(t, store)
	from := time.Date(2026, 9, 10, 8, 0, 0, 0, time.UTC)
	to := from.Add(2 * time.Hour)

	if _, err := service.Schedule(t.Context(), driverActor(), "driver-id"); err != nil {
		t.Fatalf("own Schedule() error = %v", err)
	}
	if _, err := service.Schedule(t.Context(), driverActor(), "other-driver"); !errors.Is(err, auth.ErrForbidden) {
		t.Fatalf("other Schedule() error = %v", err)
	}
	store.scheduleErr = errors.New("schedule read failed")
	if _, err := service.Schedule(t.Context(), adminActor(), "driver-id"); !errors.Is(err, store.scheduleErr) {
		t.Fatalf("Schedule() store error = %v", err)
	}

	store.scheduleErr = nil
	status, reasons, err := service.IsAvailable(t.Context(), driverActor(), "driver-id", from, to)
	if err != nil || status != StatusUnavailable || len(reasons) != 1 || reasons[0] != "keine gepflegte Verfügbarkeit" {
		t.Fatalf("IsAvailable() = %q, %#v, %v", status, reasons, err)
	}
	if !store.availabilityFrom.Equal(from) || !store.availabilityTo.Equal(to) || store.availabilityStart != "2026-09-10" || store.availabilityEnd != "2026-09-10" {
		t.Fatalf("availability query = %s to %s (%q, %q)", store.availabilityFrom, store.availabilityTo, store.availabilityStart, store.availabilityEnd)
	}
	if _, err := service.ResolveAvailability(t.Context(), adminActor(), "driver-id", from.In(time.FixedZone("other", 3600)), to); !errors.Is(err, ErrValidation) {
		t.Fatalf("non-UTC ResolveAvailability() error = %v", err)
	}
	if _, err := service.ResolveAvailability(t.Context(), adminActor(), "driver-id", from, from.Add((MaxResolveDays+1)*24*time.Hour)); !errors.Is(err, ErrValidation) {
		t.Fatalf("long ResolveAvailability() error = %v", err)
	}
	store.availabilityErr = errors.New("availability read failed")
	if _, err := service.ResolveAvailability(t.Context(), adminActor(), "driver-id", from, to); !errors.Is(err, store.availabilityErr) {
		t.Fatalf("ResolveAvailability() store error = %v", err)
	}
}

func TestRuleAndExceptionMutationsValidateNormalizeAndPropagateStoreErrors(t *testing.T) {
	store := &storeStub{}
	service := newDriverTestService(t, store)
	rule := RuleInput{Weekday: 2, LocalStart: " 08:00 ", LocalEnd: " 17:00 ", ValidFrom: " 2026-01-01 ", ValidUntil: " 2026-12-31 ", Status: RuleLimited, InternalNote: "  Frühschicht  "}

	id, err := service.CreateRule(t.Context(), driverActor(), "driver-id", rule, "request")
	if err != nil || id != "rule-id" || store.createdRule.LocalStart != "08:00" || store.createdRule.ValidFrom != "2026-01-01" || store.createdRule.InternalNote != "Frühschicht" {
		t.Fatalf("CreateRule() = %q, %v; stored %#v", id, err, store.createdRule)
	}
	if _, err := service.CreateRule(t.Context(), driverActor(), "driver-id", RuleInput{Weekday: 8}, "request"); !errors.Is(err, ErrValidation) {
		t.Fatalf("invalid CreateRule() error = %v", err)
	}
	store.createdRuleErr = errors.New("rule write failed")
	if _, err := service.CreateRule(t.Context(), adminActor(), "driver-id", validRuleInput(), "request"); !errors.Is(err, store.createdRuleErr) {
		t.Fatalf("CreateRule() store error = %v", err)
	}

	store.createdRuleErr = nil
	if err := service.UpdateRule(t.Context(), adminActor(), "driver-id", "rule-id", 2, rule, "request"); err != nil {
		t.Fatalf("UpdateRule() error = %v", err)
	}
	if store.updatedRule.LocalEnd != "17:00" || store.updatedRule.ValidUntil != "2026-12-31" {
		t.Fatalf("stored rule update = %#v", store.updatedRule)
	}
	if err := service.UpdateRule(t.Context(), adminActor(), "driver-id", "", 2, validRuleInput(), "request"); !errors.Is(err, ErrValidation) {
		t.Fatalf("invalid UpdateRule() error = %v", err)
	}
	store.updatedRuleErr = errors.New("rule update failed")
	if err := service.UpdateRule(t.Context(), adminActor(), "driver-id", "rule-id", 2, validRuleInput(), "request"); !errors.Is(err, store.updatedRuleErr) {
		t.Fatalf("UpdateRule() store error = %v", err)
	}
	store.deletedRuleErr = errors.New("rule delete failed")
	if err := service.DeleteRule(t.Context(), adminActor(), "driver-id", "rule-id", 2, "request"); !errors.Is(err, store.deletedRuleErr) {
		t.Fatalf("DeleteRule() store error = %v", err)
	}

	location, err := time.LoadLocation("Europe/Vienna")
	if err != nil {
		t.Fatal(err)
	}
	exception := ExceptionInput{Type: ExceptionOther, StartsAt: time.Date(2026, 9, 10, 9, 0, 0, 0, location), EndsAt: time.Date(2026, 9, 10, 10, 0, 0, 0, location), InternalNote: "  Arzttermin  "}
	id, err = service.CreateException(t.Context(), driverActor(), "driver-id", exception, "request")
	if err != nil || id != "exception-id" || store.createdException.StartsAt.Location() != time.UTC || store.createdException.InternalNote != "Arzttermin" {
		t.Fatalf("CreateException() = %q, %v; stored %#v", id, err, store.createdException)
	}
	if _, err := service.CreateException(t.Context(), adminActor(), "driver-id", ExceptionInput{Type: ExceptionVacation, IsAllDay: true, LocalDate: "not-a-date"}, "request"); !errors.Is(err, ErrValidation) {
		t.Fatalf("invalid CreateException() error = %v", err)
	}
	store.createdExceptionErr = errors.New("exception write failed")
	if _, err := service.CreateException(t.Context(), adminActor(), "driver-id", exception, "request"); !errors.Is(err, store.createdExceptionErr) {
		t.Fatalf("CreateException() store error = %v", err)
	}

	allDay := ExceptionInput{Type: ExceptionVacation, IsAllDay: true, LocalDate: " 2026-09-11 ", InternalNote: "  Urlaub  "}
	if err := service.UpdateException(t.Context(), adminActor(), "driver-id", "exception-id", 2, allDay, "request"); err != nil {
		t.Fatalf("UpdateException() error = %v", err)
	}
	if store.updatedException.LocalDate != "2026-09-11" || store.updatedException.InternalNote != "Urlaub" {
		t.Fatalf("stored exception update = %#v", store.updatedException)
	}
	if err := service.UpdateException(t.Context(), adminActor(), "driver-id", "", 2, allDay, "request"); !errors.Is(err, ErrValidation) {
		t.Fatalf("invalid UpdateException() error = %v", err)
	}
	store.updatedExceptionErr = errors.New("exception update failed")
	if err := service.UpdateException(t.Context(), adminActor(), "driver-id", "exception-id", 2, allDay, "request"); !errors.Is(err, store.updatedExceptionErr) {
		t.Fatalf("UpdateException() store error = %v", err)
	}
	store.deletedExceptionErr = errors.New("exception delete failed")
	if err := service.DeleteException(t.Context(), adminActor(), "driver-id", "exception-id", 2, "request"); !errors.Is(err, store.deletedExceptionErr) {
		t.Fatalf("DeleteException() store error = %v", err)
	}
}

func TestEvaluateAvailabilityAndDateTimeParsingHandleBoundaries(t *testing.T) {
	location, err := time.LoadLocation("Europe/Vienna")
	if err != nil {
		t.Fatal(err)
	}
	from := time.Date(2026, 9, 7, 8, 0, 0, 0, time.UTC)
	to := from.Add(2 * time.Hour)
	data := Availability{Profile: Profile{IsActive: true}, Rules: []Rule{{
		ID: "monday", Weekday: 1, StartMinute: 9 * 60, EndMinute: 12 * 60, ValidFrom: "2026-01-01", Status: RuleLimited,
	}}}
	status, reasons, err := EvaluateAvailability(data, from, to, location)
	if err != nil || status != StatusLimited || len(reasons) != 1 || reasons[0] != "eingeschränkte Wochenregel" {
		t.Fatalf("EvaluateAvailability() = %q, %#v, %v", status, reasons, err)
	}
	if _, _, err := EvaluateAvailability(data, from, to, time.UTC); !errors.Is(err, ErrValidation) {
		t.Fatalf("EvaluateAvailability() UTC location error = %v", err)
	}
	if _, _, err := EvaluateAvailability(data, to, from, location); !errors.Is(err, ErrValidation) {
		t.Fatalf("EvaluateAvailability() reversed range error = %v", err)
	}

	parsed, err := ParseLocalDateTime(" 2026-09-10T12:30 ", location)
	if err != nil || !parsed.Equal(time.Date(2026, 9, 10, 10, 30, 0, 0, time.UTC)) {
		t.Fatalf("ParseLocalDateTime() = %s, %v", parsed, err)
	}
	for _, value := range []string{"2026-03-29T02:30", "2026-10-25T02:30"} {
		if _, err := ParseLocalDateTime(value, location); !errors.Is(err, ErrLocalTime) {
			t.Fatalf("ParseLocalDateTime(%q) error = %v", value, err)
		}
	}
	if _, err := ParseLocalDateTime("not-a-datetime", location); !errors.Is(err, ErrValidation) {
		t.Fatalf("invalid ParseLocalDateTime() error = %v", err)
	}
}

func testService(t *testing.T, availability Availability) *Service {
	t.Helper()
	return newDriverTestService(t, &storeStub{availability: availability})
}

func newDriverTestService(t *testing.T, store *storeStub) *Service {
	t.Helper()
	location, err := time.LoadLocation("Europe/Vienna")
	if err != nil {
		t.Fatal(err)
	}
	service, err := New(store, location)
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func adminActor() auth.Actor {
	return auth.Actor{UserID: "admin", Role: auth.RoleAdmin}
}

func driverActor() auth.Actor {
	return auth.Actor{UserID: "driver-user", Role: auth.RoleDriver, DriverID: "driver-id"}
}

func validProfileInput() ProfileInput {
	return ProfileInput{UserID: "user-id", DisplayName: "Fahrerin", Email: "fahrerin@example.test"}
}

func validRuleInput() RuleInput {
	return RuleInput{Weekday: 1, LocalStart: "08:00", LocalEnd: "17:00", ValidFrom: "2026-01-01", Status: RuleAvailable}
}
