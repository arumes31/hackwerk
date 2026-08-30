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
	NotificationPreference                                                  NotificationPreference
	Version, JobCount, ActiveJobCount, HistoricalJobCount                   int32
	Archived                                                                bool
	LastUsedAt, UpdatedAt                                                   time.Time
	MapsURL                                                                 string
	AddressComplete, HasContact                                             bool
}

type CustomerListFilter struct {
	Search, Sort, Direction string
	IncludeArchived         bool
	Page, PageSize          int
}

func (filter *CustomerListFilter) Normalize() {
	filter.Search = strings.TrimSpace(filter.Search)
	if filter.Sort != "name" && filter.Sort != "locality" && filter.Sort != "jobs" && filter.Sort != "recent" {
		filter.Sort = "recent"
	}
	if filter.Direction != "asc" && filter.Direction != "desc" {
		if filter.Sort == "recent" || filter.Sort == "jobs" {
			filter.Direction = "desc"
		} else {
			filter.Direction = "asc"
		}
	}
	if filter.Page < 1 {
		filter.Page = 1
	}
	if filter.PageSize < 1 || filter.PageSize > 100 {
		filter.PageSize = 25
	}
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
	ID, JobNumber, VolumeM3, PreferredStartDate, PreferredEndDate, ActiveAppointmentID string
	PreferenceText, Region, WorkflowStatus                                             string
	JobType                                                                            JobType
	TransportMode                                                                      TransportMode
	Urgency                                                                            Urgency
	Source                                                                             Source
	PreferenceMode                                                                     PreferenceMode
	EstimatedHackMinutes, EstimatedTransportMinutes, TransportTripCount                int32
	ExternalTransportConfirmed                                                         bool
	ReceivedAt                                                                         time.Time
	ArchivedAt                                                                         *time.Time
	Version                                                                            int32
	PileLatitude, PileLongitude                                                        *float64
	PileLocationSource                                                                 PileLocationSource
	PileMapsURL                                                                        string
}

type Note struct {
	ID, JobID, AuthorUserID, AuthorName, Body, CorrectionOfID string
	CreatedAt                                                 time.Time
}

type AppointmentHistory struct {
	ID, JobNumber, Lifecycle, Confirmation string
	StartsAt, EndsAt                       time.Time
}

type CustomerDetail struct {
	Customer      Customer
	Jobs          []Job
	Appointments  []AppointmentHistory
	Notes         map[string][]Note
	Duplicates    []Duplicate
	MapsURL       string
	PageRequestID string
}

type Duplicate struct{ ID, FirstName, LastName, CompanyName, Locality string }

type WaitlistItem struct {
	WaitlistID, JobID, JobNumber, VolumeM3, PreferredStartDate, PreferredEndDate                                                    string
	PreferenceText, Region, CustomerID, FirstName, LastName, CompanyName, Locality                                                  string
	NoteExcerpt                                                                                                                     string
	PriorityReason                                                                                                                  string
	JobType                                                                                                                         JobType
	TransportMode                                                                                                                   TransportMode
	Urgency                                                                                                                         Urgency
	PreferenceMode                                                                                                                  PreferenceMode
	EnteredAt                                                                                                                       time.Time
	ManualPriority, WaitlistVersion, EstimatedHackMinutes, EstimatedTransportMinutes, TotalMinutes, AgeDays                         int32
	WorkflowStatus, NextStep                                                                                                        string
	MissingFields                                                                                                                   []string
	Completeness                                                                                                                    int
	UpdatedAt                                                                                                                       time.Time
	HasPileLocation, HasPileSource, HasActiveAppointment, HasInternalAssignment, ExternalTransportConfirmed, DurationIssue, Overdue bool
	HasContact, PlanReady                                                                                                           bool
}

type JobDraft struct {
	CustomerID, CustomerName string
	Job                      JobInput
}

type RecentRecord struct {
	CustomerID, JobID, Label, Context string
	ViewedAt                          time.Time
}

type WaitlistFilterFavorite struct {
	ID, Name string
	Filter   WaitlistFilter
}

type Page[T any] struct {
	Items           []T
	Page            int
	PageSize        int
	Total           int64
	UnfilteredTotal int64
	TotalPages      int
	Recent          []RecentRecord
	Favorites       []WaitlistFilterFavorite
	CustomerFilter  CustomerListFilter
}

type SearchResult struct {
	Kind     string `json:"kind"`
	ID       string `json:"id"`
	ParentID string `json:"parent_id,omitempty"`
	Title    string `json:"title"`
	Subtitle string `json:"subtitle,omitempty"`
	Href     string `json:"href"`
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
	ListCustomers(context.Context, CustomerListFilter) (Page[CustomerSummary], error)
	CustomerDetail(context.Context, string) (CustomerDetail, error)
	DuplicateJobDraft(context.Context, string) (JobDraft, error)
	RecordRecentCustomer(context.Context, string, string) error
	RecordRecentJob(context.Context, string, string) (string, error)
	ListRecent(context.Context, string, int) ([]RecentRecord, error)
	UpdateCustomer(context.Context, auth.Actor, UpdateCustomerInput) error
	ArchiveCustomer(context.Context, auth.Actor, string, int32, string) error
	ListWaitlist(context.Context, WaitlistFilter) (Page[WaitlistItem], error)
	SearchWorkspace(context.Context, string) ([]SearchResult, error)
	ListWaitlistFilterFavorites(context.Context, string) ([]WaitlistFilterFavorite, error)
	SaveWaitlistFilterFavorite(context.Context, string, string, WaitlistFilter) error
	DeleteWaitlistFilterFavorite(context.Context, string, string) error
	UpdateWaitlistPriority(context.Context, auth.Actor, string, int32, string, int32, string) error
	RemoveWaitlist(context.Context, auth.Actor, string, int32, string, string) error
	AddNote(context.Context, auth.Actor, string, string, string, string, string) (string, error)
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
	if input.Job.PreferenceMode == "" {
		input.Job.PreferenceMode = PreferenceWindow
	}
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
	if input.Job.PreferenceMode == "" {
		input.Job.PreferenceMode = PreferenceWindow
	}
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

type Service struct {
	store                                              Store
	durationReviewMinMinutes, durationReviewMaxMinutes int32
}

type ServiceOption func(*Service) error

func WithDurationReviewThresholds(minimum, maximum int32) ServiceOption {
	return func(service *Service) error {
		if minimum < 1 || maximum <= minimum {
			return errors.New("customers: invalid duration review thresholds")
		}
		service.durationReviewMinMinutes, service.durationReviewMaxMinutes = minimum, maximum
		return nil
	}
}

func NewService(store Store, options ...ServiceOption) (*Service, error) {
	if store == nil {
		return nil, errors.New("customers: store is required")
	}
	service := &Service{store: store, durationReviewMinMinutes: 15, durationReviewMaxMinutes: 12 * 60}
	for _, option := range options {
		if option == nil {
			return nil, errors.New("customers: service option is required")
		}
		if err := option(service); err != nil {
			return nil, err
		}
	}
	return service, nil
}

func (service *Service) CreateIntake(ctx context.Context, actor auth.Actor, input IntakeInput, requestID string) (CreatedIntake, error) {
	for _, permission := range []auth.Permission{auth.PermissionCustomerCreate, auth.PermissionJobCreate, auth.PermissionWaitlistAdd} {
		if err := actor.Require(permission); err != nil {
			return CreatedIntake{}, err
		}
	}
	if err := PrepareIntake(&input); err != nil {
		return CreatedIntake{}, err
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

// PrepareIntake applies the same normalization and domain validation to every
// intake boundary, including the explicitly reviewed voice workflow.
func PrepareIntake(input *IntakeInput) error {
	if input == nil {
		return ErrValidation
	}
	normalizeIntake(input)
	if err := input.Customer.Validate(); err != nil {
		return err
	}
	if err := input.Job.Validate(); err != nil {
		return err
	}
	if len([]rune(input.InitialNote)) > 4000 {
		return fmt.Errorf("%w: note is too long", ErrValidation)
	}
	return nil
}

func (service *Service) ListCustomers(ctx context.Context, actor auth.Actor, filter CustomerListFilter) (Page[CustomerSummary], error) {
	if err := actor.Require(auth.PermissionCustomerUpdate); err != nil {
		return Page[CustomerSummary]{}, err
	}
	filter.Normalize()
	if filter.IncludeArchived && actor.Role != auth.RoleAdmin {
		return Page[CustomerSummary]{}, auth.ErrForbidden
	}
	return service.store.ListCustomers(ctx, filter)
}

func (service *Service) DuplicateJobDraft(ctx context.Context, actor auth.Actor, id string) (JobDraft, error) {
	if err := actor.Require(auth.PermissionJobCreate); err != nil {
		return JobDraft{}, err
	}
	if strings.TrimSpace(id) == "" {
		return JobDraft{}, ErrValidation
	}
	return service.store.DuplicateJobDraft(ctx, id)
}

func (service *Service) FindDuplicatesForCustomer(ctx context.Context, actor auth.Actor, customer Customer) ([]Duplicate, error) {
	if err := actor.Require(auth.PermissionCustomerUpdate); err != nil {
		return nil, err
	}
	duplicates, err := service.store.FindDuplicates(ctx, CustomerInput{
		FirstName: customer.FirstName, LastName: customer.LastName, CompanyName: customer.CompanyName,
		Locality: customer.Locality, PhoneRaw: customer.PhoneRaw, Email: customer.Email,
	})
	if err != nil {
		return nil, err
	}
	result := duplicates[:0]
	for _, duplicate := range duplicates {
		if duplicate.ID != customer.ID {
			result = append(result, duplicate)
		}
	}
	return result, nil
}

func (service *Service) RecordRecentCustomer(ctx context.Context, actor auth.Actor, customerID string) error {
	if err := actor.Require(auth.PermissionCustomerUpdate); err != nil {
		return err
	}
	if strings.TrimSpace(customerID) == "" {
		return ErrValidation
	}
	return service.store.RecordRecentCustomer(ctx, actor.UserID, customerID)
}

func (service *Service) RecordRecentJob(ctx context.Context, actor auth.Actor, jobID string) (string, error) {
	if err := actor.Require(auth.PermissionJobUpdate); err != nil {
		return "", err
	}
	if strings.TrimSpace(jobID) == "" {
		return "", ErrValidation
	}
	return service.store.RecordRecentJob(ctx, actor.UserID, jobID)
}

func (service *Service) ListRecent(ctx context.Context, actor auth.Actor) ([]RecentRecord, error) {
	if err := actor.Require(auth.PermissionCustomerUpdate); err != nil {
		return nil, err
	}
	return service.store.ListRecent(ctx, actor.UserID, 12)
}

func (service *Service) ListWaitlistFilterFavorites(ctx context.Context, actor auth.Actor) ([]WaitlistFilterFavorite, error) {
	if err := actor.Require(auth.PermissionDashboardView); err != nil {
		return nil, err
	}
	return service.store.ListWaitlistFilterFavorites(ctx, actor.UserID)
}

func (service *Service) SaveWaitlistFilterFavorite(ctx context.Context, actor auth.Actor, name string, filter WaitlistFilter) error {
	if err := actor.Require(auth.PermissionDashboardView); err != nil {
		return err
	}
	name = strings.TrimSpace(name)
	if name == "" || len([]rune(name)) > 60 {
		return ErrValidation
	}
	filter.Query = ""
	filter.Normalize()
	return service.store.SaveWaitlistFilterFavorite(ctx, actor.UserID, name, filter)
}

func (service *Service) DeleteWaitlistFilterFavorite(ctx context.Context, actor auth.Actor, id string) error {
	if err := actor.Require(auth.PermissionDashboardView); err != nil {
		return err
	}
	if strings.TrimSpace(id) == "" {
		return ErrValidation
	}
	return service.store.DeleteWaitlistFilterFavorite(ctx, actor.UserID, id)
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
	id = strings.TrimSpace(id)
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
	filter.DurationReviewMinMinutes = service.durationReviewMinMinutes
	filter.DurationReviewMaxMinutes = service.durationReviewMaxMinutes
	page, err := service.store.ListWaitlist(ctx, filter)
	if err != nil {
		return Page[WaitlistItem]{}, err
	}
	for index := range page.Items {
		assessWaitlistItem(&page.Items[index])
	}
	return page, nil
}

func (service *Service) SearchWorkspace(ctx context.Context, actor auth.Actor, query string) ([]SearchResult, error) {
	if err := actor.Require(auth.PermissionDashboardView); err != nil {
		return nil, err
	}
	query = strings.TrimSpace(query)
	if len([]rune(query)) < 2 || len([]rune(query)) > 120 {
		return nil, ErrValidation
	}
	return service.store.SearchWorkspace(ctx, query)
}

func (service *Service) UpdateWaitlistPriority(ctx context.Context, actor auth.Actor, id string, priority int32, reason string, version int32, requestID string) error {
	if err := actor.Require(auth.PermissionWaitlistPrioritize); err != nil {
		return err
	}
	reason = strings.TrimSpace(reason)
	if id == "" || version < 1 || priority < -100 || priority > 100 || len([]rune(reason)) > 240 || (priority != 0 && reason == "") {
		return ErrValidation
	}
	return service.store.UpdateWaitlistPriority(ctx, actor, id, priority, reason, version, requestID)
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

func (service *Service) AddNote(ctx context.Context, actor auth.Actor, jobID string, body string, correctionOfID string, idempotencyKey string, requestID string) (string, error) {
	if err := actor.Require(auth.PermissionJobUpdate); err != nil {
		return "", err
	}
	body = strings.TrimSpace(body)
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	if jobID == "" || body == "" || len([]rune(body)) > 4000 || idempotencyKey == "" || len([]rune(idempotencyKey)) > 200 {
		return "", ErrValidation
	}
	return service.store.AddNote(ctx, actor, jobID, body, correctionOfID, idempotencyKey, requestID)
}

func normalizeIntake(input *IntakeInput) {
	normalizeCustomer(&input.Customer)
	input.Job.VolumeM3, _ = CanonicalVolume(input.Job.VolumeM3)
	input.Job.PreferenceText = strings.TrimSpace(input.Job.PreferenceText)
	if input.Job.PreferenceMode == "" {
		input.Job.PreferenceMode = PreferenceWindow
	}
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

func assessWaitlistItem(item *WaitlistItem) {
	missing := make([]string, 0, 6)
	if !item.HasPileSource || !item.HasPileLocation {
		missing = append(missing, "Einsatzort vollständig erfassen")
	}
	if item.DurationIssue {
		missing = append(missing, "Dauer plausibilisieren")
	}
	if strings.TrimSpace(item.Region) == "" {
		missing = append(missing, "Region ergänzen")
	}
	if item.PreferenceMode == PreferenceWindow && (item.PreferredStartDate == "" || item.PreferredEndDate == "") {
		missing = append(missing, "Wunschzeitraum vervollständigen")
	}
	transportPending := item.JobType == JobTypeChippingWithTransport &&
		(item.TransportMode == TransportUndecided || (item.TransportMode == TransportExternal && !item.ExternalTransportConfirmed))
	if transportPending {
		missing = append(missing, "Transport klären")
	}
	if !item.HasContact {
		missing = append(missing, "passenden Benachrichtigungskontakt ergänzen")
	}
	item.MissingFields = missing
	item.Completeness = (6 - len(missing)) * 100 / 6
	item.PlanReady = len(missing) == 0
	if item.PlanReady {
		item.NextStep = "Planungsbereit"
		return
	}
	item.NextStep = missing[0]
}

func allowedRemovalReason(value string) bool {
	return value == "scheduled" || value == "cancelled" || value == "duplicate" || value == "other"
}
