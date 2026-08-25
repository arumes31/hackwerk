package customers

import (
	"context"
	"errors"
	"testing"

	"example.invalid/hackplan/internal/auth"
)

type storeStub struct {
	createCalls   int
	priorityCalls int
	created       CreatedIntake
	duplicates    []Duplicate
}

func (store *storeStub) FindDuplicates(context.Context, CustomerInput) ([]Duplicate, error) {
	return store.duplicates, nil
}
func (store *storeStub) CreateIntake(context.Context, auth.Actor, IntakeInput, string) (CreatedIntake, error) {
	store.createCalls++
	return store.created, nil
}
func (store *storeStub) CreateJob(context.Context, auth.Actor, CreateJobInput) (CreatedIntake, error) {
	return store.created, nil
}
func (store *storeStub) UpdateJob(context.Context, auth.Actor, UpdateJobInput) error { return nil }
func (store *storeStub) ArchiveJob(context.Context, auth.Actor, string, int32, string) error {
	return nil
}
func (store *storeStub) ListCustomers(context.Context, string, int, int) (Page[CustomerSummary], error) {
	return Page[CustomerSummary]{}, nil
}
func (store *storeStub) CustomerDetail(context.Context, string) (CustomerDetail, error) {
	return CustomerDetail{}, nil
}
func (store *storeStub) UpdateCustomer(context.Context, auth.Actor, UpdateCustomerInput) error {
	return nil
}
func (store *storeStub) ArchiveCustomer(context.Context, auth.Actor, string, int32, string) error {
	return nil
}
func (store *storeStub) ListWaitlist(context.Context, WaitlistFilter) (Page[WaitlistItem], error) {
	return Page[WaitlistItem]{}, nil
}
func (store *storeStub) UpdateWaitlistPriority(context.Context, auth.Actor, string, int32, int32, string) error {
	store.priorityCalls++
	return nil
}
func (store *storeStub) RemoveWaitlist(context.Context, auth.Actor, string, int32, string, string) error {
	return nil
}
func (store *storeStub) AddNote(context.Context, auth.Actor, string, string, string, string) (string, error) {
	return "note-1", nil
}

func TestCreateIntakeValidatesAndPreservesDuplicateWarning(t *testing.T) {
	t.Parallel()
	store := &storeStub{
		created:    CreatedIntake{CustomerID: "customer-1", JobID: "job-1", WaitlistID: "waitlist-1", JobNumber: "HA-2026-0001"},
		duplicates: []Duplicate{{ID: "existing"}},
	}
	service, err := NewService(store)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	actor := auth.Actor{UserID: "driver-1", Role: auth.RoleDriver}
	created, err := service.CreateIntake(context.Background(), actor, validIntake(), "request-1")
	if err != nil {
		t.Fatalf("CreateIntake() error = %v", err)
	}
	if store.createCalls != 1 || created.JobNumber != "HA-2026-0001" || len(created.Duplicates) != 1 {
		t.Fatalf("created = %#v, calls = %d", created, store.createCalls)
	}
}

func TestCreateIntakeRejectsInvalidTransportBeforeStore(t *testing.T) {
	t.Parallel()
	store := &storeStub{}
	service, _ := NewService(store)
	input := validIntake()
	input.Job.TransportMode = TransportInternal
	_, err := service.CreateIntake(context.Background(), auth.Actor{UserID: "driver-1", Role: auth.RoleDriver}, input, "")
	if !errors.Is(err, ErrValidation) {
		t.Fatalf("error = %v, want ErrValidation", err)
	}
	if store.createCalls != 0 {
		t.Fatalf("store create calls = %d, want 0", store.createCalls)
	}
}

func TestDriverCannotPrioritizeWaitlist(t *testing.T) {
	t.Parallel()
	store := &storeStub{}
	service, _ := NewService(store)
	err := service.UpdateWaitlistPriority(context.Background(), auth.Actor{UserID: "driver-1", Role: auth.RoleDriver}, "entry", 10, 1, "")
	if !errors.Is(err, auth.ErrForbidden) {
		t.Fatalf("error = %v, want forbidden", err)
	}
	if store.priorityCalls != 0 {
		t.Fatalf("priority calls = %d, want 0", store.priorityCalls)
	}
}

func validIntake() IntakeInput {
	return IntakeInput{
		Customer: CustomerInput{FirstName: "Franz", LastName: "Huber", CountryCode: "AT", NotificationPreference: NotifyNone},
		Job: JobInput{
			JobType: JobTypeChippingOnly, VolumeM3: "80", EstimatedHackMinutes: 180,
			TransportMode: TransportNone, Urgency: UrgencyNormal, Source: SourcePhone,
		},
	}
}
