package appointment

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"example.invalid/hackplan/internal/auth"
	"example.invalid/hackplan/internal/driver"
	"example.invalid/hackplan/internal/resource"
)

func TestDriverCannotPlanDirectly(t *testing.T) {
	service := testService(t, Appointment{}, driver.StatusAvailable)
	_, err := service.CreateDraftFromWaitlist(t.Context(), auth.Actor{UserID: "user", Role: auth.RoleDriver}, CreateDraftInput{
		JobID: "job", Time: TimeInput{StartsAt: testStart(), EndsAt: testStart().Add(time.Hour)},
	})
	if !errors.Is(err, auth.ErrForbidden) {
		t.Fatalf("CreateDraftFromWaitlist() error = %v, want forbidden", err)
	}
}

func TestTransportAssignmentIsValidatedBeforePersistence(t *testing.T) {
	current := testAppointment()
	current.JobType = "chipping_with_transport"
	current.TransportMode = "internal"
	store := &fakeStore{current: current, options: testOptions()}
	service, err := New(store, fakeAvailability{status: driver.StatusAvailable}, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.AssignDriversAndResources(t.Context(), testAdmin(), AssignInput{
		MutateInput: MutateInput{ID: current.ID, ExpectedVersion: current.Version},
		Assignments: AssignmentInput{
			DriverIDs: []string{"driver-1"}, PrimaryDriverID: "driver-1",
			Resources: []ResourceAssignment{{ID: "chipper-1", Purpose: PurposeChipping}},
		},
	})
	if !errors.Is(err, ErrValidation) || store.assignCalled {
		t.Fatalf("AssignDriversAndResources() error/called = %v/%v, want validation/no persistence", err, store.assignCalled)
	}
}

func TestAvailabilityOverrideRequiresReason(t *testing.T) {
	current := testAppointment()
	store := &fakeStore{current: current, options: testOptions()}
	service, err := New(store, fakeAvailability{status: driver.StatusUnavailable}, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	input := AssignInput{
		MutateInput: MutateInput{ID: current.ID, ExpectedVersion: current.Version},
		Assignments: AssignmentInput{
			DriverIDs: []string{"driver-1"}, PrimaryDriverID: "driver-1",
			Resources: []ResourceAssignment{{ID: "chipper-1", Purpose: PurposeChipping}},
		},
	}
	if _, err := service.AssignDriversAndResources(t.Context(), testAdmin(), input); !errors.Is(err, ErrAvailability) {
		t.Fatalf("without reason error = %v, want unavailable", err)
	}
	input.Assignments.OverrideReason = "Fahrer hat den zusätzlichen Einsatz bestätigt"
	if _, err := service.AssignDriversAndResources(t.Context(), testAdmin(), input); err != nil {
		t.Fatalf("with reason error = %v", err)
	}
}

func TestAvailabilityCheckIncludesAppointmentBuffers(t *testing.T) {
	current := testAppointment()
	current.BufferBeforeMinutes = 30
	current.BufferAfterMinutes = 45
	store := &fakeStore{current: current, options: testOptions()}
	availability := &capturingAvailability{status: driver.StatusAvailable}
	service, err := New(store, availability, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.AssignDriversAndResources(t.Context(), testAdmin(), AssignInput{
		MutateInput: MutateInput{ID: current.ID, ExpectedVersion: current.Version},
		Assignments: AssignmentInput{
			DriverIDs: []string{"driver-1"}, PrimaryDriverID: "driver-1",
			Resources: []ResourceAssignment{{ID: "chipper-1", Purpose: PurposeChipping}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if want := current.StartsAt.Add(-30 * time.Minute); !availability.from.Equal(want) {
		t.Fatalf("availability start=%s want %s", availability.from, want)
	}
	if want := current.EndsAt.Add(45 * time.Minute); !availability.to.Equal(want) {
		t.Fatalf("availability end=%s want %s", availability.to, want)
	}
}

func TestCalendarRangeIsBoundedAndUTC(t *testing.T) {
	service := testService(t, Appointment{}, driver.StatusAvailable)
	admin := testAdmin()
	local := time.FixedZone("local", 3600)
	if _, err := service.ListCalendarRange(t.Context(), admin, time.Now().In(local), time.Now().Add(time.Hour).In(local)); !errors.Is(err, ErrValidation) {
		t.Fatalf("non-UTC range error = %v", err)
	}
	from := time.Now().UTC()
	if _, err := service.ListCalendarRange(t.Context(), admin, from, from.Add(MaxCalendarRange+time.Minute)); !errors.Is(err, ErrValidation) {
		t.Fatalf("oversize range error = %v", err)
	}
}

func TestAppointmentDetailReportsCompletionPermission(t *testing.T) {
	now := testStart().Add(time.Hour)
	tests := []struct {
		name          string
		actor         auth.Actor
		startsAt      time.Time
		driverAllowed bool
		wantComplete  bool
		wantReason    bool
	}{
		{name: "admin after start", actor: testAdmin(), startsAt: testStart(), wantComplete: true},
		{name: "admin before start", actor: testAdmin(), startsAt: now.Add(time.Hour), wantComplete: true, wantReason: true},
		{name: "assigned driver after start", actor: auth.Actor{UserID: "driver-user", Role: auth.RoleDriver, DriverID: "driver-1"}, startsAt: testStart(), driverAllowed: true, wantComplete: true},
		{name: "driver before start", actor: auth.Actor{UserID: "driver-user", Role: auth.RoleDriver, DriverID: "driver-1"}, startsAt: now.Add(time.Hour), driverAllowed: true},
		{name: "driver without profile", actor: auth.Actor{UserID: "driver-user", Role: auth.RoleDriver}, startsAt: testStart(), driverAllowed: true},
		{name: "driver without assignment permission", actor: auth.Actor{UserID: "driver-user", Role: auth.RoleDriver, DriverID: "driver-1"}, startsAt: testStart()},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			current := assignedAppointment(LifecycleFixed, 3)
			current.StartsAt = test.startsAt
			current.EndsAt = test.startsAt.Add(3 * time.Hour)
			store := &fakeStore{current: current, driverCanComplete: test.driverAllowed}
			service, err := New(store, fakeAvailability{status: driver.StatusAvailable}, func() time.Time { return now })
			if err != nil {
				t.Fatal(err)
			}
			value, err := service.AppointmentDetail(t.Context(), test.actor, current.ID)
			if err != nil {
				t.Fatal(err)
			}
			if value.CanComplete != test.wantComplete || value.CompleteRequiresOverride != test.wantReason {
				t.Fatalf("completion permission/reason = %v/%v, want %v/%v", value.CanComplete, value.CompleteRequiresOverride, test.wantComplete, test.wantReason)
			}
		})
	}
}

func TestCompleteAppointmentEnforcesRoleTimeAssignmentAndReason(t *testing.T) {
	now := testStart().Add(time.Hour)
	current := assignedAppointment(LifecycleFixed, 3)
	tests := []struct {
		name          string
		actor         auth.Actor
		startsAt      time.Time
		driverAllowed bool
		reason        string
		wantErr       error
	}{
		{name: "assigned driver", actor: auth.Actor{UserID: "driver-user", Role: auth.RoleDriver, DriverID: "driver-1"}, startsAt: testStart(), driverAllowed: true},
		{name: "unassigned driver", actor: auth.Actor{UserID: "driver-user", Role: auth.RoleDriver, DriverID: "driver-2"}, startsAt: testStart(), wantErr: auth.ErrForbidden},
		{name: "driver before start", actor: auth.Actor{UserID: "driver-user", Role: auth.RoleDriver, DriverID: "driver-1"}, startsAt: now.Add(time.Hour), driverAllowed: true, wantErr: auth.ErrForbidden},
		{name: "admin before start needs reason", actor: testAdmin(), startsAt: now.Add(time.Hour), wantErr: ErrValidation},
		{name: "admin before start with reason", actor: testAdmin(), startsAt: now.Add(time.Hour), reason: "Vorzeitiger Abschluss nach Rückmeldung"},
		{name: "oversized reason", actor: testAdmin(), startsAt: now.Add(time.Hour), reason: strings.Repeat("x", 1001), wantErr: ErrValidation},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value := current
			value.StartsAt = test.startsAt
			value.EndsAt = test.startsAt.Add(3 * time.Hour)
			store := &fakeStore{current: value, driverCanComplete: test.driverAllowed}
			service, err := New(store, fakeAvailability{status: driver.StatusAvailable}, func() time.Time { return now })
			if err != nil {
				t.Fatal(err)
			}
			_, err = service.CompleteAppointment(t.Context(), test.actor, CompleteInput{MutateInput: MutateInput{ID: value.ID, ExpectedVersion: value.Version}, OverrideReason: test.reason})
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("CompleteAppointment() error = %v, want %v", err, test.wantErr)
			}
			if store.completeCalled != (test.wantErr == nil) {
				t.Fatalf("CompleteAppointment() persisted = %v, want %v", store.completeCalled, test.wantErr == nil)
			}
		})
	}
}

func TestReopenAppointmentRequiresAdminReasonAndCancelledVersion(t *testing.T) {
	tests := []struct {
		name    string
		actor   auth.Actor
		current Appointment
		input   ReopenInput
		wantErr error
	}{
		{
			name:    "driver forbidden",
			actor:   auth.Actor{UserID: "driver-user", Role: auth.RoleDriver},
			current: cancelledAppointment(),
			input:   ReopenInput{MutateInput: MutateInput{ID: "appointment-1", ExpectedVersion: 2}, Reason: "Kunde wünscht Neuplanung"},
			wantErr: auth.ErrForbidden,
		},
		{
			name:    "reason required",
			actor:   testAdmin(),
			current: cancelledAppointment(),
			input:   ReopenInput{MutateInput: MutateInput{ID: "appointment-1", ExpectedVersion: 2}},
			wantErr: ErrValidation,
		},
		{
			name:    "stale version",
			actor:   testAdmin(),
			current: cancelledAppointment(),
			input:   ReopenInput{MutateInput: MutateInput{ID: "appointment-1", ExpectedVersion: 1}, Reason: "Kunde wünscht Neuplanung"},
			wantErr: ErrConflict,
		},
		{
			name:    "only cancelled",
			actor:   testAdmin(),
			current: assignedAppointment(LifecycleProposal, 2),
			input:   ReopenInput{MutateInput: MutateInput{ID: "appointment-1", ExpectedVersion: 2}, Reason: "Kunde wünscht Neuplanung"},
			wantErr: ErrTransition,
		},
		{
			name:  "assignments required",
			actor: testAdmin(),
			current: Appointment{
				ID: "appointment-1", Lifecycle: LifecycleCancelled, StartsAt: testStart(), EndsAt: testStart().Add(3 * time.Hour), Version: 2,
			},
			input:   ReopenInput{MutateInput: MutateInput{ID: "appointment-1", ExpectedVersion: 2}, Reason: "Kunde wünscht Neuplanung"},
			wantErr: ErrValidation,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := &fakeStore{current: test.current, options: testOptions()}
			service, err := New(store, fakeAvailability{status: driver.StatusAvailable}, time.Now)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := service.ReopenAppointment(t.Context(), test.actor, test.input); !errors.Is(err, test.wantErr) {
				t.Fatalf("ReopenAppointment() error = %v, want %v", err, test.wantErr)
			}
			if store.reopenCalled {
				t.Fatal("ReopenAppointment() persisted an invalid transition")
			}
		})
	}
}

func TestReopenAppointmentRevalidatesAssignmentsAndAvailability(t *testing.T) {
	current := cancelledAppointment()
	store := &fakeStore{current: current, options: testOptions()}
	availability := &capturingAvailability{status: driver.StatusUnavailable}
	service, err := New(store, availability, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	input := ReopenInput{
		MutateInput: MutateInput{ID: current.ID, ExpectedVersion: current.Version},
		Reason:      "  Kunde hat den Auftrag erneut freigegeben  ",
	}
	if _, err := service.ReopenAppointment(t.Context(), testAdmin(), input); !errors.Is(err, ErrAvailability) {
		t.Fatalf("ReopenAppointment() unavailable error = %v, want %v", err, ErrAvailability)
	}
	if store.reopenCalled {
		t.Fatal("ReopenAppointment() persisted unavailable assignments without override")
	}

	input.OverrideReason = "  Fahrer hat die Ausnahme bestätigt  "
	if _, err := service.ReopenAppointment(t.Context(), testAdmin(), input); err != nil {
		t.Fatalf("ReopenAppointment() with override error = %v", err)
	}
	if !store.reopenCalled {
		t.Fatal("ReopenAppointment() did not persist valid reopen")
	}
	if store.lastReopen.Reason != "Kunde hat den Auftrag erneut freigegeben" ||
		store.lastReopen.OverrideReason != "Fahrer hat die Ausnahme bestätigt" {
		t.Fatalf("ReopenAppointment() normalized input = %#v", store.lastReopen)
	}
	if want := current.StartsAt.Add(-30 * time.Minute); !availability.from.Equal(want) {
		t.Fatalf("availability start = %s, want %s", availability.from, want)
	}
	if want := current.EndsAt.Add(45 * time.Minute); !availability.to.Equal(want) {
		t.Fatalf("availability end = %s, want %s", availability.to, want)
	}
}

func testService(t *testing.T, current Appointment, status driver.Status) *Service {
	t.Helper()
	service, err := New(&fakeStore{current: current, options: testOptions()}, fakeAvailability{status: status}, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func testAdmin() auth.Actor { return auth.Actor{UserID: "admin", Role: auth.RoleAdmin} }
func testStart() time.Time  { return time.Date(2026, 9, 1, 8, 0, 0, 0, time.UTC) }

func testAppointment() Appointment {
	return Appointment{
		ID: "appointment-1", JobID: "job-1", JobType: "chipping_only", TransportMode: "none",
		Lifecycle: LifecycleDraft, StartsAt: testStart(), EndsAt: testStart().Add(3 * time.Hour), Version: 1,
	}
}

func assignedAppointment(lifecycle Lifecycle, version int32) Appointment {
	value := testAppointment()
	value.Lifecycle = lifecycle
	value.Version = version
	value.Drivers = []DriverAssignment{{ID: "driver-1", Name: "Franz", Primary: true}}
	value.Resources = []AssignedResource{{
		ID: "chipper-1", Name: "Hackmaschine 1", Type: resource.TypeChipper, Purpose: PurposeChipping, Exclusive: true,
	}}
	return value
}

func cancelledAppointment() Appointment {
	value := assignedAppointment(LifecycleCancelled, 2)
	value.BufferBeforeMinutes = 30
	value.BufferAfterMinutes = 45
	return value
}

func testOptions() PlanningOptions {
	return PlanningOptions{
		Drivers: []PlanningDriver{{ID: "driver-1", Name: "Franz"}},
		Resources: []PlanningResource{
			{ID: "chipper-1", Name: "Hackmaschine 1", Type: resource.TypeChipper, IsExclusive: true},
			{ID: "transport-1", Name: "Transporter 1", Type: resource.TypeTransportVehicle, IsExclusive: true},
		},
	}
}

type fakeAvailability struct{ status driver.Status }

func (fake fakeAvailability) IsAvailable(context.Context, auth.Actor, string, time.Time, time.Time) (driver.Status, []string, error) {
	return fake.status, nil, nil
}

type capturingAvailability struct {
	status   driver.Status
	from, to time.Time
}

func (fake *capturingAvailability) IsAvailable(_ context.Context, _ auth.Actor, _ string, from, to time.Time) (driver.Status, []string, error) {
	fake.from, fake.to = from, to
	return fake.status, nil, nil
}

type fakeStore struct {
	current           Appointment
	currents          map[string]Appointment
	options           PlanningOptions
	conflictUntil     time.Time
	lastSwap          SwapInput
	assignCalled      bool
	reopenCalled      bool
	lastReopen        ReopenInput
	driverCanComplete bool
	completeCalled    bool
}

func (fake *fakeStore) CreateDraft(context.Context, auth.Actor, CreateDraftInput) (Appointment, error) {
	return fake.current, nil
}
func (fake *fakeStore) Get(_ context.Context, id string) (Appointment, error) {
	if fake.currents != nil {
		return fake.currents[id], nil
	}
	return fake.current, nil
}
func (fake *fakeStore) Assign(_ context.Context, _ auth.Actor, _ AssignInput) (Appointment, error) {
	fake.assignCalled = true
	fake.current.Version++
	return fake.current, nil
}
func (fake *fakeStore) Propose(context.Context, auth.Actor, MutateInput, string) (Appointment, error) {
	return fake.current, nil
}
func (fake *fakeStore) Reschedule(context.Context, auth.Actor, MoveInput, string) (Appointment, error) {
	return fake.current, nil
}
func (fake *fakeStore) Fix(context.Context, auth.Actor, FixInput) (Appointment, error) {
	return fake.current, nil
}
func (fake *fakeStore) Cancel(context.Context, auth.Actor, CancelInput) (Appointment, error) {
	return fake.current, nil
}
func (fake *fakeStore) Reopen(_ context.Context, _ auth.Actor, input ReopenInput) (Appointment, error) {
	fake.reopenCalled = true
	fake.lastReopen = input
	fake.current.Lifecycle = LifecycleProposal
	fake.current.Version++
	return fake.current, nil
}

func (fake *fakeStore) Complete(context.Context, auth.Actor, CompleteInput) (Appointment, error) {
	fake.completeCalled = true
	return fake.current, nil
}
func (fake *fakeStore) Detail(context.Context, string) (Detail, error) {
	return Detail{CalendarEvent: CalendarEvent{Appointment: fake.current}}, nil
}
func (fake *fakeStore) ListCalendar(context.Context, time.Time, time.Time) ([]CalendarEvent, error) {
	return nil, nil
}
func (fake *fakeStore) PlanningOptions(context.Context) (PlanningOptions, error) {
	return fake.options, nil
}

func (fake *fakeStore) ListConflicts(_ context.Context, from time.Time, _ time.Time, _ []string, _ []string, _ string) ([]Conflict, error) {
	if !fake.conflictUntil.IsZero() && from.Before(fake.conflictUntil) {
		return []Conflict{{AppointmentID: "busy", JobNumber: "HA-BUSY", CustomerName: "Musterkunde", SubjectName: "Franz"}}, nil
	}
	return nil, nil
}
func (fake *fakeStore) DriverCanComplete(context.Context, string, string) (bool, error) {
	return fake.driverCanComplete, nil
}
func (fake *fakeStore) Swap(_ context.Context, _ auth.Actor, input SwapInput) ([]Appointment, error) {
	fake.lastSwap = input
	return []Appointment{fake.current}, nil
}

func TestConflictAlternativesIncludeAffectedAndThreeSlots(t *testing.T) {
	t.Parallel()
	start := time.Date(2026, 8, 27, 8, 0, 0, 0, time.UTC)
	current := assignedAppointment(LifecycleProposal, 3)
	store := &fakeStore{current: current, conflictUntil: start.Add(45 * time.Minute)}
	service, err := New(store, fakeAvailability{status: driver.StatusAvailable}, func() time.Time { return start })
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.ConflictAlternatives(t.Context(), auth.Actor{UserID: "admin", Role: auth.RoleAdmin}, current.ID, start, start.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Conflicts) != 1 || result.Conflicts[0].CustomerName != "Musterkunde" || len(result.Alternatives) != 3 {
		t.Fatalf("resolution = %#v", result)
	}
}

func TestSwapAppointmentsIsAdminOnlyAndVersioned(t *testing.T) {
	t.Parallel()
	first := assignedAppointment(LifecycleProposal, 2)
	first.ID = "first"
	second := assignedAppointment(LifecycleDraft, 4)
	second.ID = "second"
	second.StartsAt = first.StartsAt.Add(3 * time.Hour)
	second.EndsAt = second.StartsAt.Add(time.Hour)
	store := &fakeStore{current: first, currents: map[string]Appointment{"first": first, "second": second}}
	service, _ := New(store, fakeAvailability{status: driver.StatusAvailable}, time.Now)
	input := SwapInput{FirstID: "first", SecondID: "second", FirstVersion: 2, SecondVersion: 4}
	if _, err := service.SwapAppointments(t.Context(), auth.Actor{Role: auth.RoleDriver}, input); !errors.Is(err, auth.ErrForbidden) {
		t.Fatalf("driver swap error=%v", err)
	}
	if _, err := service.SwapAppointments(t.Context(), auth.Actor{UserID: "admin", Role: auth.RoleAdmin}, input); err != nil {
		t.Fatal(err)
	}
	if store.lastSwap.SecondID != "second" {
		t.Fatalf("swap not persisted: %#v", store.lastSwap)
	}
}
