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
			wantErr: ErrVersionConflict,
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

func TestPlanFromWaitlistBuildsCompleteProposal(t *testing.T) {
	current := testAppointment()
	options := testOptions()
	options.Waitlist = []WaitlistItem{{
		JobID: "job-1", JobNumber: "HA-100", JobType: "chipping_only",
		EstimatedHackMinutes: 90,
	}}
	store := &fakeStore{current: current, options: options}
	service, err := New(store, fakeAvailability{status: driver.StatusAvailable}, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	input := PlanInput{
		CreateDraftInput: CreateDraftInput{
			JobID: " job-1 ",
			Time:  TimeInput{StartsAt: testStart(), EndsAt: testStart().Add(2 * time.Hour)},
		},
		Assignments: AssignmentInput{
			DriverIDs:       []string{" driver-1 "},
			PrimaryDriverID: " driver-1 ",
			Resources:       []ResourceAssignment{{ID: " chipper-1 ", Purpose: PurposeChipping}},
		},
	}
	if _, err := service.PlanFromWaitlist(t.Context(), testAdmin(), input); err != nil {
		t.Fatalf("PlanFromWaitlist() error = %v", err)
	}
	if !store.planCalled {
		t.Fatal("PlanFromWaitlist() did not persist a validated plan")
	}
	if store.lastPlan.JobID != "job-1" || store.lastPlan.Assignments.PrimaryDriverID != "driver-1" ||
		store.lastPlan.Assignments.Resources[0].ID != "chipper-1" {
		t.Fatalf("PlanFromWaitlist() did not normalize plan input: %#v", store.lastPlan)
	}
}

func TestPlanFromWaitlistRejectsMissingWaitlistItemAndShortDuration(t *testing.T) {
	options := testOptions()
	options.Waitlist = []WaitlistItem{{JobID: "other-job", EstimatedHackMinutes: 30}}
	store := &fakeStore{current: testAppointment(), options: options}
	service, err := New(store, fakeAvailability{status: driver.StatusAvailable}, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	validAssignments := AssignmentInput{
		DriverIDs: []string{"driver-1"}, PrimaryDriverID: "driver-1",
		Resources: []ResourceAssignment{{ID: "chipper-1", Purpose: PurposeChipping}},
	}
	_, err = service.PlanFromWaitlist(t.Context(), testAdmin(), PlanInput{
		CreateDraftInput: CreateDraftInput{JobID: "job-1", Time: TimeInput{StartsAt: testStart(), EndsAt: testStart().Add(time.Hour)}},
		Assignments:      validAssignments,
	})
	if !errors.Is(err, ErrNotFound) || store.planCalled {
		t.Fatalf("missing waitlist item error/persisted = %v/%v, want not found/no persistence", err, store.planCalled)
	}

	options.Waitlist = []WaitlistItem{{JobID: "job-1", EstimatedHackMinutes: 121}}
	store = &fakeStore{current: testAppointment(), options: options}
	service, err = New(store, fakeAvailability{status: driver.StatusAvailable}, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.PlanFromWaitlist(t.Context(), testAdmin(), PlanInput{
		CreateDraftInput: CreateDraftInput{JobID: "job-1", Time: TimeInput{StartsAt: testStart(), EndsAt: testStart().Add(2 * time.Hour)}},
		Assignments:      validAssignments,
	})
	if !errors.Is(err, ErrValidation) || store.planCalled {
		t.Fatalf("short duration error/persisted = %v/%v, want validation/no persistence", err, store.planCalled)
	}
}

func TestAppointmentMutationsPersistValidTransitions(t *testing.T) {
	assignments := AssignmentInput{
		DriverIDs: []string{"driver-1"}, PrimaryDriverID: "driver-1",
		Resources: []ResourceAssignment{{ID: "chipper-1", Purpose: PurposeChipping}},
	}
	tests := []struct {
		name string
		run  func(*Service, *fakeStore) error
	}{
		{
			name: "create draft",
			run: func(service *Service, store *fakeStore) error {
				_, err := service.CreateDraftFromWaitlist(t.Context(), testAdmin(), CreateDraftInput{
					JobID: " job-1 ", Time: TimeInput{StartsAt: testStart(), EndsAt: testStart().Add(time.Hour)},
				})
				if err == nil && (!store.createCalled || store.lastCreate.JobID != "job-1") {
					t.Fatal("CreateDraftFromWaitlist() did not persist normalized input")
				}
				return err
			},
		},
		{
			name: "assign drivers and resources",
			run: func(service *Service, store *fakeStore) error {
				_, err := service.AssignDriversAndResources(t.Context(), testAdmin(), AssignInput{
					MutateInput: MutateInput{ID: store.current.ID, ExpectedVersion: store.current.Version}, Assignments: assignments,
				})
				if err == nil && !store.assignCalled {
					t.Fatal("AssignDriversAndResources() did not persist assignment")
				}
				return err
			},
		},
		{
			name: "propose draft",
			run: func(service *Service, store *fakeStore) error {
				_, err := service.ProposeAppointment(t.Context(), testAdmin(), MutateInput{ID: store.current.ID, ExpectedVersion: store.current.Version}, "  approved  ")
				if err == nil && !store.proposeCalled {
					t.Fatal("ProposeAppointment() did not persist proposal")
				}
				return err
			},
		},
		{
			name: "move appointment",
			run: func(service *Service, store *fakeStore) error {
				_, err := service.MoveAppointment(t.Context(), testAdmin(), MoveInput{
					MutateInput: MutateInput{ID: store.current.ID, ExpectedVersion: store.current.Version},
					StartsAt:    testStart().Add(time.Hour), EndsAt: testStart().Add(4 * time.Hour),
					WithoutNotificationReason: "  customer requested it  ",
				})
				if err == nil && (!store.rescheduleCalled || store.lastReschedule.WithoutNotificationReason != "customer requested it") {
					t.Fatal("MoveAppointment() did not persist normalized change")
				}
				return err
			},
		},
		{
			name: "resize appointment",
			run: func(service *Service, store *fakeStore) error {
				_, err := service.ResizeAppointment(t.Context(), testAdmin(), ResizeInput{
					MutateInput: MutateInput{ID: store.current.ID, ExpectedVersion: store.current.Version},
					StartsAt:    store.current.StartsAt, EndsAt: store.current.EndsAt.Add(time.Hour),
				})
				if err == nil && !store.rescheduleCalled {
					t.Fatal("ResizeAppointment() did not persist change")
				}
				return err
			},
		},
		{
			name: "fix proposal",
			run: func(service *Service, store *fakeStore) error {
				_, err := service.FixAppointment(t.Context(), testAdmin(), FixInput{MutateInput: MutateInput{ID: store.current.ID, ExpectedVersion: store.current.Version}})
				if err == nil && !store.fixCalled {
					t.Fatal("FixAppointment() did not persist transition")
				}
				return err
			},
		},
		{
			name: "cancel fixed with reason",
			run: func(service *Service, store *fakeStore) error {
				_, err := service.CancelAppointment(t.Context(), testAdmin(), CancelInput{
					MutateInput: MutateInput{ID: store.current.ID, ExpectedVersion: store.current.Version}, Reason: "  customer cancelled  ",
				})
				if err == nil && (!store.cancelCalled || store.lastCancel.Reason != "customer cancelled") {
					t.Fatal("CancelAppointment() did not persist normalized reason")
				}
				return err
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			current := assignedAppointment(LifecycleDraft, 3)
			if test.name == "fix proposal" {
				current.Lifecycle = LifecycleProposal
			}
			if test.name == "cancel fixed with reason" {
				current.Lifecycle = LifecycleFixed
			}
			store := &fakeStore{current: current, options: testOptions()}
			service, err := New(store, fakeAvailability{status: driver.StatusAvailable}, time.Now)
			if err != nil {
				t.Fatal(err)
			}
			if err := test.run(service, store); err != nil {
				t.Fatalf("valid mutation error = %v", err)
			}
		})
	}
}

func TestAppointmentQueryOperationsAcceptAuthorizedValidRequests(t *testing.T) {
	current := assignedAppointment(LifecycleFixed, 3)
	store := &fakeStore{current: current, options: testOptions()}
	service, err := New(store, fakeAvailability{status: driver.StatusAvailable}, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	from := testStart()
	to := from.Add(24 * time.Hour)
	if _, err := service.ListCalendarRange(t.Context(), testAdmin(), from, to); err != nil {
		t.Fatalf("ListCalendarRange() error = %v", err)
	}
	if _, err := service.PlanningOptions(t.Context(), testAdmin()); err != nil {
		t.Fatalf("PlanningOptions() error = %v", err)
	}
	if _, err := service.ListConflictsAndCapacity(t.Context(), testAdmin(), from, to, []string{"driver-1"}, []string{"chipper-1"}, current.ID); err != nil {
		t.Fatalf("ListConflictsAndCapacity() error = %v", err)
	}
}

func TestServiceConstructionAndMutationGuardsRejectUnsafeRequests(t *testing.T) {
	if _, err := New(nil, fakeAvailability{status: driver.StatusAvailable}, nil); err == nil {
		t.Fatal("New() accepted a nil store")
	}
	if _, err := New(&fakeStore{}, nil, nil); err == nil {
		t.Fatal("New() accepted a nil availability service")
	}

	current := assignedAppointment(LifecycleDraft, 3)
	store := &fakeStore{current: current, options: testOptions()}
	service, err := New(store, fakeAvailability{status: driver.StatusAvailable}, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.ProposeAppointment(t.Context(), testAdmin(), MutateInput{ID: current.ID, ExpectedVersion: current.Version}, strings.Repeat("x", 1001)); !errors.Is(err, ErrValidation) {
		t.Fatalf("ProposeAppointment() oversized override error = %v, want validation", err)
	}
	if _, err := service.MoveAppointment(t.Context(), testAdmin(), MoveInput{
		MutateInput: MutateInput{ID: current.ID, ExpectedVersion: current.Version},
		StartsAt:    current.StartsAt, EndsAt: current.EndsAt,
		WithoutNotificationReason: strings.Repeat("x", 1001),
	}); !errors.Is(err, ErrValidation) {
		t.Fatalf("MoveAppointment() oversized notification reason error = %v, want validation", err)
	}
	if _, err := service.CancelAppointment(t.Context(), testAdmin(), CancelInput{
		MutateInput: MutateInput{ID: current.ID, ExpectedVersion: current.Version}, Reason: strings.Repeat("x", 1001),
	}); !errors.Is(err, ErrValidation) {
		t.Fatalf("CancelAppointment() oversized reason error = %v, want validation", err)
	}
}

func TestConflictAndSwapValidationStopsBeforePersistence(t *testing.T) {
	current := assignedAppointment(LifecycleProposal, 3)
	store := &fakeStore{current: current, currents: map[string]Appointment{current.ID: current}}
	service, err := New(store, fakeAvailability{status: driver.StatusAvailable}, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	start := testStart()
	if _, err := service.ConflictAlternatives(t.Context(), testAdmin(), " ", start, start.Add(time.Hour)); !errors.Is(err, ErrValidation) {
		t.Fatalf("ConflictAlternatives() blank appointment error = %v, want validation", err)
	}
	if _, err := service.ConflictAlternatives(t.Context(), testAdmin(), current.ID, start.In(time.FixedZone("local", 3600)), start.Add(time.Hour).In(time.FixedZone("local", 3600))); !errors.Is(err, ErrValidation) {
		t.Fatalf("ConflictAlternatives() non-UTC error = %v, want validation", err)
	}
	if _, err := service.SwapAppointments(t.Context(), testAdmin(), SwapInput{FirstID: current.ID, SecondID: current.ID, FirstVersion: 3, SecondVersion: 3}); !errors.Is(err, ErrValidation) {
		t.Fatalf("SwapAppointments() same appointment error = %v, want validation", err)
	}
	if _, err := service.SwapAppointments(t.Context(), testAdmin(), SwapInput{FirstID: current.ID, SecondID: "missing", FirstVersion: 2, SecondVersion: 3}); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("SwapAppointments() stale version error = %v, want version conflict", err)
	}
	if store.lastSwap.FirstID != "" {
		t.Fatalf("SwapAppointments() persisted invalid request: %#v", store.lastSwap)
	}
}

func TestSwapCandidatesAreAdminOnlyAndFilteredByLifecycle(t *testing.T) {
	from := testStart()
	store := &fakeStore{events: []CalendarEvent{
		{Appointment: Appointment{ID: "current", Lifecycle: LifecycleDraft}},
		{Appointment: Appointment{ID: "draft", Lifecycle: LifecycleDraft}},
		{Appointment: Appointment{ID: "proposal", Lifecycle: LifecycleProposal}},
		{Appointment: Appointment{ID: "fixed", Lifecycle: LifecycleFixed}},
	}}
	service, err := New(store, fakeAvailability{status: driver.StatusAvailable}, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	candidates, err := service.SwapCandidates(t.Context(), testAdmin(), "current", from, from.Add(24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 2 || candidates[0].ID != "draft" || candidates[1].ID != "proposal" {
		t.Fatalf("SwapCandidates() = %#v", candidates)
	}
	candidates, err = service.SwapCandidates(t.Context(), testAdmin(), "  current\t", from, from.Add(24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 2 || candidates[0].ID != "draft" || candidates[1].ID != "proposal" {
		t.Fatalf("SwapCandidates() with padded exclusion = %#v", candidates)
	}
	if _, err := service.SwapCandidates(t.Context(), auth.Actor{Role: auth.RoleDriver}, "current", from, from.Add(24*time.Hour)); !errors.Is(err, auth.ErrForbidden) {
		t.Fatalf("driver SwapCandidates() error = %v, want forbidden", err)
	}
}

func TestAppointmentTransitionsRejectStaleOrInvalidStates(t *testing.T) {
	tests := []struct {
		name    string
		current Appointment
		run     func(*Service, Appointment) error
		wantErr error
	}{
		{
			name:    "proposal requires draft",
			current: assignedAppointment(LifecycleProposal, 3),
			run: func(service *Service, current Appointment) error {
				_, err := service.ProposeAppointment(t.Context(), testAdmin(), MutateInput{ID: current.ID, ExpectedVersion: current.Version}, "")
				return err
			},
			wantErr: ErrTransition,
		},
		{
			name:    "fix requires proposal",
			current: assignedAppointment(LifecycleDraft, 3),
			run: func(service *Service, current Appointment) error {
				_, err := service.FixAppointment(t.Context(), testAdmin(), FixInput{MutateInput: MutateInput{ID: current.ID, ExpectedVersion: current.Version}})
				return err
			},
			wantErr: ErrTransition,
		},
		{
			name:    "cancel cannot repeat",
			current: assignedAppointment(LifecycleCancelled, 3),
			run: func(service *Service, current Appointment) error {
				_, err := service.CancelAppointment(t.Context(), testAdmin(), CancelInput{MutateInput: MutateInput{ID: current.ID, ExpectedVersion: current.Version}, Reason: "duplicate"})
				return err
			},
			wantErr: ErrTransition,
		},
		{
			name:    "reschedule needs current version",
			current: assignedAppointment(LifecycleDraft, 3),
			run: func(service *Service, current Appointment) error {
				_, err := service.MoveAppointment(t.Context(), testAdmin(), MoveInput{
					MutateInput: MutateInput{ID: current.ID, ExpectedVersion: current.Version - 1},
					StartsAt:    current.StartsAt, EndsAt: current.EndsAt,
				})
				return err
			},
			wantErr: ErrVersionConflict,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := &fakeStore{current: test.current, options: testOptions()}
			service, err := New(store, fakeAvailability{status: driver.StatusAvailable}, time.Now)
			if err != nil {
				t.Fatal(err)
			}
			if err := test.run(service, test.current); !errors.Is(err, test.wantErr) {
				t.Fatalf("transition error = %v, want %v", err, test.wantErr)
			}
			if store.proposeCalled || store.fixCalled || store.cancelCalled || store.rescheduleCalled {
				t.Fatal("invalid transition reached persistence")
			}
		})
	}
}

func TestAppointmentQueryOperationsEnforcePermissionAndRange(t *testing.T) {
	service := testService(t, assignedAppointment(LifecycleFixed, 3), driver.StatusAvailable)
	from := testStart()
	to := from.Add(time.Hour)
	if _, err := service.PlanningOptions(t.Context(), auth.Actor{UserID: "driver-user", Role: auth.RoleDriver}); !errors.Is(err, auth.ErrForbidden) {
		t.Fatalf("PlanningOptions() driver error = %v, want forbidden", err)
	}
	if _, err := service.ListConflictsAndCapacity(t.Context(), testAdmin(), from, from, nil, nil, ""); !errors.Is(err, ErrValidation) {
		t.Fatalf("ListConflictsAndCapacity() empty range error = %v, want validation", err)
	}
	if _, err := service.ListConflictsAndCapacity(t.Context(), auth.Actor{Role: auth.RoleDriver}, from, to, nil, nil, ""); !errors.Is(err, auth.ErrForbidden) {
		t.Fatalf("ListConflictsAndCapacity() unauthenticated error = %v, want forbidden", err)
	}
	if _, err := service.ListConflictsAndCapacity(t.Context(), auth.Actor{UserID: "driver-user", Role: auth.RoleDriver}, from, to, nil, nil, ""); err != nil {
		t.Fatalf("ListConflictsAndCapacity() authenticated driver error = %v", err)
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
	events            []CalendarEvent
	options           PlanningOptions
	conflictUntil     time.Time
	lastSwap          SwapInput
	lastPlan          PlanInput
	lastCreate        CreateDraftInput
	lastPropose       MutateInput
	lastReschedule    MoveInput
	lastFix           FixInput
	lastCancel        CancelInput
	planCalled        bool
	createCalled      bool
	proposeCalled     bool
	rescheduleCalled  bool
	fixCalled         bool
	cancelCalled      bool
	assignCalled      bool
	reopenCalled      bool
	lastReopen        ReopenInput
	driverCanComplete bool
	completeCalled    bool
}

func (fake *fakeStore) Plan(_ context.Context, _ auth.Actor, input PlanInput, _ string) (Appointment, error) {
	fake.planCalled = true
	fake.lastPlan = input
	return fake.current, nil
}

func (fake *fakeStore) CreateDraft(_ context.Context, _ auth.Actor, input CreateDraftInput) (Appointment, error) {
	fake.createCalled = true
	fake.lastCreate = input
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
func (fake *fakeStore) Propose(_ context.Context, _ auth.Actor, input MutateInput, _ string) (Appointment, error) {
	fake.proposeCalled = true
	fake.lastPropose = input
	return fake.current, nil
}
func (fake *fakeStore) Reschedule(_ context.Context, _ auth.Actor, input MoveInput, _ string) (Appointment, error) {
	fake.rescheduleCalled = true
	fake.lastReschedule = input
	return fake.current, nil
}
func (fake *fakeStore) Fix(_ context.Context, _ auth.Actor, input FixInput) (Appointment, error) {
	fake.fixCalled = true
	fake.lastFix = input
	return fake.current, nil
}
func (fake *fakeStore) Cancel(_ context.Context, _ auth.Actor, input CancelInput) (Appointment, error) {
	fake.cancelCalled = true
	fake.lastCancel = input
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
	return fake.events, nil
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

func TestPreviewMutationIsAdminOnlyAndReportsAuthoritativeChecks(t *testing.T) {
	t.Parallel()
	current := assignedAppointment(LifecycleProposal, 3)
	current.JobNumber = "HA-2026-0001"
	current.EstimatedHackMinutes = 120
	current.EstimatedTransportMinutes = 30
	current.BufferBeforeMinutes = 15
	current.BufferAfterMinutes = 20
	store := &fakeStore{current: current, conflictUntil: current.StartsAt.Add(time.Hour)}
	service, err := New(store, fakeAvailability{status: driver.StatusAvailable}, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	input := PreflightInput{AppointmentID: current.ID, Action: "fix", ExpectedVersion: 2}
	if _, err := service.PreviewMutation(t.Context(), auth.Actor{UserID: "driver", Role: auth.RoleDriver}, input); !errors.Is(err, auth.ErrForbidden) {
		t.Fatalf("driver PreviewMutation() error = %v", err)
	}
	preview, err := service.PreviewMutation(t.Context(), testAdmin(), input)
	if err != nil {
		t.Fatal(err)
	}
	checks := make(map[string]bool, len(preview.Checks))
	for _, check := range preview.Checks {
		checks[check.Key] = check.Passed
	}
	if checks["version"] || !checks["job"] || !checks["time"] || !checks["driver"] || !checks["chipper"] || !checks["transport"] || !checks["availability"] || checks["conflicts"] {
		t.Fatalf("preflight checks = %#v", preview.Checks)
	}
	if preview.WorkingMinutes != 120 || preview.TransportMinutes != 30 || preview.BufferBeforeMinutes != 15 || preview.BufferAfterMinutes != 20 || len(preview.Conflicts) != 1 {
		t.Fatalf("preflight timing/conflicts = %#v", preview)
	}
}

func TestPreviewMutationUsesAuthoritativeTransportValidation(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		mode      string
		confirmed bool
		resources []AssignedResource
		want      bool
	}{
		{name: "missing transport plan", mode: "none", want: false},
		{name: "unconfirmed external transport", mode: "external", want: false},
		{name: "confirmed external transport", mode: "external", confirmed: true, want: true},
		{name: "internal transport without vehicle", mode: "internal", want: false},
		{name: "internal transport with vehicle", mode: "internal", resources: []AssignedResource{{ID: "transport-1", Type: resource.TypeTransportVehicle, Purpose: PurposeTransport}}, want: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			current := assignedAppointment(LifecycleProposal, 3)
			current.JobType = "chipping_with_transport"
			current.TransportMode = test.mode
			current.ExternalTransportConfirmed = test.confirmed
			current.Resources = append(current.Resources, test.resources...)
			store := &fakeStore{current: current}
			service, err := New(store, fakeAvailability{status: driver.StatusAvailable}, time.Now)
			if err != nil {
				t.Fatal(err)
			}
			preview, err := service.PreviewMutation(t.Context(), testAdmin(), PreflightInput{AppointmentID: current.ID, Action: "fix", ExpectedVersion: current.Version})
			if err != nil {
				t.Fatal(err)
			}
			for _, check := range preview.Checks {
				if check.Key == "transport" {
					if check.Passed != test.want {
						t.Fatalf("transport check = %v, want %v", check.Passed, test.want)
					}
					return
				}
			}
			t.Fatal("transport check missing")
		})
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
