package appointment

import (
	"context"
	"errors"
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

type fakeStore struct {
	current      Appointment
	options      PlanningOptions
	assignCalled bool
}

func (fake *fakeStore) CreateDraft(context.Context, auth.Actor, CreateDraftInput) (Appointment, error) {
	return fake.current, nil
}
func (fake *fakeStore) Get(context.Context, string) (Appointment, error) { return fake.current, nil }
func (fake *fakeStore) Assign(_ context.Context, _ auth.Actor, input AssignInput) (Appointment, error) {
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
func (fake *fakeStore) Fix(context.Context, auth.Actor, MutateInput) (Appointment, error) {
	return fake.current, nil
}
func (fake *fakeStore) Cancel(context.Context, auth.Actor, CancelInput) (Appointment, error) {
	return fake.current, nil
}
func (fake *fakeStore) Complete(context.Context, auth.Actor, CompleteInput) (Appointment, error) {
	return fake.current, nil
}
func (fake *fakeStore) ListCalendar(context.Context, time.Time, time.Time) ([]CalendarEvent, error) {
	return nil, nil
}
func (fake *fakeStore) PlanningOptions(context.Context) (PlanningOptions, error) {
	return fake.options, nil
}
func (fake *fakeStore) ListConflicts(context.Context, time.Time, time.Time, []string, []string, string) ([]Conflict, error) {
	return nil, nil
}
func (fake *fakeStore) DriverCanComplete(context.Context, string, string) (bool, error) {
	return true, nil
}
