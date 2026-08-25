package customers

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"example.invalid/hackplan/internal/auth"
)

var (
	ErrNotFound = errors.New("customers: not found")
	ErrConflict = errors.New("customers: version conflict")
)

type CustomerSummary struct {
	ID, FirstName, LastName, CompanyName, Locality, Region, PhoneRaw, Email string
	Version                                                                 int32
	JobCount                                                                int32
}

type Customer struct {
	ID, FirstName, LastName, CompanyName                               string
	Street, PostalCode, Locality, Region, CountryCode, AddressFreeform string
	PhoneRaw, PhoneNormalized, Email, GeocodingStatus                  string
	NotificationPreference                                             NotificationPreference
	Latitude, Longitude                                                *float64
	ArchivedAt                                                         *time.Time
	Version                                                            int32
	CreatedAt, UpdatedAt                                               time.Time
}

type Job struct {
	ID, JobNumber, VolumeM3, PreferredStartDate, PreferredEndDate       string
	PreferenceText, Region, WorkflowStatus                              string
	JobType                                                             JobType
	TransportMode                                                       TransportMode
	Urgency                                                             Urgency
	Source                                                              Source
	EstimatedHackMinutes, EstimatedTransportMinutes, TransportTripCount int32
	ExternalTransportConfirmed                                          bool
	ReceivedAt                                                          time.Time
	ArchivedAt                                                          *time.Time
	Version                                                             int32
}

type Note struct {
	ID, JobID, AuthorUserID, AuthorName, Body, CorrectionOfID string
	CreatedAt                                                 time.Time
}

type CustomerDetail struct {
	Customer Customer
	Jobs     []Job
	Notes    map[string][]Note
	MapsURL  string
}

type Duplicate struct{ ID, FirstName, LastName, CompanyName, Locality string }

type WaitlistItem struct {
	WaitlistID, JobID, JobNumber, VolumeM3, PreferredStartDate, PreferredEndDate   string
	PreferenceText, Region, CustomerID, FirstName, LastName, CompanyName, Locality string
	NoteExcerpt                                                                    string
	JobType                                                                        JobType
	TransportMode                                                                  TransportMode
	Urgency                                                                        Urgency
	EnteredAt                                                                      time.Time
	ManualPriority, WaitlistVersion, EstimatedHackMinutes, AgeDays                 int32
}

type Page[T any] struct {
	Items      []T
	Page       int
	PageSize   int
	Total      int64
	TotalPages int
}

type CreatedIntake struct {
	CustomerID, JobID, WaitlistID, JobNumber string
	Duplicates                               []Duplicate
}

type UpdateCustomerInput struct {
	ID, RequestID   string
	ExpectedVersion int32
	Customer        CustomerInput
}

type CreateJobInput struct {
	CustomerID, InitialNote, RequestID string
	Job                                JobInput
}

type UpdateJobInput struct {
	ID, RequestID   string
	ExpectedVersion int32
	Job             JobInput
}

type Store interface {
	FindDuplicates(context.Context, CustomerInput) ([]Duplicate, error)
	CreateIntake(context.Context, auth.Actor, IntakeInput, string) (CreatedIntake, error)
	CreateJob(context.Context, auth.Actor, CreateJobInput) (CreatedIntake, error)
	UpdateJob(context.Context, auth.Actor, UpdateJobInput) error
	ArchiveJob(context.Context, auth.Actor, string, int32, string) error
	ListCustomers(context.Context, string, int, int) (Page[CustomerSummary], error)
	CustomerDetail(context.Context, string) (CustomerDetail, error)
	UpdateCustomer(context.Context, auth.Actor, UpdateCustomerInput) error
	ArchiveCustomer(context.Context, auth.Actor, string, int32, string) error
	ListWaitlist(context.Context, WaitlistFilter) (Page[WaitlistItem], error)
	UpdateWaitlistPriority(context.Context, auth.Actor, string, int32, int32, string) error
	RemoveWaitlist(context.Context, auth.Actor, string, int32, string, string) error
	AddNote(context.Context, auth.Actor, string, string, string, string) (string, error)
}

func (service *Service) CreateJob(ctx context.Context, actor auth.Actor, input CreateJobInput) (CreatedIntake, error) {
	for _, permission := range []auth.Permission{auth.PermissionJobCreate, auth.PermissionWaitlistAdd} {
		if err := actor.Require(permission); err != nil {
			return CreatedIntake{}, err
		}
	}
	input.CustomerID = strings.TrimSpace(input.CustomerID)
	input.InitialNote = strings.TrimSpace(input.InitialNote)
	input.Job.VolumeM3, _ = CanonicalVolume(input.Job.VolumeM3)
	input.Job.PreferenceText = strings.TrimSpace(input.Job.PreferenceText)
	input.Job.Region = strings.TrimSpace(input.Job.Region)
	if input.CustomerID == "" || len([]rune(input.InitialNote)) > 4000 {
		return CreatedIntake{}, ErrValidation
	}
	if err := input.Job.Validate(); err != nil {
		return CreatedIntake{}, err
	}
	created, err := service.store.CreateJob(ctx, actor, input)
	if err != nil {
		return CreatedIntake{}, fmt.Errorf("customers: creating job: %w", err)
	}
	return created, nil
}

func (service *Service) UpdateJob(ctx context.Context, actor auth.Actor, input UpdateJobInput) error {
	if err := actor.Require(auth.PermissionJobUpdate); err != nil {
		return err
	}
	input.ID = strings.TrimSpace(input.ID)
	input.Job.VolumeM3, _ = CanonicalVolume(input.Job.VolumeM3)
	input.Job.PreferenceText = strings.TrimSpace(input.Job.PreferenceText)
	input.Job.Region = strings.TrimSpace(input.Job.Region)
	if input.ID == "" || input.ExpectedVersion < 1 {
		return ErrValidation
	}
	if err := input.Job.Validate(); err != nil {
		return err
	}
	return service.store.UpdateJob(ctx, actor, input)
}

func (service *Service) ArchiveJob(ctx context.Context, actor auth.Actor, id string, version int32, requestID string) error {
	if err := actor.Require(auth.PermissionJobArchive); err != nil {
		return err
	}
	if strings.TrimSpace(id) == "" || version < 1 {
		return ErrValidation
	}
	return service.store.ArchiveJob(ctx, actor, id, version, requestID)
}

type Service struct{ store Store }

func NewService(store Store) (*Service, error) {
	if store == nil {
		return nil, errors.New("customers: store is required")
	}
	return &Service{store: store}, nil
}

func (service *Service) CreateIntake(ctx context.Context, actor auth.Actor, input IntakeInput, requestID string) (CreatedIntake, error) {
	for _, permission := range []auth.Permission{auth.PermissionCustomerCreate, auth.PermissionJobCreate, auth.PermissionWaitlistAdd} {
		if err := actor.Require(permission); err != nil {
			return CreatedIntake{}, err
		}
	}
	normalizeIntake(&input)
	if err := input.Customer.Validate(); err != nil {
		return CreatedIntake{}, err
	}
	if err := input.Job.Validate(); err != nil {
		return CreatedIntake{}, err
	}
	if len([]rune(input.InitialNote)) > 4000 {
		return CreatedIntake{}, fmt.Errorf("%w: note is too long", ErrValidation)
	}
	duplicates, err := service.store.FindDuplicates(ctx, input.Customer)
	if err != nil {
		return CreatedIntake{}, fmt.Errorf("customers: checking duplicates: %w", err)
	}
	created, err := service.store.CreateIntake(ctx, actor, input, requestID)
	if err != nil {
		return CreatedIntake{}, fmt.Errorf("customers: creating intake: %w", err)
	}
	created.Duplicates = duplicates
	return created, nil
}

func (service *Service) ListCustomers(ctx context.Context, actor auth.Actor, search string, page int) (Page[CustomerSummary], error) {
	if err := actor.Require(auth.PermissionCustomerUpdate); err != nil {
		return Page[CustomerSummary]{}, err
	}
	if page < 1 {
		page = 1
	}
	return service.store.ListCustomers(ctx, strings.TrimSpace(search), page, 25)
}

func (service *Service) CustomerDetail(ctx context.Context, actor auth.Actor, id string) (CustomerDetail, error) {
	if err := actor.Require(auth.PermissionCustomerUpdate); err != nil {
		return CustomerDetail{}, err
	}
	return service.store.CustomerDetail(ctx, id)
}

func (service *Service) UpdateCustomer(ctx context.Context, actor auth.Actor, input UpdateCustomerInput) error {
	if err := actor.Require(auth.PermissionCustomerUpdate); err != nil {
		return err
	}
	normalizeCustomer(&input.Customer)
	if input.ExpectedVersion < 1 || input.ID == "" {
		return ErrValidation
	}
	if err := input.Customer.Validate(); err != nil {
		return err
	}
	return service.store.UpdateCustomer(ctx, actor, input)
}

func (service *Service) ArchiveCustomer(ctx context.Context, actor auth.Actor, id string, version int32, requestID string) error {
	if err := actor.Require(auth.PermissionCustomerArchive); err != nil {
		return err
	}
	if id == "" || version < 1 {
		return ErrValidation
	}
	return service.store.ArchiveCustomer(ctx, actor, id, version, requestID)
}

func (service *Service) ListWaitlist(ctx context.Context, actor auth.Actor, filter WaitlistFilter) (Page[WaitlistItem], error) {
	if err := actor.Require(auth.PermissionDashboardView); err != nil {
		return Page[WaitlistItem]{}, err
	}
	filter.Normalize()
	return service.store.ListWaitlist(ctx, filter)
}

func (service *Service) UpdateWaitlistPriority(ctx context.Context, actor auth.Actor, id string, priority int32, version int32, requestID string) error {
	if err := actor.Require(auth.PermissionWaitlistPrioritize); err != nil {
		return err
	}
	if id == "" || version < 1 || priority < -100 || priority > 100 {
		return ErrValidation
	}
	return service.store.UpdateWaitlistPriority(ctx, actor, id, priority, version, requestID)
}

func (service *Service) RemoveWaitlist(ctx context.Context, actor auth.Actor, id string, version int32, reason string, requestID string) error {
	if err := actor.Require(auth.PermissionWaitlistPrioritize); err != nil {
		return err
	}
	if id == "" || version < 1 || !allowedRemovalReason(reason) {
		return ErrValidation
	}
	return service.store.RemoveWaitlist(ctx, actor, id, version, reason, requestID)
}

func (service *Service) AddNote(ctx context.Context, actor auth.Actor, jobID string, body string, correctionOfID string, requestID string) (string, error) {
	if err := actor.Require(auth.PermissionJobUpdate); err != nil {
		return "", err
	}
	body = strings.TrimSpace(body)
	if jobID == "" || body == "" || len([]rune(body)) > 4000 {
		return "", ErrValidation
	}
	return service.store.AddNote(ctx, actor, jobID, body, correctionOfID, requestID)
}

func normalizeIntake(input *IntakeInput) {
	normalizeCustomer(&input.Customer)
	input.Job.VolumeM3, _ = CanonicalVolume(input.Job.VolumeM3)
	input.Job.PreferenceText = strings.TrimSpace(input.Job.PreferenceText)
	input.Job.Region = strings.TrimSpace(input.Job.Region)
	input.InitialNote = strings.TrimSpace(input.InitialNote)
}

func normalizeCustomer(input *CustomerInput) {
	input.FirstName = strings.TrimSpace(input.FirstName)
	input.LastName = strings.TrimSpace(input.LastName)
	input.CompanyName = strings.TrimSpace(input.CompanyName)
	input.Street = strings.TrimSpace(input.Street)
	input.PostalCode = strings.TrimSpace(input.PostalCode)
	input.Locality = strings.TrimSpace(input.Locality)
	input.Region = strings.TrimSpace(input.Region)
	input.CountryCode = strings.ToUpper(strings.TrimSpace(input.CountryCode))
	if input.CountryCode == "" {
		input.CountryCode = "AT"
	}
	input.AddressFreeform = strings.TrimSpace(input.AddressFreeform)
	input.PhoneRaw = strings.TrimSpace(input.PhoneRaw)
	input.Email = strings.TrimSpace(input.Email)
}

func allowedRemovalReason(value string) bool {
	return value == "scheduled" || value == "cancelled" || value == "duplicate" || value == "other"
}
