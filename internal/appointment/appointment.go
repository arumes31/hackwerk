// Package appointment implements calendar scheduling and reservation rules.
package appointment

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"example.invalid/hackplan/internal/auth"
	"example.invalid/hackplan/internal/driver"
	"example.invalid/hackplan/internal/resource"
)

const (
	MaxCalendarRange = 93 * 24 * time.Hour
	MaxDuration      = 7 * 24 * time.Hour
)

var (
	ErrAvailability    = errors.New("appointment: driver unavailable")
	ErrConflict        = errors.New("appointment: reservation conflict")
	ErrVersionConflict = errors.New("appointment: version conflict")
	ErrNotFound        = errors.New("appointment: not found")
	ErrNotification    = errors.New("appointment: no reachable notification channel")
	ErrTransition      = errors.New("appointment: invalid transition")
	ErrValidation      = errors.New("appointment: validation failed")
)

type Lifecycle string
type Confirmation string
type Purpose string
type PreflightSeverity string

const (
	LifecycleDraft     Lifecycle = "draft"
	LifecycleProposal  Lifecycle = "proposal"
	LifecycleFixed     Lifecycle = "fixed"
	LifecycleCancelled Lifecycle = "cancelled"
	LifecycleCompleted Lifecycle = "completed"

	ConfirmationNotRequested Confirmation = "not_requested"
	ConfirmationPending      Confirmation = "pending"
	ConfirmationConfirmed    Confirmation = "confirmed"
	ConfirmationDeclined     Confirmation = "declined"
	ConfirmationCallback     Confirmation = "callback_requested"

	PurposeChipping  Purpose = "chipping"
	PurposeTransport Purpose = "transport"
	PurposeTrailer   Purpose = "trailer"
	PurposeOther     Purpose = "other"

	PreflightBlocking PreflightSeverity = "blocking"
	PreflightWarning  PreflightSeverity = "warning"
	PreflightInfo     PreflightSeverity = "info"
)

type TimeInput struct {
	StartsAt            time.Time
	EndsAt              time.Time
	BufferBeforeMinutes int32
	BufferAfterMinutes  int32
}

type AssignmentInput struct {
	DriverIDs       []string
	PrimaryDriverID string
	Resources       []ResourceAssignment
	OverrideReason  string
}

type ResourceAssignment struct {
	ID      string
	Purpose Purpose
}

type DriverAssignment struct {
	ID, Name string
	Primary  bool
}

type AssignedResource struct {
	ID, Name  string
	Type      resource.Type
	Purpose   Purpose
	Exclusive bool
}

type Appointment struct {
	ID, JobID, JobNumber, JobWorkflow, JobType, TransportMode string
	PreferredStartDate, PreferredEndDate, PreferenceMode      string
	Lifecycle                                                 Lifecycle
	Confirmation                                              Confirmation
	StartsAt, EndsAt                                          time.Time
	BufferBeforeMinutes, BufferAfterMinutes                   int32
	AvailabilityOverrideReason                                string
	ExternalTransportConfirmed                                bool
	EstimatedHackMinutes, EstimatedTransportMinutes           int32
	Version                                                   int32
	Drivers                                                   []DriverAssignment
	Resources                                                 []AssignedResource
}

type CalendarEvent struct {
	Appointment
	CustomerID, CustomerName, Locality, Street, PostalCode string
	VolumeM3, Latitude, Longitude                          string
	MapsURL                                                string
}

type Detail struct {
	CalendarEvent
	Phone, Email, NotificationPreference string
	Notes                                []Note
	CanComplete                          bool
	CompleteRequiresOverride             bool
}

type Note struct {
	AuthorName string
	Body       string
	CreatedAt  time.Time
}

type PlanningDriver struct {
	ID, Name        string
	CanCompleteJobs bool
}

type PlanningResource struct {
	ID, Name    string
	Type        resource.Type
	IsExclusive bool
}

type WaitlistItem struct {
	WaitlistID, JobID, JobNumber, JobType, VolumeM3 string
	CustomerName, Locality                          string
	EstimatedHackMinutes, EstimatedTransportMinutes int32
	TransportMode                                   string
	ExternalTransportConfirmed                      bool
}

type PlanningOptions struct {
	Drivers   []PlanningDriver
	Resources []PlanningResource
	Waitlist  []WaitlistItem
}

type Conflict struct {
	Type, SubjectID, SubjectName, AppointmentID string
	JobNumber, CustomerName                     string
	StartsAt, EndsAt                            time.Time
	Reason                                      string
}

type Alternative struct{ StartsAt, EndsAt time.Time }

type ConflictResolution struct {
	RequestedStartsAt, RequestedEndsAt time.Time
	Conflicts                          []Conflict
	Alternatives                       []Alternative
}

type PreflightCheck struct {
	Key      string            `json:"key"`
	Label    string            `json:"label"`
	Detail   string            `json:"detail"`
	Passed   bool              `json:"passed"`
	Severity PreflightSeverity `json:"severity"`
}

type PreflightInput struct {
	AppointmentID, Action string
	ExpectedVersion       int32
	StartsAt, EndsAt      time.Time
	Assignments           *AssignmentInput
}

type Preflight struct {
	CurrentStartsAt     time.Time        `json:"current_starts_at"`
	CurrentEndsAt       time.Time        `json:"current_ends_at"`
	ProposedStartsAt    time.Time        `json:"proposed_starts_at"`
	ProposedEndsAt      time.Time        `json:"proposed_ends_at"`
	WorkingMinutes      int32            `json:"working_minutes"`
	TransportMinutes    int32            `json:"transport_minutes"`
	BufferBeforeMinutes int32            `json:"buffer_before_minutes"`
	BufferAfterMinutes  int32            `json:"buffer_after_minutes"`
	Checks              []PreflightCheck `json:"checks"`
	Conflicts           []Conflict       `json:"conflicts"`
}

func (s *Service) PreviewMutation(ctx context.Context, actor auth.Actor, input PreflightInput) (Preflight, error) {
	if err := actor.Require(auth.PermissionAppointmentPlan); err != nil {
		return Preflight{}, err
	}
	input.AppointmentID = strings.TrimSpace(input.AppointmentID)
	if input.AppointmentID == "" || input.ExpectedVersion < 1 {
		return Preflight{}, ErrValidation
	}
	current, err := s.store.Get(ctx, input.AppointmentID)
	if err != nil {
		return Preflight{}, err
	}
	startsAt, endsAt := input.StartsAt, input.EndsAt
	if startsAt.IsZero() && endsAt.IsZero() {
		startsAt, endsAt = current.StartsAt, current.EndsAt
	}
	if validateTime(TimeInput{StartsAt: startsAt, EndsAt: endsAt}) != nil {
		return Preflight{}, ErrValidation
	}
	candidate := current
	candidate.StartsAt, candidate.EndsAt = startsAt, endsAt
	if input.Assignments != nil {
		assignments := *input.Assignments
		normalizeAssignments(&assignments)
		if err := assignments.Validate(); err != nil {
			return Preflight{}, err
		}
		candidate, err = s.assignmentSnapshot(ctx, candidate, assignments)
		if err != nil {
			return Preflight{}, err
		}
	}
	result := Preflight{
		CurrentStartsAt: current.StartsAt, CurrentEndsAt: current.EndsAt,
		ProposedStartsAt: startsAt, ProposedEndsAt: endsAt,
		WorkingMinutes: current.EstimatedHackMinutes, TransportMinutes: current.EstimatedTransportMinutes,
		BufferBeforeMinutes: current.BufferBeforeMinutes, BufferAfterMinutes: current.BufferAfterMinutes,
		Checks: make([]PreflightCheck, 0, 8),
	}
	result.Checks = append(result.Checks,
		PreflightCheck{Key: "version", Label: "Terminversion", Passed: current.Version == input.ExpectedVersion, Severity: PreflightBlocking, Detail: "Aktueller Stand wird beim Speichern erneut geprüft."},
		PreflightCheck{Key: "job", Label: "Auftrag", Passed: strings.TrimSpace(current.JobID) != "", Severity: PreflightBlocking, Detail: current.JobNumber},
		PreflightCheck{Key: "time", Label: "Zeit und Dauer", Passed: endsAt.After(startsAt) && endsAt.Sub(startsAt) >= time.Duration(current.EstimatedHackMinutes+current.EstimatedTransportMinutes)*time.Minute, Severity: PreflightBlocking, Detail: "Arbeits-, Transport- und Pufferzeit sind getrennt ausgewiesen."},
	)
	result.Checks = append(result.Checks, customerPreferenceCheck(candidate, startsAt))
	primaryDriver := false
	for _, assigned := range candidate.Drivers {
		primaryDriver = primaryDriver || assigned.Primary
	}
	chipper := false
	transport := validateAppointmentTransport(candidate) == nil
	driverIDs := make([]string, 0, len(candidate.Drivers))
	for _, assigned := range candidate.Drivers {
		driverIDs = append(driverIDs, assigned.ID)
	}
	resourceIDs := make([]string, 0, len(candidate.Resources))
	for _, assigned := range candidate.Resources {
		chipper = chipper || assigned.Purpose == PurposeChipping
		if assigned.Exclusive {
			resourceIDs = append(resourceIDs, assigned.ID)
		}
	}
	result.Checks = append(result.Checks,
		PreflightCheck{Key: "driver", Label: "Primärfahrer", Passed: primaryDriver, Severity: PreflightBlocking, Detail: "Mindestens ein Fahrer und genau ein Primärfahrer."},
		PreflightCheck{Key: "chipper", Label: "Hackressource", Passed: chipper, Severity: PreflightBlocking, Detail: "Eine aktive Hackmaschine ist erforderlich."},
		PreflightCheck{Key: "transport", Label: "Transport", Passed: transport, Severity: PreflightBlocking, Detail: "Interner Transport benötigt ein Transportmittel; externer Transport muss bestätigt sein."},
	)
	from, to := reservationRange(candidate, startsAt, endsAt)
	availabilityPassed := true
	for _, assigned := range candidate.Drivers {
		status, _, availabilityErr := s.availability.IsAvailable(ctx, actor, assigned.ID, from, to)
		if availabilityErr != nil {
			if errors.Is(availabilityErr, driver.ErrNotFound) {
				availabilityPassed = false
				continue
			}
			return Preflight{}, availabilityErr
		}
		if status != driver.StatusAvailable {
			availabilityPassed = false
		}
	}
	conflicts, err := s.store.ListConflicts(ctx, from, to, driverIDs, resourceIDs, current.ID)
	if err != nil {
		return Preflight{}, err
	}
	result.Conflicts = conflicts
	result.Checks = append(result.Checks,
		PreflightCheck{Key: "availability", Label: "Fahrerverfügbarkeit", Passed: availabilityPassed, Severity: PreflightBlocking, Detail: "Abweichungen benötigen eine Admin-Begründung."},
		PreflightCheck{Key: "conflicts", Label: "Konflikte", Passed: len(conflicts) == 0, Severity: PreflightBlocking, Detail: fmt.Sprintf("%d betroffene Belegung(en)", len(conflicts))},
	)
	return result, nil
}

func customerPreferenceCheck(candidate Appointment, startsAt time.Time) PreflightCheck {
	check := PreflightCheck{
		Key: "customer_preference", Label: "Kundenwunsch", Passed: true, Severity: PreflightInfo,
		Detail: "Kein fester Wunschzeitraum hinterlegt.",
	}
	if candidate.PreferenceMode == "flexible" || candidate.PreferredStartDate == "" || candidate.PreferredEndDate == "" {
		return check
	}
	location, err := time.LoadLocation("Europe/Vienna")
	if err != nil {
		return check
	}
	localDate := startsAt.In(location).Format(time.DateOnly)
	start, startErr := time.Parse(time.DateOnly, candidate.PreferredStartDate)
	end, endErr := time.Parse(time.DateOnly, candidate.PreferredEndDate)
	if startErr != nil || endErr != nil {
		return check
	}
	period := start.Format("02.01.") + "–" + end.Format("02.01.2006")
	if localDate >= candidate.PreferredStartDate && localDate <= candidate.PreferredEndDate {
		check.Detail = "Der Termin liegt im Kundenwunsch " + period + "."
		return check
	}
	check.Passed = false
	check.Severity = PreflightWarning
	check.Detail = "Kunde möchte einen anderen Zeitraum (" + period + ")."
	return check
}

type SwapInput struct {
	FirstID, SecondID           string
	FirstVersion, SecondVersion int32
	RequestID                   string
}

type CreateDraftInput struct {
	JobID, RequestID string
	Time             TimeInput
}

// PlanInput is the complete, still-unfixed planning mutation. Stores must
// persist draft creation, assignments, and proposal transition atomically.
type PlanInput struct {
	CreateDraftInput
	Assignments AssignmentInput
}

type MutateInput struct {
	ID, RequestID   string
	ExpectedVersion int32
}

type MoveInput struct {
	MutateInput
	StartsAt                  time.Time
	EndsAt                    time.Time
	OverrideReason            string
	WithoutNotificationReason string
}

type ResizeInput = MoveInput

type AssignInput struct {
	MutateInput
	Assignments AssignmentInput
}

type CancelInput struct {
	MutateInput
	Reason string
}

// ReopenInput moves a cancelled appointment back into planning. Reason records
// why the historical cancellation is being reversed; OverrideReason is only
// used when the assigned driver's current availability requires an explicit
// administrator override.
type ReopenInput struct {
	MutateInput
	Reason         string
	OverrideReason string
}

type CompleteInput struct {
	MutateInput
	OverrideReason string
}

type FixInput struct {
	MutateInput
	WithoutNotificationReason string
}

type Store interface {
	Plan(context.Context, auth.Actor, PlanInput, string) (Appointment, error)
	CreateDraft(context.Context, auth.Actor, CreateDraftInput) (Appointment, error)
	Get(context.Context, string) (Appointment, error)
	Assign(context.Context, auth.Actor, AssignInput) (Appointment, error)
	Propose(context.Context, auth.Actor, MutateInput, string) (Appointment, error)
	Reschedule(context.Context, auth.Actor, MoveInput, string) (Appointment, error)
	Fix(context.Context, auth.Actor, FixInput) (Appointment, error)
	Cancel(context.Context, auth.Actor, CancelInput) (Appointment, error)
	Reopen(context.Context, auth.Actor, ReopenInput) (Appointment, error)
	Complete(context.Context, auth.Actor, CompleteInput) (Appointment, error)
	Detail(context.Context, string) (Detail, error)
	ListCalendar(context.Context, time.Time, time.Time) ([]CalendarEvent, error)
	PlanningOptions(context.Context) (PlanningOptions, error)
	ListConflicts(context.Context, time.Time, time.Time, []string, []string, string) ([]Conflict, error)
	DriverCanComplete(context.Context, string, string) (bool, error)
	Swap(context.Context, auth.Actor, SwapInput) ([]Appointment, error)
}

func (s *Service) ConflictAlternatives(ctx context.Context, actor auth.Actor, appointmentID string, startsAt, endsAt time.Time) (ConflictResolution, error) {
	if err := actor.Require(auth.PermissionAppointmentPlan); err != nil {
		return ConflictResolution{}, err
	}
	if strings.TrimSpace(appointmentID) == "" || startsAt.Location() != time.UTC || endsAt.Location() != time.UTC || validateTime(TimeInput{StartsAt: startsAt, EndsAt: endsAt}) != nil {
		return ConflictResolution{}, ErrValidation
	}
	current, err := s.store.Get(ctx, appointmentID)
	if err != nil {
		return ConflictResolution{}, err
	}
	driverIDs := make([]string, 0, len(current.Drivers))
	for _, item := range current.Drivers {
		driverIDs = append(driverIDs, item.ID)
	}
	resourceIDs := make([]string, 0, len(current.Resources))
	for _, item := range current.Resources {
		if item.Exclusive {
			resourceIDs = append(resourceIDs, item.ID)
		}
	}
	from, to := reservationRange(current, startsAt, endsAt)
	conflicts, err := s.store.ListConflicts(ctx, from, to, driverIDs, resourceIDs, appointmentID)
	if err != nil {
		return ConflictResolution{}, err
	}
	result := ConflictResolution{RequestedStartsAt: startsAt, RequestedEndsAt: endsAt, Conflicts: conflicts}
	duration := endsAt.Sub(startsAt)
	location, locationErr := time.LoadLocation("Europe/Vienna")
	if locationErr != nil {
		return ConflictResolution{}, locationErr
	}
	for cursor, checked := startsAt.Add(15*time.Minute), 0; len(result.Alternatives) < 3 && checked < 14*24*4; cursor, checked = cursor.Add(15*time.Minute), checked+1 {
		local := cursor.In(location)
		endLocal := cursor.Add(duration).In(location)
		if local.Weekday() == time.Saturday || local.Weekday() == time.Sunday || local.Hour() < 7 || endLocal.Day() != local.Day() || endLocal.Hour() > 17 || (endLocal.Hour() == 17 && endLocal.Minute() > 0) {
			continue
		}
		candidateFrom, candidateTo := reservationRange(current, cursor, cursor.Add(duration))
		available := true
		for _, assigned := range current.Drivers {
			if _, availabilityErr := s.checkAvailability(ctx, actor, []DriverAssignment{assigned}, candidateFrom, candidateTo, ""); availabilityErr != nil {
				available = false
				break
			}
		}
		if !available {
			continue
		}
		candidateConflicts, listErr := s.store.ListConflicts(ctx, candidateFrom, candidateTo, driverIDs, resourceIDs, appointmentID)
		if listErr != nil {
			return ConflictResolution{}, listErr
		}
		if len(candidateConflicts) == 0 {
			result.Alternatives = append(result.Alternatives, Alternative{StartsAt: cursor, EndsAt: cursor.Add(duration)})
		}
	}
	return result, nil
}

func (s *Service) SwapAppointments(ctx context.Context, actor auth.Actor, input SwapInput) ([]Appointment, error) {
	if err := actor.Require(auth.PermissionAppointmentPlan); err != nil {
		return nil, err
	}
	input.FirstID, input.SecondID = strings.TrimSpace(input.FirstID), strings.TrimSpace(input.SecondID)
	if input.FirstID == "" || input.SecondID == "" || input.FirstID == input.SecondID || input.FirstVersion < 1 || input.SecondVersion < 1 {
		return nil, ErrValidation
	}
	first, err := s.store.Get(ctx, input.FirstID)
	if err != nil {
		return nil, err
	}
	second, err := s.store.Get(ctx, input.SecondID)
	if err != nil {
		return nil, err
	}
	if first.Version != input.FirstVersion || second.Version != input.SecondVersion {
		return nil, ErrVersionConflict
	}
	if !swapEligible(first.Lifecycle) || !swapEligible(second.Lifecycle) {
		return nil, ErrTransition
	}
	firstFrom, firstTo := reservationRange(first, second.StartsAt, second.StartsAt.Add(first.EndsAt.Sub(first.StartsAt)))
	secondFrom, secondTo := reservationRange(second, first.StartsAt, first.StartsAt.Add(second.EndsAt.Sub(second.StartsAt)))
	if _, err := s.checkAvailability(ctx, actor, first.Drivers, firstFrom, firstTo, ""); err != nil {
		return nil, err
	}
	if _, err := s.checkAvailability(ctx, actor, second.Drivers, secondFrom, secondTo, ""); err != nil {
		return nil, err
	}
	return s.store.Swap(ctx, actor, input)
}

func swapEligible(value Lifecycle) bool { return value == LifecycleDraft || value == LifecycleProposal }

type Availability interface {
	IsAvailable(context.Context, auth.Actor, string, time.Time, time.Time) (driver.Status, []string, error)
}

type Service struct {
	store        Store
	availability Availability
	now          func() time.Time
}

func New(store Store, availability Availability, now func() time.Time) (*Service, error) {
	if store == nil || availability == nil {
		return nil, errors.New("appointment: store and availability are required")
	}
	if now == nil {
		now = time.Now
	}
	return &Service{store: store, availability: availability, now: now}, nil
}

func (s *Service) CreateDraftFromWaitlist(ctx context.Context, actor auth.Actor, input CreateDraftInput) (Appointment, error) {
	if err := actor.Require(auth.PermissionAppointmentPlan); err != nil {
		return Appointment{}, err
	}
	input.JobID = strings.TrimSpace(input.JobID)
	if input.JobID == "" || validateTime(input.Time) != nil {
		return Appointment{}, ErrValidation
	}
	return s.store.CreateDraft(ctx, actor, input)
}

// PlanFromWaitlist validates the full proposal before asking the store to
// commit the complete plan as one transaction. No draft is exposed if any
// phase fails or the request is cancelled.
func (s *Service) PlanFromWaitlist(ctx context.Context, actor auth.Actor, input PlanInput) (Appointment, error) {
	if err := actor.Require(auth.PermissionAppointmentPlan); err != nil {
		return Appointment{}, err
	}
	input.JobID = strings.TrimSpace(input.JobID)
	normalizeAssignments(&input.Assignments)
	if input.JobID == "" || validateTime(input.Time) != nil {
		return Appointment{}, ErrValidation
	}
	if err := input.Assignments.Validate(); err != nil {
		return Appointment{}, err
	}
	options, err := s.store.PlanningOptions(ctx)
	if err != nil {
		return Appointment{}, err
	}
	var item WaitlistItem
	found := false
	for _, candidate := range options.Waitlist {
		if candidate.JobID == input.JobID {
			item, found = candidate, true
			break
		}
	}
	if !found {
		return Appointment{}, ErrNotFound
	}
	prospective := Appointment{
		JobID: item.JobID, JobNumber: item.JobNumber, JobType: item.JobType,
		TransportMode: item.TransportMode, ExternalTransportConfirmed: item.ExternalTransportConfirmed,
		EstimatedHackMinutes: item.EstimatedHackMinutes, EstimatedTransportMinutes: item.EstimatedTransportMinutes,
		StartsAt: input.Time.StartsAt, EndsAt: input.Time.EndsAt,
		BufferBeforeMinutes: input.Time.BufferBeforeMinutes, BufferAfterMinutes: input.Time.BufferAfterMinutes,
	}
	prospective, err = assignmentSnapshot(prospective, input.Assignments, options)
	if err != nil {
		return Appointment{}, err
	}
	if err := validateAppointmentAssignments(prospective); err != nil {
		return Appointment{}, err
	}
	required := time.Duration(prospective.EstimatedHackMinutes+prospective.EstimatedTransportMinutes) * time.Minute
	if input.Time.EndsAt.Sub(input.Time.StartsAt) < required {
		return Appointment{}, ErrValidation
	}
	from, to := reservationRange(prospective, prospective.StartsAt, prospective.EndsAt)
	override, err := s.checkAvailability(ctx, actor, prospective.Drivers, from, to, input.Assignments.OverrideReason)
	if err != nil {
		return Appointment{}, err
	}
	return s.store.Plan(ctx, actor, input, override)
}

func (s *Service) AssignDriversAndResources(ctx context.Context, actor auth.Actor, input AssignInput) (Appointment, error) {
	if err := actor.Require(auth.PermissionAppointmentPlan); err != nil {
		return Appointment{}, err
	}
	normalizeAssignments(&input.Assignments)
	if err := validateMutation(input.MutateInput); err != nil {
		return Appointment{}, err
	}
	if err := input.Assignments.Validate(); err != nil {
		return Appointment{}, err
	}
	current, err := s.store.Get(ctx, input.ID)
	if err != nil {
		return Appointment{}, err
	}
	if current.Version != input.ExpectedVersion {
		return Appointment{}, ErrVersionConflict
	}
	if !current.Lifecycle.Editable() {
		return Appointment{}, ErrTransition
	}
	candidate, err := s.assignmentSnapshot(ctx, current, input.Assignments)
	if err != nil {
		return Appointment{}, err
	}
	if err := validateAppointmentAssignments(candidate); err != nil {
		return Appointment{}, err
	}
	availableFrom, availableTo := reservationRange(current, current.StartsAt, current.EndsAt)
	if _, err := s.checkAvailability(ctx, actor, candidate.Drivers, availableFrom, availableTo, input.Assignments.OverrideReason); err != nil {
		return Appointment{}, err
	}
	return s.store.Assign(ctx, actor, input)
}

func (s *Service) ProposeAppointment(ctx context.Context, actor auth.Actor, input MutateInput, overrideReason string) (Appointment, error) {
	if err := actor.Require(auth.PermissionAppointmentPlan); err != nil {
		return Appointment{}, err
	}
	if err := validateMutation(input); err != nil {
		return Appointment{}, err
	}
	current, err := s.store.Get(ctx, input.ID)
	if err != nil {
		return Appointment{}, err
	}
	if current.Version != input.ExpectedVersion {
		return Appointment{}, ErrVersionConflict
	}
	if current.Lifecycle != LifecycleDraft {
		return Appointment{}, ErrTransition
	}
	if err := validateAppointmentAssignments(current); err != nil {
		return Appointment{}, err
	}
	availableFrom, availableTo := reservationRange(current, current.StartsAt, current.EndsAt)
	overrideReason, err = s.checkAvailability(ctx, actor, current.Drivers, availableFrom, availableTo, overrideReason)
	if err != nil {
		return Appointment{}, err
	}
	return s.store.Propose(ctx, actor, input, overrideReason)
}

func (s *Service) MoveAppointment(ctx context.Context, actor auth.Actor, input MoveInput) (Appointment, error) {
	return s.reschedule(ctx, actor, input)
}

func (s *Service) ResizeAppointment(ctx context.Context, actor auth.Actor, input ResizeInput) (Appointment, error) {
	return s.reschedule(ctx, actor, input)
}

func (s *Service) reschedule(ctx context.Context, actor auth.Actor, input MoveInput) (Appointment, error) {
	if err := actor.Require(auth.PermissionAppointmentPlan); err != nil {
		return Appointment{}, err
	}
	if err := validateMutation(input.MutateInput); err != nil || validateTime(TimeInput{StartsAt: input.StartsAt, EndsAt: input.EndsAt}) != nil {
		return Appointment{}, ErrValidation
	}
	input.WithoutNotificationReason = strings.TrimSpace(input.WithoutNotificationReason)
	if len([]rune(input.WithoutNotificationReason)) > 1000 {
		return Appointment{}, ErrValidation
	}
	current, err := s.store.Get(ctx, input.ID)
	if err != nil {
		return Appointment{}, err
	}
	if current.Version != input.ExpectedVersion {
		return Appointment{}, ErrVersionConflict
	}
	if !current.Lifecycle.Editable() {
		return Appointment{}, ErrTransition
	}
	availableFrom, availableTo := reservationRange(current, input.StartsAt, input.EndsAt)
	override, err := s.checkAvailability(ctx, actor, current.Drivers, availableFrom, availableTo, input.OverrideReason)
	if err != nil {
		return Appointment{}, err
	}
	return s.store.Reschedule(ctx, actor, input, override)
}

func (s *Service) FixAppointment(ctx context.Context, actor auth.Actor, input FixInput) (Appointment, error) {
	if err := actor.Require(auth.PermissionAppointmentFix); err != nil {
		return Appointment{}, err
	}
	if err := validateMutation(input.MutateInput); err != nil {
		return Appointment{}, err
	}
	input.WithoutNotificationReason = strings.TrimSpace(input.WithoutNotificationReason)
	if len([]rune(input.WithoutNotificationReason)) > 1000 {
		return Appointment{}, ErrValidation
	}
	current, err := s.store.Get(ctx, input.ID)
	if err != nil {
		return Appointment{}, err
	}
	if current.Version != input.ExpectedVersion {
		return Appointment{}, ErrVersionConflict
	}
	if current.Lifecycle != LifecycleProposal {
		return Appointment{}, ErrTransition
	}
	if err := validateAppointmentAssignments(current); err != nil {
		return Appointment{}, err
	}
	availableFrom, availableTo := reservationRange(current, current.StartsAt, current.EndsAt)
	if _, err := s.checkAvailability(ctx, actor, current.Drivers, availableFrom, availableTo, current.AvailabilityOverrideReason); err != nil {
		return Appointment{}, err
	}
	return s.store.Fix(ctx, actor, input)
}

func (s *Service) assignmentSnapshot(ctx context.Context, current Appointment, input AssignmentInput) (Appointment, error) {
	options, err := s.store.PlanningOptions(ctx)
	if err != nil {
		return Appointment{}, err
	}
	return assignmentSnapshot(current, input, options)
}

func assignmentSnapshot(current Appointment, input AssignmentInput, options PlanningOptions) (Appointment, error) {
	driverNames := make(map[string]string, len(options.Drivers))
	for _, item := range options.Drivers {
		driverNames[item.ID] = item.Name
	}
	resourceOptions := make(map[string]PlanningResource, len(options.Resources))
	for _, item := range options.Resources {
		resourceOptions[item.ID] = item
	}
	current.Drivers = make([]DriverAssignment, 0, len(input.DriverIDs))
	for _, id := range input.DriverIDs {
		name, ok := driverNames[id]
		if !ok {
			return Appointment{}, ErrValidation
		}
		current.Drivers = append(current.Drivers, DriverAssignment{ID: id, Name: name, Primary: id == input.PrimaryDriverID})
	}
	current.Resources = make([]AssignedResource, 0, len(input.Resources))
	for _, assigned := range input.Resources {
		option, ok := resourceOptions[assigned.ID]
		if !ok {
			return Appointment{}, ErrValidation
		}
		current.Resources = append(current.Resources, AssignedResource{
			ID: option.ID, Name: option.Name, Type: option.Type, Purpose: assigned.Purpose, Exclusive: option.IsExclusive,
		})
	}
	return current, nil
}

func (s *Service) CancelAppointment(ctx context.Context, actor auth.Actor, input CancelInput) (Appointment, error) {
	if err := actor.Require(auth.PermissionAppointmentCancel); err != nil {
		return Appointment{}, err
	}
	input.Reason = strings.TrimSpace(input.Reason)
	if err := validateMutation(input.MutateInput); err != nil || len([]rune(input.Reason)) > 1000 {
		return Appointment{}, ErrValidation
	}
	current, err := s.store.Get(ctx, input.ID)
	if err != nil {
		return Appointment{}, err
	}
	if current.Lifecycle == LifecycleFixed && input.Reason == "" {
		return Appointment{}, ErrValidation
	}
	if !current.Lifecycle.Editable() {
		return Appointment{}, ErrTransition
	}
	return s.store.Cancel(ctx, actor, input)
}

func (s *Service) ReopenAppointment(ctx context.Context, actor auth.Actor, input ReopenInput) (Appointment, error) {
	if err := actor.Require(auth.PermissionAppointmentCancel); err != nil {
		return Appointment{}, err
	}
	if err := actor.Require(auth.PermissionAppointmentPlan); err != nil {
		return Appointment{}, err
	}
	input.Reason = strings.TrimSpace(input.Reason)
	input.OverrideReason = strings.TrimSpace(input.OverrideReason)
	if err := validateMutation(input.MutateInput); err != nil || input.Reason == "" ||
		len([]rune(input.Reason)) > 1000 || len([]rune(input.OverrideReason)) > 1000 {
		return Appointment{}, ErrValidation
	}
	current, err := s.store.Get(ctx, input.ID)
	if err != nil {
		return Appointment{}, err
	}
	if current.Version != input.ExpectedVersion {
		return Appointment{}, ErrVersionConflict
	}
	if current.Lifecycle != LifecycleCancelled {
		return Appointment{}, ErrTransition
	}
	if err := validateAppointmentAssignments(current); err != nil {
		return Appointment{}, err
	}
	availableFrom, availableTo := reservationRange(current, current.StartsAt, current.EndsAt)
	input.OverrideReason, err = s.checkAvailability(
		ctx,
		actor,
		current.Drivers,
		availableFrom,
		availableTo,
		input.OverrideReason,
	)
	if err != nil {
		return Appointment{}, err
	}
	return s.store.Reopen(ctx, actor, input)
}

func (s *Service) CompleteAppointment(ctx context.Context, actor auth.Actor, input CompleteInput) (Appointment, error) {
	if err := actor.Require(auth.PermissionAppointmentComplete); err != nil {
		return Appointment{}, err
	}
	if err := validateMutation(input.MutateInput); err != nil {
		return Appointment{}, err
	}
	current, err := s.store.Get(ctx, input.ID)
	if err != nil {
		return Appointment{}, err
	}
	if current.Lifecycle != LifecycleFixed {
		return Appointment{}, ErrTransition
	}
	input.OverrideReason = strings.TrimSpace(input.OverrideReason)
	if len([]rune(input.OverrideReason)) > 1000 {
		return Appointment{}, ErrValidation
	}
	if actor.Role == auth.RoleAdmin {
		if s.now().Before(current.StartsAt) && input.OverrideReason == "" {
			return Appointment{}, ErrValidation
		}
	} else {
		if actor.DriverID == "" || s.now().Before(current.StartsAt) {
			return Appointment{}, auth.ErrForbidden
		}
		allowed, canErr := s.store.DriverCanComplete(ctx, input.ID, actor.DriverID)
		if canErr != nil {
			return Appointment{}, canErr
		}
		if !allowed {
			return Appointment{}, auth.ErrForbidden
		}
	}
	return s.store.Complete(ctx, actor, input)
}

func (s *Service) ListCalendarRange(ctx context.Context, actor auth.Actor, fromUTC, toUTC time.Time) ([]CalendarEvent, error) {
	if err := actor.Require(auth.PermissionCalendarViewAll); err != nil {
		return nil, err
	}
	if err := validateRange(fromUTC, toUTC); err != nil {
		return nil, err
	}
	return s.store.ListCalendar(ctx, fromUTC, toUTC)
}

func (s *Service) SwapCandidates(ctx context.Context, actor auth.Actor, excludeID string, fromUTC, toUTC time.Time) ([]CalendarEvent, error) {
	if err := actor.Require(auth.PermissionAppointmentPlan); err != nil {
		return nil, err
	}
	excludeID = strings.TrimSpace(excludeID)
	if excludeID == "" || validateRange(fromUTC, toUTC) != nil {
		return nil, ErrValidation
	}
	events, err := s.store.ListCalendar(ctx, fromUTC, toUTC)
	if err != nil {
		return nil, err
	}
	result := make([]CalendarEvent, 0, len(events))
	for _, event := range events {
		if event.ID != excludeID && swapEligible(event.Lifecycle) {
			result = append(result, event)
		}
	}
	return result, nil
}

func (s *Service) AppointmentDetail(ctx context.Context, actor auth.Actor, id string) (Detail, error) {
	if err := actor.Require(auth.PermissionCalendarViewAll); err != nil {
		return Detail{}, err
	}
	if strings.TrimSpace(id) == "" {
		return Detail{}, ErrNotFound
	}
	value, err := s.store.Detail(ctx, id)
	if err != nil || value.Lifecycle != LifecycleFixed || !actor.Role.Allows(auth.PermissionAppointmentComplete) {
		return value, err
	}
	if actor.Role == auth.RoleAdmin {
		value.CanComplete = true
		value.CompleteRequiresOverride = s.now().Before(value.StartsAt)
		return value, nil
	}
	if actor.DriverID == "" || s.now().Before(value.StartsAt) {
		return value, nil
	}
	value.CanComplete, err = s.store.DriverCanComplete(ctx, value.ID, actor.DriverID)
	return value, err
}

func (s *Service) PlanningOptions(ctx context.Context, actor auth.Actor) (PlanningOptions, error) {
	if err := actor.Require(auth.PermissionAppointmentPlan); err != nil {
		return PlanningOptions{}, err
	}
	return s.store.PlanningOptions(ctx)
}

func (s *Service) ListConflictsAndCapacity(ctx context.Context, actor auth.Actor, fromUTC, toUTC time.Time, driverIDs, resourceIDs []string, excludeID string) ([]Conflict, error) {
	if err := actor.Require(auth.PermissionCalendarViewAll); err != nil {
		return nil, err
	}
	if err := validateRange(fromUTC, toUTC); err != nil {
		return nil, err
	}
	return s.store.ListConflicts(ctx, fromUTC, toUTC, driverIDs, resourceIDs, excludeID)
}

func (s *Service) checkAvailability(ctx context.Context, actor auth.Actor, assignments []DriverAssignment, startsAt, endsAt time.Time, reason string) (string, error) {
	reason = strings.TrimSpace(reason)
	for _, assignment := range assignments {
		status, _, err := s.availability.IsAvailable(ctx, actor, assignment.ID, startsAt.UTC(), endsAt.UTC())
		if err != nil {
			return "", err
		}
		if status != driver.StatusAvailable && reason == "" {
			return "", fmt.Errorf("%w: %s", ErrAvailability, assignment.Name)
		}
	}
	if len([]rune(reason)) > 1000 {
		return "", ErrValidation
	}
	return reason, nil
}

func reservationRange(current Appointment, startsAt, endsAt time.Time) (time.Time, time.Time) {
	return startsAt.Add(-time.Duration(current.BufferBeforeMinutes) * time.Minute),
		endsAt.Add(time.Duration(current.BufferAfterMinutes) * time.Minute)
}

func validateAppointmentAssignments(value Appointment) error {
	if len(value.Drivers) == 0 || !slices.ContainsFunc(value.Drivers, func(item DriverAssignment) bool { return item.Primary }) {
		return fmt.Errorf("%w: primary driver required", ErrValidation)
	}
	if !slices.ContainsFunc(value.Resources, func(item AssignedResource) bool {
		return item.Type == resource.TypeChipper && item.Purpose == PurposeChipping
	}) {
		return fmt.Errorf("%w: chipping resource required", ErrValidation)
	}
	return validateAppointmentTransport(value)
}

func validateAppointmentTransport(value Appointment) error {
	if value.JobType != "chipping_with_transport" {
		return nil
	}
	switch value.TransportMode {
	case "internal":
		if !slices.ContainsFunc(value.Resources, func(item AssignedResource) bool {
			return item.Type == resource.TypeTransportVehicle && item.Purpose == PurposeTransport
		}) {
			return fmt.Errorf("%w: transport resource required", ErrValidation)
		}
	case "external":
		if !value.ExternalTransportConfirmed {
			return fmt.Errorf("%w: external transport not confirmed", ErrValidation)
		}
	default:
		return fmt.Errorf("%w: transport plan required", ErrValidation)
	}
	return nil
}

func (input AssignmentInput) Validate() error {
	if len(input.DriverIDs) == 0 || input.PrimaryDriverID == "" || !slices.Contains(input.DriverIDs, input.PrimaryDriverID) {
		return ErrValidation
	}
	seen := make(map[string]bool, len(input.DriverIDs)+len(input.Resources))
	for _, id := range input.DriverIDs {
		if id == "" || seen["d:"+id] {
			return ErrValidation
		}
		seen["d:"+id] = true
	}
	for _, assigned := range input.Resources {
		if assigned.ID == "" || !assigned.Purpose.Valid() || seen["r:"+assigned.ID] {
			return ErrValidation
		}
		seen["r:"+assigned.ID] = true
	}
	return nil
}

func (purpose Purpose) Valid() bool {
	return purpose == PurposeChipping || purpose == PurposeTransport || purpose == PurposeTrailer || purpose == PurposeOther
}

func (lifecycle Lifecycle) Editable() bool {
	return lifecycle == LifecycleDraft || lifecycle == LifecycleProposal || lifecycle == LifecycleFixed
}

func validateTime(input TimeInput) error {
	if input.StartsAt.IsZero() || input.EndsAt.IsZero() || !input.EndsAt.After(input.StartsAt) ||
		input.EndsAt.Sub(input.StartsAt) > MaxDuration || input.BufferBeforeMinutes < 0 || input.BufferBeforeMinutes > 1440 ||
		input.BufferAfterMinutes < 0 || input.BufferAfterMinutes > 1440 {
		return ErrValidation
	}
	return nil
}

func validateRange(fromUTC, toUTC time.Time) error {
	if fromUTC.Location() != time.UTC || toUTC.Location() != time.UTC || !toUTC.After(fromUTC) || toUTC.Sub(fromUTC) > MaxCalendarRange {
		return ErrValidation
	}
	return nil
}

func validateMutation(input MutateInput) error {
	if strings.TrimSpace(input.ID) == "" || input.ExpectedVersion < 1 {
		return ErrValidation
	}
	return nil
}

func normalizeAssignments(input *AssignmentInput) {
	input.PrimaryDriverID = strings.TrimSpace(input.PrimaryDriverID)
	input.OverrideReason = strings.TrimSpace(input.OverrideReason)
	for index := range input.DriverIDs {
		input.DriverIDs[index] = strings.TrimSpace(input.DriverIDs[index])
	}
	for index := range input.Resources {
		input.Resources[index].ID = strings.TrimSpace(input.Resources[index].ID)
	}
}
