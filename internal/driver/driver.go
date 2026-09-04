// Package driver implements driver profiles and Vienna availability rules.
package driver

import (
	"context"
	"errors"
	"fmt"
	"net/mail"
	"sort"
	"strings"
	"time"

	"example.invalid/hackplan/internal/auth"
)

const MaxResolveDays = 90

var (
	ErrConflict   = errors.New("driver: version conflict")
	ErrLocalTime  = errors.New("driver: ambiguous or nonexistent local time")
	ErrNotFound   = errors.New("driver: not found")
	ErrValidation = errors.New("driver: validation failed")
)

type RuleStatus string
type Status string
type ExceptionType string
type Source string
type AvailabilityPolicy string

const (
	RuleAvailable RuleStatus = "available"
	RuleLimited   RuleStatus = "limited"

	StatusAvailable   Status = "available"
	StatusLimited     Status = "limited"
	StatusUnavailable Status = "unavailable"

	ExceptionVacation          ExceptionType = "vacation"
	ExceptionSick              ExceptionType = "sick"
	ExceptionUnavailable       ExceptionType = "unavailable"
	ExceptionAvailableOverride ExceptionType = "available_override"
	ExceptionOther             ExceptionType = "other"

	SourceNone      Source = "none"
	SourceRule      Source = "rule"
	SourceException Source = "exception"
	SourceInactive  Source = "inactive_driver"
	SourcePolicy    Source = "policy"

	PolicyLegacyRules      AvailabilityPolicy = "legacy_rules"
	PolicyAssumedAvailable AvailabilityPolicy = "assumed_available"
	PolicyExplicitDates    AvailabilityPolicy = "explicit_dates"
)

type ProfileInput struct {
	UserID             string
	DisplayName        string
	Phone              string
	Email              string
	CanCompleteJobs    bool
	InternalNote       string
	IsPrimary          bool
	AvailabilityPolicy AvailabilityPolicy
}

type Profile struct {
	ID                 string
	UserID             string
	Username           string
	DisplayName        string
	Phone              string
	Email              string
	IsActive           bool
	CanCompleteJobs    bool
	InternalNote       string
	IsPrimary          bool
	AvailabilityPolicy AvailabilityPolicy
	Version            int32
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

type RuleInput struct {
	Weekday      int
	LocalStart   string
	LocalEnd     string
	ValidFrom    string
	ValidUntil   string
	Status       RuleStatus
	InternalNote string
}

type Rule struct {
	ID           string
	DriverID     string
	Weekday      int
	StartMinute  int
	EndMinute    int
	ValidFrom    string
	ValidUntil   string
	Status       RuleStatus
	InternalNote string
	Version      int32
}

type RuleRef struct {
	ID      string
	Version int32
}

type ExceptionInput struct {
	Type         ExceptionType
	IsAllDay     bool
	LocalDate    string
	StartsAt     time.Time
	EndsAt       time.Time
	InternalNote string
}

type Exception struct {
	ID           string
	DriverID     string
	Type         ExceptionType
	IsAllDay     bool
	LocalDate    string
	StartsAt     time.Time
	EndsAt       time.Time
	InternalNote string
	Version      int32
}

type Interval struct {
	StartsAt     time.Time `json:"starts_at"`
	EndsAt       time.Time `json:"ends_at"`
	Status       Status    `json:"status"`
	Source       Source    `json:"source"`
	SourceType   string    `json:"source_type,omitempty"`
	Reason       string    `json:"reason"`
	InternalNote string    `json:"internal_note,omitempty"`
}

type Availability struct {
	Profile    Profile
	Rules      []Rule
	Exceptions []Exception
}

type Store interface {
	ListProfiles(context.Context) ([]Profile, error)
	CreateProfile(context.Context, auth.Actor, ProfileInput, string) (string, error)
	UpdateProfile(context.Context, auth.Actor, string, int32, ProfileInput, string) error
	DeactivateProfile(context.Context, auth.Actor, string, int32, string) error
	Schedule(context.Context, string) (Availability, error)
	Availability(context.Context, string, time.Time, time.Time, string, string) (Availability, error)
	CreateRule(context.Context, auth.Actor, string, RuleInput, string) (string, error)
	UpdateRule(context.Context, auth.Actor, string, string, int32, RuleInput, string) error
	DeleteRule(context.Context, auth.Actor, string, string, int32, string) error
	ClearRulesForDay(context.Context, auth.Actor, string, int, []RuleRef, string) error
	CreateException(context.Context, auth.Actor, string, ExceptionInput, string) (string, error)
	CreateExceptions(context.Context, auth.Actor, string, []ExceptionInput, string) error
	UpdateException(context.Context, auth.Actor, string, string, int32, ExceptionInput, string) error
	DeleteException(context.Context, auth.Actor, string, string, int32, string) error
}

type Service struct {
	store    Store
	location *time.Location
}

func New(store Store, location *time.Location) (*Service, error) {
	if store == nil {
		return nil, errors.New("driver: store is required")
	}
	if location == nil || location.String() != "Europe/Vienna" {
		return nil, errors.New("driver: Europe/Vienna location is required")
	}
	return &Service{store: store, location: location}, nil
}

func (s *Service) ListProfiles(ctx context.Context, actor auth.Actor) ([]Profile, error) {
	if err := actor.Require(auth.PermissionDriverManage); err != nil {
		return nil, err
	}
	return s.store.ListProfiles(ctx)
}

func (s *Service) CreateProfile(ctx context.Context, actor auth.Actor, input ProfileInput, requestID string) (string, error) {
	if err := actor.Require(auth.PermissionDriverManage); err != nil {
		return "", err
	}
	normalizeProfile(&input)
	if err := input.Validate(); err != nil {
		return "", err
	}
	return s.store.CreateProfile(ctx, actor, input, requestID)
}

func (s *Service) UpdateProfile(ctx context.Context, actor auth.Actor, id string, version int32, input ProfileInput, requestID string) error {
	if err := actor.Require(auth.PermissionDriverManage); err != nil {
		return err
	}
	normalizeProfile(&input)
	if strings.TrimSpace(id) == "" || version < 1 {
		return ErrValidation
	}
	if err := input.Validate(); err != nil {
		return err
	}
	return s.store.UpdateProfile(ctx, actor, id, version, input, requestID)
}

func (s *Service) DeactivateProfile(ctx context.Context, actor auth.Actor, id string, version int32, requestID string) error {
	if err := actor.Require(auth.PermissionDriverManage); err != nil {
		return err
	}
	if strings.TrimSpace(id) == "" || version < 1 {
		return ErrValidation
	}
	return s.store.DeactivateProfile(ctx, actor, id, version, requestID)
}

func (s *Service) Schedule(ctx context.Context, actor auth.Actor, driverID string) (Availability, error) {
	if err := authorizeAvailability(actor, driverID); err != nil {
		return Availability{}, err
	}
	return s.store.Schedule(ctx, driverID)
}

func (s *Service) CreateRule(ctx context.Context, actor auth.Actor, driverID string, input RuleInput, requestID string) (string, error) {
	if err := authorizeAvailability(actor, driverID); err != nil {
		return "", err
	}
	normalizeRule(&input)
	if err := input.Validate(); err != nil {
		return "", err
	}
	if err := s.requireLegacyRules(ctx, driverID); err != nil {
		return "", err
	}
	return s.store.CreateRule(ctx, actor, driverID, input, requestID)
}

func (s *Service) UpdateRule(ctx context.Context, actor auth.Actor, driverID string, id string, version int32, input RuleInput, requestID string) error {
	if err := authorizeAvailability(actor, driverID); err != nil {
		return err
	}
	normalizeRule(&input)
	if id == "" || version < 1 {
		return ErrValidation
	}
	if err := input.Validate(); err != nil {
		return err
	}
	if err := s.requireLegacyRules(ctx, driverID); err != nil {
		return err
	}
	return s.store.UpdateRule(ctx, actor, driverID, id, version, input, requestID)
}

func (s *Service) DeleteRule(ctx context.Context, actor auth.Actor, driverID string, id string, version int32, requestID string) error {
	if err := authorizeAvailability(actor, driverID); err != nil {
		return err
	}
	if id == "" || version < 1 {
		return ErrValidation
	}
	return s.store.DeleteRule(ctx, actor, driverID, id, version, requestID)
}

func (s *Service) DuplicateRule(ctx context.Context, actor auth.Actor, driverID, id string, targetWeekday int, requestID string) (string, error) {
	if err := authorizeAvailability(actor, driverID); err != nil {
		return "", err
	}
	if strings.TrimSpace(id) == "" || targetWeekday < 1 || targetWeekday > 7 {
		return "", ErrValidation
	}
	data, err := s.store.Schedule(ctx, driverID)
	if err != nil {
		return "", err
	}
	if profilePolicy(data.Profile) != PolicyLegacyRules {
		return "", ErrValidation
	}
	for _, rule := range data.Rules {
		if rule.ID != id {
			continue
		}
		input := RuleInput{
			Weekday: targetWeekday, LocalStart: FormatLocalTime(rule.StartMinute), LocalEnd: FormatLocalTime(rule.EndMinute),
			ValidFrom: rule.ValidFrom, ValidUntil: rule.ValidUntil, Status: rule.Status, InternalNote: rule.InternalNote,
		}
		return s.store.CreateRule(ctx, actor, driverID, input, requestID)
	}
	return "", ErrNotFound
}

func (s *Service) ClearRulesForDay(ctx context.Context, actor auth.Actor, driverID string, weekday int, refs []RuleRef, requestID string) error {
	if err := authorizeAvailability(actor, driverID); err != nil {
		return err
	}
	if weekday < 1 || weekday > 7 || len(refs) == 0 {
		return ErrValidation
	}
	seen := make(map[string]struct{}, len(refs))
	for _, ref := range refs {
		if strings.TrimSpace(ref.ID) == "" || ref.Version < 1 {
			return ErrValidation
		}
		if _, duplicate := seen[ref.ID]; duplicate {
			return ErrValidation
		}
		seen[ref.ID] = struct{}{}
	}
	return s.store.ClearRulesForDay(ctx, actor, driverID, weekday, refs, requestID)
}

func (s *Service) CreateException(ctx context.Context, actor auth.Actor, driverID string, input ExceptionInput, requestID string) (string, error) {
	if err := authorizeAvailability(actor, driverID); err != nil {
		return "", err
	}
	normalizeException(&input)
	if err := input.Validate(); err != nil {
		return "", err
	}
	if err := s.requireExceptionPolicy(ctx, driverID, input.Type); err != nil {
		return "", err
	}
	return s.store.CreateException(ctx, actor, driverID, input, requestID)
}

func (s *Service) CreateVacationPreset(ctx context.Context, actor auth.Actor, driverID, localDate string, workweek bool, internalNote, requestID string) error {
	if err := authorizeAvailability(actor, driverID); err != nil {
		return err
	}
	if err := s.requireLegacyRules(ctx, driverID); err != nil {
		return err
	}
	start, err := time.ParseInLocation(time.DateOnly, strings.TrimSpace(localDate), s.location)
	if err != nil || len([]rune(internalNote)) > 1000 {
		return ErrValidation
	}
	count := 1
	if workweek {
		count = 5
	}
	inputs := make([]ExceptionInput, 0, count)
	for day := start; len(inputs) < count; day = day.AddDate(0, 0, 1) {
		if workweek && (day.Weekday() == time.Saturday || day.Weekday() == time.Sunday) {
			continue
		}
		inputs = append(inputs, ExceptionInput{Type: ExceptionVacation, IsAllDay: true, LocalDate: day.Format(time.DateOnly), InternalNote: strings.TrimSpace(internalNote)})
	}
	return s.store.CreateExceptions(ctx, actor, driverID, inputs, requestID)
}

func (s *Service) UpdateException(ctx context.Context, actor auth.Actor, driverID string, id string, version int32, input ExceptionInput, requestID string) error {
	if err := authorizeAvailability(actor, driverID); err != nil {
		return err
	}
	normalizeException(&input)
	if id == "" || version < 1 {
		return ErrValidation
	}
	if err := input.Validate(); err != nil {
		return err
	}
	if err := s.requireExceptionPolicy(ctx, driverID, input.Type); err != nil {
		return err
	}
	return s.store.UpdateException(ctx, actor, driverID, id, version, input, requestID)
}

func (s *Service) requireLegacyRules(ctx context.Context, driverID string) error {
	data, err := s.store.Schedule(ctx, driverID)
	if err != nil {
		return err
	}
	if profilePolicy(data.Profile) != PolicyLegacyRules {
		return ErrValidation
	}
	return nil
}

func (s *Service) requireExceptionPolicy(ctx context.Context, driverID string, exceptionType ExceptionType) error {
	data, err := s.store.Schedule(ctx, driverID)
	if err != nil {
		return err
	}
	switch profilePolicy(data.Profile) {
	case PolicyLegacyRules:
		return nil
	case PolicyExplicitDates:
		if exceptionType == ExceptionAvailableOverride {
			return nil
		}
	}
	return ErrValidation
}

func profilePolicy(profile Profile) AvailabilityPolicy {
	if profile.AvailabilityPolicy == "" {
		return PolicyLegacyRules
	}
	return profile.AvailabilityPolicy
}

func (s *Service) DeleteException(ctx context.Context, actor auth.Actor, driverID string, id string, version int32, requestID string) error {
	if err := authorizeAvailability(actor, driverID); err != nil {
		return err
	}
	if id == "" || version < 1 {
		return ErrValidation
	}
	return s.store.DeleteException(ctx, actor, driverID, id, version, requestID)
}

func (s *Service) ResolveAvailability(ctx context.Context, actor auth.Actor, driverID string, fromUTC time.Time, toUTC time.Time) ([]Interval, error) {
	if err := authorizeAvailability(actor, driverID); err != nil {
		return nil, err
	}
	if err := validateRange(fromUTC, toUTC); err != nil {
		return nil, err
	}
	localFrom := fromUTC.In(s.location).Format(time.DateOnly)
	localTo := toUTC.Add(-time.Nanosecond).In(s.location).Format(time.DateOnly)
	data, err := s.store.Availability(ctx, driverID, fromUTC, toUTC, localFrom, localTo)
	if err != nil {
		return nil, err
	}
	return s.resolve(data, fromUTC.UTC(), toUTC.UTC())
}

func (s *Service) IsAvailable(ctx context.Context, actor auth.Actor, driverID string, fromUTC time.Time, toUTC time.Time) (Status, []string, error) {
	intervals, err := s.ResolveAvailability(ctx, actor, driverID, fromUTC, toUTC)
	if err != nil {
		return StatusUnavailable, nil, err
	}
	status, reasons := availabilityStatus(intervals)
	return status, reasons, nil
}

// EvaluateAvailability resolves a transactionally loaded availability snapshot.
// It is used at persistence boundaries that must revalidate a schedule while
// holding the same driver locks as the appointment mutation.
func EvaluateAvailability(data Availability, fromUTC, toUTC time.Time, location *time.Location) (Status, []string, error) {
	if location == nil || location.String() != "Europe/Vienna" || validateRange(fromUTC, toUTC) != nil {
		return StatusUnavailable, nil, ErrValidation
	}
	service := Service{location: location}
	intervals, err := service.resolve(data, fromUTC, toUTC)
	if err != nil {
		return StatusUnavailable, nil, err
	}
	status, reasons := availabilityStatus(intervals)
	return status, reasons, nil
}

func availabilityStatus(intervals []Interval) (Status, []string) {
	status := StatusAvailable
	reasons := make([]string, 0)
	seen := make(map[string]bool)
	for _, interval := range intervals {
		if interval.Status == StatusUnavailable {
			status = StatusUnavailable
		} else if interval.Status == StatusLimited && status == StatusAvailable {
			status = StatusLimited
		}
		if !seen[interval.Reason] {
			reasons = append(reasons, interval.Reason)
			seen[interval.Reason] = true
		}
	}
	return status, reasons
}

func (i ProfileInput) Validate() error {
	if i.DisplayName == "" || len([]rune(i.DisplayName)) > 200 || len([]rune(i.Phone)) > 64 || len([]rune(i.InternalNote)) > 4000 || !i.AvailabilityPolicy.Valid() || (i.AvailabilityPolicy == PolicyAssumedAvailable && !i.IsPrimary) {
		return ErrValidation
	}
	if i.Email != "" {
		address, err := mail.ParseAddress(i.Email)
		if err != nil || address.Address != i.Email || len(i.Email) > 320 || strings.ContainsAny(i.Email, "\r\n") {
			return fmt.Errorf("%w: invalid email", ErrValidation)
		}
	}
	return nil
}

func (p AvailabilityPolicy) Valid() bool {
	return p == PolicyLegacyRules || p == PolicyAssumedAvailable || p == PolicyExplicitDates
}

func (i RuleInput) Validate() error {
	start, startErr := ParseLocalTime(i.LocalStart)
	end, endErr := ParseLocalTime(i.LocalEnd)
	from, fromErr := time.Parse(time.DateOnly, i.ValidFrom)
	var until time.Time
	var untilErr error
	if i.ValidUntil != "" {
		until, untilErr = time.Parse(time.DateOnly, i.ValidUntil)
	}
	if i.Weekday < 1 || i.Weekday > 7 || startErr != nil || endErr != nil || end <= start || fromErr != nil || untilErr != nil || (!until.IsZero() && until.Before(from)) || !i.Status.Valid() || len([]rune(i.InternalNote)) > 1000 {
		return ErrValidation
	}
	return nil
}

func (i ExceptionInput) Validate() error {
	if !i.Type.Valid() || len([]rune(i.InternalNote)) > 1000 {
		return ErrValidation
	}
	if i.IsAllDay {
		if _, err := time.Parse(time.DateOnly, i.LocalDate); err != nil || !i.StartsAt.IsZero() || !i.EndsAt.IsZero() {
			return ErrValidation
		}
		return nil
	}
	if i.LocalDate != "" || i.StartsAt.IsZero() || !i.EndsAt.After(i.StartsAt) || i.EndsAt.Sub(i.StartsAt) > MaxResolveDays*24*time.Hour {
		return ErrValidation
	}
	return nil
}

func (s RuleStatus) Valid() bool { return s == RuleAvailable || s == RuleLimited }
func (t ExceptionType) Valid() bool {
	return t == ExceptionVacation || t == ExceptionSick || t == ExceptionUnavailable || t == ExceptionAvailableOverride || t == ExceptionOther
}

func ParseLocalTime(value string) (int, error) {
	parsed, err := time.Parse("15:04", strings.TrimSpace(value))
	if err != nil {
		return 0, ErrValidation
	}
	return parsed.Hour()*60 + parsed.Minute(), nil
}

func FormatLocalTime(minutes int) string {
	return fmt.Sprintf("%02d:%02d", minutes/60, minutes%60)
}

// ParseLocalDateTime converts an HTML datetime-local value without guessing
// across daylight-saving gaps or repeated clock times.
func ParseLocalDateTime(value string, location *time.Location) (time.Time, error) {
	if location == nil {
		return time.Time{}, ErrValidation
	}
	parsed, err := time.Parse("2006-01-02T15:04", strings.TrimSpace(value))
	if err != nil {
		return time.Time{}, ErrValidation
	}
	candidate := time.Date(parsed.Year(), parsed.Month(), parsed.Day(), parsed.Hour(), parsed.Minute(), 0, 0, location)
	matches := make([]time.Time, 0, 2)
	for offset := -2 * time.Hour; offset <= 2*time.Hour; offset += time.Hour {
		local := candidate.Add(offset).In(location)
		if local.Year() != parsed.Year() || local.Month() != parsed.Month() || local.Day() != parsed.Day() || local.Hour() != parsed.Hour() || local.Minute() != parsed.Minute() {
			continue
		}
		duplicate := false
		for _, match := range matches {
			duplicate = duplicate || match.Equal(local)
		}
		if !duplicate {
			matches = append(matches, local)
		}
	}
	if len(matches) != 1 {
		return time.Time{}, ErrLocalTime
	}
	return matches[0].UTC(), nil
}

func authorizeAvailability(actor auth.Actor, driverID string) error {
	driverID = strings.TrimSpace(driverID)
	if driverID == "" {
		return ErrValidation
	}
	if actor.Role == auth.RoleAdmin {
		return actor.Require(auth.PermissionAvailabilityOther)
	}
	if err := actor.Require(auth.PermissionAvailabilityOwn); err != nil {
		return err
	}
	if actor.DriverID == "" || actor.DriverID != driverID {
		return auth.ErrForbidden
	}
	return nil
}

func validateRange(fromUTC time.Time, toUTC time.Time) error {
	if fromUTC.Location() != time.UTC || toUTC.Location() != time.UTC || !toUTC.After(fromUTC) || toUTC.Sub(fromUTC) > MaxResolveDays*24*time.Hour {
		return ErrValidation
	}
	return nil
}

func normalizeProfile(input *ProfileInput) {
	input.UserID = strings.TrimSpace(input.UserID)
	input.DisplayName = strings.TrimSpace(input.DisplayName)
	input.Phone = strings.TrimSpace(input.Phone)
	input.Email = strings.TrimSpace(input.Email)
	input.InternalNote = strings.TrimSpace(input.InternalNote)
	if input.AvailabilityPolicy == "" {
		input.AvailabilityPolicy = PolicyExplicitDates
	}
}

func normalizeRule(input *RuleInput) {
	input.LocalStart = strings.TrimSpace(input.LocalStart)
	input.LocalEnd = strings.TrimSpace(input.LocalEnd)
	input.ValidFrom = strings.TrimSpace(input.ValidFrom)
	input.ValidUntil = strings.TrimSpace(input.ValidUntil)
	input.InternalNote = strings.TrimSpace(input.InternalNote)
}

func normalizeException(input *ExceptionInput) {
	input.LocalDate = strings.TrimSpace(input.LocalDate)
	input.InternalNote = strings.TrimSpace(input.InternalNote)
	input.StartsAt = input.StartsAt.UTC()
	input.EndsAt = input.EndsAt.UTC()
}

func (s *Service) resolve(data Availability, fromUTC time.Time, toUTC time.Time) ([]Interval, error) {
	base := Interval{StartsAt: fromUTC, EndsAt: toUTC, Status: StatusUnavailable, Source: SourceNone, Reason: "keine gepflegte Verfügbarkeit"}
	if !data.Profile.IsActive {
		base.Source = SourceInactive
		base.Reason = "Fahrerprofil ist inaktiv"
		return []Interval{base}, nil
	}
	policy := data.Profile.AvailabilityPolicy
	if policy == "" {
		policy = PolicyLegacyRules
	}
	if policy == PolicyAssumedAvailable {
		return []Interval{{
			StartsAt: fromUTC, EndsAt: toUTC, Status: StatusAvailable, Source: SourcePolicy,
			SourceType: string(policy), Reason: "Hauptfahrer standardmäßig verfügbar",
		}}, nil
	}
	intervals := []Interval{base}
	fromDate := localMidnight(fromUTC.In(s.location), s.location)
	toDate := localMidnight(toUTC.Add(-time.Nanosecond).In(s.location), s.location)
	for date := fromDate; policy != PolicyExplicitDates && !date.After(toDate); date = date.AddDate(0, 0, 1) {
		dateText := date.Format(time.DateOnly)
		for _, rule := range data.Rules {
			if rule.Weekday != isoWeekday(date.Weekday()) || dateText < rule.ValidFrom || (rule.ValidUntil != "" && dateText > rule.ValidUntil) {
				continue
			}
			start, err := s.strictLocal(date, rule.StartMinute)
			if err != nil {
				return nil, fmt.Errorf("%w: rule %s start", err, rule.ID)
			}
			end, err := s.strictLocal(date, rule.EndMinute)
			if err != nil {
				return nil, fmt.Errorf("%w: rule %s end", err, rule.ID)
			}
			status := StatusAvailable
			if rule.Status == RuleLimited {
				status = StatusLimited
			}
			intervals = overlay(intervals, clipped(Interval{
				StartsAt: start.UTC(), EndsAt: end.UTC(), Status: status, Source: SourceRule,
				SourceType: string(rule.Status), Reason: ruleReason(rule.Status), InternalNote: rule.InternalNote,
			}, fromUTC, toUTC))
		}
	}
	sort.SliceStable(data.Exceptions, func(i int, j int) bool {
		left, right := exceptionPriority(data.Exceptions[i].Type), exceptionPriority(data.Exceptions[j].Type)
		if left != right {
			return left < right
		}
		return data.Exceptions[i].ID < data.Exceptions[j].ID
	})
	for _, exception := range data.Exceptions {
		if policy == PolicyExplicitDates && exception.Type != ExceptionAvailableOverride {
			continue
		}
		start, end, err := s.exceptionRange(exception)
		if err != nil {
			return nil, err
		}
		status := StatusUnavailable
		if exception.Type == ExceptionAvailableOverride {
			status = StatusAvailable
		}
		intervals = overlay(intervals, clipped(Interval{
			StartsAt: start, EndsAt: end, Status: status, Source: SourceException,
			SourceType: string(exception.Type), Reason: exceptionReason(exception.Type), InternalNote: exception.InternalNote,
		}, fromUTC, toUTC))
	}
	return merge(intervals), nil
}

func (s *Service) strictLocal(date time.Time, minute int) (time.Time, error) {
	hour := minute / 60
	minuteOfHour := minute % 60
	candidate := time.Date(date.Year(), date.Month(), date.Day(), hour, minuteOfHour, 0, 0, s.location)
	matches := make([]time.Time, 0, 2)
	for offset := -2 * time.Hour; offset <= 2*time.Hour; offset += time.Hour {
		value := candidate.Add(offset).In(s.location)
		if value.Year() == date.Year() && value.Month() == date.Month() && value.Day() == date.Day() && value.Hour() == hour && value.Minute() == minuteOfHour {
			duplicate := false
			for _, match := range matches {
				duplicate = duplicate || match.Equal(value)
			}
			if !duplicate {
				matches = append(matches, value)
			}
		}
	}
	if len(matches) != 1 {
		return time.Time{}, ErrLocalTime
	}
	return matches[0], nil
}

func (s *Service) exceptionRange(exception Exception) (time.Time, time.Time, error) {
	if !exception.IsAllDay {
		return exception.StartsAt.UTC(), exception.EndsAt.UTC(), nil
	}
	date, err := time.ParseInLocation(time.DateOnly, exception.LocalDate, s.location)
	if err != nil {
		return time.Time{}, time.Time{}, ErrValidation
	}
	return date.UTC(), date.AddDate(0, 0, 1).UTC(), nil
}

func overlay(intervals []Interval, applied Interval) []Interval {
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

func clipped(interval Interval, fromUTC time.Time, toUTC time.Time) Interval {
	if interval.StartsAt.Before(fromUTC) {
		interval.StartsAt = fromUTC
	}
	if interval.EndsAt.After(toUTC) {
		interval.EndsAt = toUTC
	}
	return interval
}

func merge(intervals []Interval) []Interval {
	sort.Slice(intervals, func(i int, j int) bool { return intervals[i].StartsAt.Before(intervals[j].StartsAt) })
	result := make([]Interval, 0, len(intervals))
	for _, interval := range intervals {
		if !interval.EndsAt.After(interval.StartsAt) {
			continue
		}
		if len(result) > 0 {
			last := &result[len(result)-1]
			if last.EndsAt.Equal(interval.StartsAt) && last.Status == interval.Status && last.Source == interval.Source && last.SourceType == interval.SourceType && last.Reason == interval.Reason && last.InternalNote == interval.InternalNote {
				last.EndsAt = interval.EndsAt
				continue
			}
		}
		result = append(result, interval)
	}
	return result
}

func localMidnight(value time.Time, location *time.Location) time.Time {
	return time.Date(value.Year(), value.Month(), value.Day(), 0, 0, 0, 0, location)
}

func isoWeekday(day time.Weekday) int {
	if day == time.Sunday {
		return 7
	}
	return int(day)
}

func ruleReason(status RuleStatus) string {
	if status == RuleLimited {
		return "eingeschränkte Wochenregel"
	}
	return "verfügbare Wochenregel"
}

func exceptionReason(value ExceptionType) string {
	switch value {
	case ExceptionVacation:
		return "Urlaub"
	case ExceptionSick:
		return "nicht verfügbar"
	case ExceptionUnavailable:
		return "kurzfristig nicht verfügbar"
	case ExceptionAvailableOverride:
		return "kurzfristig verfügbar"
	case ExceptionOther:
		return "andere Abwesenheit"
	default:
		return "nicht verfügbar"
	}
}

func exceptionPriority(value ExceptionType) int {
	switch value {
	case ExceptionAvailableOverride:
		return 1
	case ExceptionOther:
		return 2
	case ExceptionUnavailable:
		return 3
	case ExceptionVacation:
		return 4
	case ExceptionSick:
		return 5
	default:
		return 0
	}
}
