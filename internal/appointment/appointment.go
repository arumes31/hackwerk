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
	ErrAvailability = errors.New("appointment: driver unavailable")
	ErrConflict     = errors.New("appointment: reservation conflict")
	ErrNotFound     = errors.New("appointment: not found")
	ErrTransition   = errors.New("appointment: invalid transition")
	ErrValidation   = errors.New("appointment: validation failed")
)

type Lifecycle string
type Confirmation string
type Purpose string

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
	Lifecycle                                                 Lifecycle
	Confirmation                                              Confirmation
	StartsAt, EndsAt                                          time.Time
	BufferBeforeMinutes, BufferAfterMinutes                   int32
	AvailabilityOverrideReason                                string
	ExternalTransportConfirmed                                bool
	EstimatedHackMinutes                                      int32
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
	EstimatedHackMinutes                            int32
}

type PlanningOptions struct {
	Drivers   []PlanningDriver
	Resources []PlanningResource
	Waitlist  []WaitlistItem
}

type Conflict struct {
	Type, SubjectID, SubjectName, AppointmentID string
	StartsAt, EndsAt                            time.Time
	Reason                                      string
}

type CreateDraftInput struct {
	JobID, RequestID string
	Time             TimeInput
}

type MutateInput struct {
	ID, RequestID   string
	ExpectedVersion int32
}

type MoveInput struct {
	MutateInput
	StartsAt       time.Time
	EndsAt         time.Time
	OverrideReason string
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

type CompleteInput struct {
	MutateInput
	OverrideReason string
}

type Store interface {
	CreateDraft(context.Context, auth.Actor, CreateDraftInput) (Appointment, error)
	Get(context.Context, string) (Appointment, error)
	Assign(context.Context, auth.Actor, AssignInput) (Appointment, error)
	Propose(context.Context, auth.Actor, MutateInput, string) (Appointment, error)
	Reschedule(context.Context, auth.Actor, MoveInput, string) (Appointment, error)
	Fix(context.Context, auth.Actor, MutateInput) (Appointment, error)
	Cancel(context.Context, auth.Actor, CancelInput) (Appointment, error)
	Complete(context.Context, auth.Actor, CompleteInput) (Appointment, error)
	ListCalendar(context.Context, time.Time, time.Time) ([]CalendarEvent, error)
	PlanningOptions(context.Context) (PlanningOptions, error)
	ListConflicts(context.Context, time.Time, time.Time, []string, []string, string) ([]Conflict, error)
	DriverCanComplete(context.Context, string, string) (bool, error)
}

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
	if current.Version != input.ExpectedVersion || !current.Lifecycle.Editable() {
		return Appointment{}, ErrConflict
	}
	candidate, err := s.assignmentSnapshot(ctx, current, input.Assignments)
	if err != nil {
		return Appointment{}, err
	}
	if err := validateAppointmentAssignments(candidate); err != nil {
		return Appointment{}, err
	}
	if _, err := s.checkAvailability(ctx, actor, candidate.Drivers, current.StartsAt, current.EndsAt, input.Assignments.OverrideReason); err != nil {
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
	if current.Version != input.ExpectedVersion || current.Lifecycle != LifecycleDraft {
		return Appointment{}, ErrConflict
	}
	if err := validateAppointmentAssignments(current); err != nil {
		return Appointment{}, err
	}
	overrideReason, err = s.checkAvailability(ctx, actor, current.Drivers, current.StartsAt, current.EndsAt, overrideReason)
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
	current, err := s.store.Get(ctx, input.ID)
	if err != nil {
		return Appointment{}, err
	}
	if current.Version != input.ExpectedVersion || !current.Lifecycle.Editable() {
		return Appointment{}, ErrConflict
	}
	override, err := s.checkAvailability(ctx, actor, current.Drivers, input.StartsAt, input.EndsAt, input.OverrideReason)
	if err != nil {
		return Appointment{}, err
	}
	return s.store.Reschedule(ctx, actor, input, override)
}

func (s *Service) FixAppointment(ctx context.Context, actor auth.Actor, input MutateInput) (Appointment, error) {
	if err := actor.Require(auth.PermissionAppointmentFix); err != nil {
		return Appointment{}, err
	}
	if err := validateMutation(input); err != nil {
		return Appointment{}, err
	}
	current, err := s.store.Get(ctx, input.ID)
	if err != nil {
		return Appointment{}, err
	}
	if current.Version != input.ExpectedVersion || current.Lifecycle != LifecycleProposal {
		return Appointment{}, ErrConflict
	}
	if err := validateAppointmentAssignments(current); err != nil {
		return Appointment{}, err
	}
	if _, err := s.checkAvailability(ctx, actor, current.Drivers, current.StartsAt, current.EndsAt, current.AvailabilityOverrideReason); err != nil {
		return Appointment{}, err
	}
	return s.store.Fix(ctx, actor, input)
}

func (s *Service) assignmentSnapshot(ctx context.Context, current Appointment, input AssignmentInput) (Appointment, error) {
	options, err := s.store.PlanningOptions(ctx)
	if err != nil {
		return Appointment{}, err
	}
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

func (s *Service) PlanningOptions(ctx context.Context, actor auth.Actor) (PlanningOptions, error) {
	if err := actor.Require(auth.PermissionCalendarViewAll); err != nil {
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

func validateAppointmentAssignments(value Appointment) error {
	if len(value.Drivers) == 0 || !slices.ContainsFunc(value.Drivers, func(item DriverAssignment) bool { return item.Primary }) {
		return fmt.Errorf("%w: primary driver required", ErrValidation)
	}
	if !slices.ContainsFunc(value.Resources, func(item AssignedResource) bool {
		return item.Type == resource.TypeChipper && item.Purpose == PurposeChipping
	}) {
		return fmt.Errorf("%w: chipping resource required", ErrValidation)
	}
	if value.JobType == "chipping_with_transport" {
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
