package customers

import (
	"context"
	"errors"
	"strings"
	"testing"

	"example.invalid/hackplan/internal/auth"
)

type storeStub struct {
	createCalls       int
	createCustomer    CustomerInput
	createdCustomerID string
	priorityCalls     int
	listCalls         int
	created           CreatedIntake
	duplicates        []Duplicate
	favoriteName      string
	favoriteFilter    WaitlistFilter
	createJob         CreateJobInput
	updateJob         UpdateJobInput
	archiveJobID      string
	archiveJobV       int32
	customerFilter    CustomerListFilter
	draft             JobDraft
	recentCustomer    string
	recentJob         string
	recent            []RecentRecord
	detail            CustomerDetail
	updateCustomer    UpdateCustomerInput
	archiveCustID     string
	archiveCustV      int32
	waitlistFilter    WaitlistFilter
	priorityID        string
	priority          int32
	priorityReason    string
	searchResults     []SearchResult
	removeID          string
	removeReason      string
	noteJobID         string
	noteBody          string
	noteKey           string
}

func (store *storeStub) FindDuplicates(context.Context, CustomerInput) ([]Duplicate, error) {
	return store.duplicates, nil
}
func (store *storeStub) CreateIntake(context.Context, auth.Actor, IntakeInput, string) (CreatedIntake, error) {
	store.createCalls++
	return store.created, nil
}
func (store *storeStub) CreateCustomer(_ context.Context, _ auth.Actor, input CustomerInput, _ string) (string, error) {
	store.createCustomer = input
	return store.createdCustomerID, nil
}
func (store *storeStub) CreateJob(_ context.Context, _ auth.Actor, input CreateJobInput) (CreatedIntake, error) {
	store.createJob = input
	return store.created, nil
}
func (store *storeStub) UpdateJob(_ context.Context, _ auth.Actor, input UpdateJobInput) error {
	store.updateJob = input
	return nil
}
func (store *storeStub) ArchiveJob(_ context.Context, _ auth.Actor, id string, version int32, _ string) error {
	store.archiveJobID, store.archiveJobV = id, version
	return nil
}
func (store *storeStub) ListCustomers(_ context.Context, filter CustomerListFilter) (Page[CustomerSummary], error) {
	store.listCalls++
	store.customerFilter = filter
	return Page[CustomerSummary]{}, nil
}
func (store *storeStub) CustomerDetail(context.Context, string) (CustomerDetail, error) {
	return store.detail, nil
}
func (store *storeStub) DuplicateJobDraft(context.Context, string) (JobDraft, error) {
	return store.draft, nil
}
func (store *storeStub) RecordRecentCustomer(_ context.Context, _ string, id string) error {
	store.recentCustomer = id
	return nil
}
func (store *storeStub) RecordRecentJob(_ context.Context, _ string, id string) (string, error) {
	store.recentJob = id
	return "recent-1", nil
}
func (store *storeStub) ListRecent(context.Context, string, int) ([]RecentRecord, error) {
	return store.recent, nil
}
func (store *storeStub) UpdateCustomer(_ context.Context, _ auth.Actor, input UpdateCustomerInput) error {
	store.updateCustomer = input
	return nil
}
func (store *storeStub) ArchiveCustomer(_ context.Context, _ auth.Actor, id string, version int32, _ string) error {
	store.archiveCustID, store.archiveCustV = id, version
	return nil
}
func (store *storeStub) ListWaitlist(_ context.Context, filter WaitlistFilter) (Page[WaitlistItem], error) {
	store.waitlistFilter = filter
	return Page[WaitlistItem]{}, nil
}
func (store *storeStub) ListWaitlistFilterFavorites(context.Context, string) ([]WaitlistFilterFavorite, error) {
	return nil, nil
}
func (store *storeStub) SaveWaitlistFilterFavorite(_ context.Context, _ string, name string, filter WaitlistFilter) error {
	store.favoriteName = name
	store.favoriteFilter = filter
	return nil
}
func (store *storeStub) DeleteWaitlistFilterFavorite(context.Context, string, string) error {
	return nil
}
func (store *storeStub) SearchWorkspace(context.Context, string) ([]SearchResult, error) {
	return store.searchResults, nil
}
func (store *storeStub) UpdateWaitlistPriority(_ context.Context, _ auth.Actor, id string, priority int32, reason string, _ int32, _ string) error {
	store.priorityCalls++
	store.priorityID, store.priority, store.priorityReason = id, priority, reason
	return nil
}
func (store *storeStub) RemoveWaitlist(_ context.Context, _ auth.Actor, id string, _ int32, reason string, _ string) error {
	store.removeID, store.removeReason = id, reason
	return nil
}
func (store *storeStub) AddNote(_ context.Context, _ auth.Actor, jobID, body, _ string, key, _ string) (string, error) {
	store.noteJobID, store.noteBody, store.noteKey = jobID, body, key
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

func TestCreateCustomerWithoutJobNormalizesAndReturnsDuplicateWarning(t *testing.T) {
	t.Parallel()
	store := &storeStub{
		createdCustomerID: "customer-1",
		duplicates:        []Duplicate{{ID: "existing"}},
	}
	service, err := NewService(store)
	if err != nil {
		t.Fatal(err)
	}

	created, err := service.CreateCustomer(t.Context(), auth.Actor{UserID: "driver-1", Role: auth.RoleDriver}, CreateCustomerInput{
		Customer:  CustomerInput{FirstName: "  Anna ", LastName: " Wald ", CountryCode: " at ", NotificationPreference: NotifyNone},
		RequestID: "request-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.CustomerID != "customer-1" || len(created.Duplicates) != 1 {
		t.Fatalf("created = %#v", created)
	}
	if store.createCustomer.FirstName != "Anna" || store.createCustomer.LastName != "Wald" || store.createCustomer.CountryCode != "AT" {
		t.Fatalf("stored customer = %#v", store.createCustomer)
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
	err := service.UpdateWaitlistPriority(context.Background(), auth.Actor{UserID: "driver-1", Role: auth.RoleDriver}, "entry", 10, "Ausnahme", 1, "")
	if !errors.Is(err, auth.ErrForbidden) {
		t.Fatalf("error = %v, want forbidden", err)
	}
	if store.priorityCalls != 0 {
		t.Fatalf("priority calls = %d, want 0", store.priorityCalls)
	}
}

func TestDriverCannotIncludeArchivedCustomers(t *testing.T) {
	t.Parallel()
	store := &storeStub{}
	service, _ := NewService(store)
	_, err := service.ListCustomers(context.Background(), auth.Actor{UserID: "driver-1", Role: auth.RoleDriver}, CustomerListFilter{IncludeArchived: true})
	if !errors.Is(err, auth.ErrForbidden) || store.listCalls != 0 {
		t.Fatalf("ListCustomers() error=%v calls=%d", err, store.listCalls)
	}
}

func TestCustomerListFilterNormalizeAllowlistAndTextFilters(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		filter CustomerListFilter
		want   CustomerListFilter
	}{
		{
			name: "valid filters survive",
			filter: CustomerListFilter{
				Search: "  Huber ", Locality: " Linz  ", Region: " Nord ",
				JobActivity: CustomerJobsActive, NotificationPreference: NotifyEmail,
				MissingContact: true, IncompleteAddress: true, Sort: "name", Direction: "desc",
				Page: 2, PageSize: 50,
			},
			want: CustomerListFilter{
				Search: "Huber", Locality: "Linz", Region: "Nord",
				JobActivity: CustomerJobsActive, NotificationPreference: NotifyEmail,
				MissingContact: true, IncompleteAddress: true, Sort: "name", Direction: "desc",
				Page: 2, PageSize: 50,
			},
		},
		{
			name: "unknown selections return to safe defaults",
			filter: CustomerListFilter{
				JobActivity: "historical", NotificationPreference: "fax", Sort: "sql", Direction: "sideways",
				Page: -2, PageSize: 1000,
			},
			want: CustomerListFilter{Sort: "recent", Direction: "desc", Page: 1, PageSize: 25},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			test.filter.Normalize()
			if test.filter != test.want {
				t.Fatalf("Normalize() = %#v, want %#v", test.filter, test.want)
			}
		})
	}
}

func TestFilterFavoriteDropsPersonalSearchAndNormalizesAllowlist(t *testing.T) {
	t.Parallel()
	store := &storeStub{}
	service, _ := NewService(store)
	err := service.SaveWaitlistFilterFavorite(context.Background(), auth.Actor{UserID: "driver-1", Role: auth.RoleDriver}, "  Nord  ", WaitlistFilter{
		Query: "Franz Huber", JobType: "invalid", Region: "Nord", Workflow: "proposal", DurationGroup: "long",
		Overdue: true, Unassigned: true, TransportPending: true, Sort: "duration", Direction: "desc",
	})
	if err != nil {
		t.Fatalf("SaveWaitlistFilterFavorite() error=%v", err)
	}
	if store.favoriteName != "Nord" || store.favoriteFilter.Query != "" || store.favoriteFilter.JobType != "" ||
		store.favoriteFilter.Workflow != "proposal" || store.favoriteFilter.DurationGroup != "long" ||
		!store.favoriteFilter.Overdue || !store.favoriteFilter.Unassigned || !store.favoriteFilter.TransportPending ||
		store.favoriteFilter.Sort != "duration" {
		t.Fatalf("favorite=%q filter=%#v", store.favoriteName, store.favoriteFilter)
	}
}

func TestAddNoteRequiresStableIdempotencyKey(t *testing.T) {
	t.Parallel()
	service, _ := NewService(&storeStub{})
	_, err := service.AddNote(context.Background(), auth.Actor{UserID: "driver-1", Role: auth.RoleDriver}, "job-1", "Text", "", "", "request-1")
	if !errors.Is(err, ErrValidation) {
		t.Fatalf("AddNote() error=%v, want validation", err)
	}
}

func TestCustomerServiceDelegatesAuthorizedOperations(t *testing.T) {
	t.Parallel()
	store := &storeStub{
		created:    CreatedIntake{JobID: "job-created", JobNumber: "HW-2026-000001"},
		duplicates: []Duplicate{{ID: "self"}, {ID: "other", Locality: "Linz"}},
		draft:      JobDraft{CustomerID: "customer-1", CustomerName: "Franz Huber"},
		recent:     []RecentRecord{{CustomerID: "customer-1", JobID: "job-1"}},
		detail:     CustomerDetail{Customer: Customer{ID: "customer-1"}},
	}
	service, err := NewService(store)
	if err != nil {
		t.Fatal(err)
	}
	driver := auth.Actor{UserID: "driver-1", Role: auth.RoleDriver}
	admin := auth.Actor{UserID: "admin-1", Role: auth.RoleAdmin}

	created, err := service.CreateJob(t.Context(), driver, CreateJobInput{
		CustomerID: " customer-1 ", InitialNote: "  Einsatz aufnehmen ",
		Job: validIntake().Job,
	})
	if err != nil || created.JobID != "job-created" || store.createJob.CustomerID != "customer-1" || store.createJob.InitialNote != "Einsatz aufnehmen" {
		t.Fatalf("CreateJob result/input = %#v / %#v / %v", created, store.createJob, err)
	}

	updateJob := UpdateJobInput{ID: " job-1 ", ExpectedVersion: 2, Job: validIntake().Job}
	updateJob.Job.VolumeM3 = " 80,5 "
	updateJob.Job.PreferenceText = "  bald  "
	if err := service.UpdateJob(t.Context(), driver, updateJob); err != nil || store.updateJob.ID != "job-1" || store.updateJob.Job.VolumeM3 != "80.50" || store.updateJob.Job.PreferenceText != "bald" {
		t.Fatalf("UpdateJob input/error = %#v / %v", store.updateJob, err)
	}
	if err := service.ArchiveJob(t.Context(), admin, "job-1", 2, "request"); err != nil || store.archiveJobID != "job-1" || store.archiveJobV != 2 {
		t.Fatalf("ArchiveJob input/error = %q/%d/%v", store.archiveJobID, store.archiveJobV, err)
	}

	if _, err := service.ListCustomers(t.Context(), driver, CustomerListFilter{Search: "  Huber ", Sort: "name", Page: -5, PageSize: 200}); err != nil || store.customerFilter.Search != "Huber" || store.customerFilter.Page != 1 || store.customerFilter.PageSize != 25 {
		t.Fatalf("ListCustomers filter/error = %#v / %v", store.customerFilter, err)
	}
	if draft, err := service.DuplicateJobDraft(t.Context(), driver, " job-1 "); err != nil || draft.CustomerID != "customer-1" {
		t.Fatalf("DuplicateJobDraft = %#v / %v", draft, err)
	}
	duplicates, err := service.FindDuplicatesForCustomer(t.Context(), driver, Customer{ID: "self", FirstName: "Franz", Locality: "Linz"})
	if err != nil || len(duplicates) != 1 || duplicates[0].ID != "other" {
		t.Fatalf("FindDuplicatesForCustomer = %#v / %v", duplicates, err)
	}
	if err := service.RecordRecentCustomer(t.Context(), driver, "customer-1"); err != nil || store.recentCustomer != "customer-1" {
		t.Fatalf("RecordRecentCustomer = %q / %v", store.recentCustomer, err)
	}
	if recentID, err := service.RecordRecentJob(t.Context(), driver, "job-1"); err != nil || recentID != "recent-1" || store.recentJob != "job-1" {
		t.Fatalf("RecordRecentJob = %q/%q/%v", recentID, store.recentJob, err)
	}
	if recent, err := service.ListRecent(t.Context(), driver); err != nil || len(recent) != 1 {
		t.Fatalf("ListRecent = %#v / %v", recent, err)
	}
	if _, err := service.ListWaitlistFilterFavorites(t.Context(), driver); err != nil {
		t.Fatalf("ListWaitlistFilterFavorites = %v", err)
	}
	if err := service.DeleteWaitlistFilterFavorite(t.Context(), driver, "favorite-1"); err != nil {
		t.Fatalf("DeleteWaitlistFilterFavorite = %v", err)
	}
	if detail, err := service.CustomerDetail(t.Context(), driver, "customer-1"); err != nil || detail.Customer.ID != "customer-1" {
		t.Fatalf("CustomerDetail = %#v / %v", detail, err)
	}
	if err := service.UpdateCustomer(t.Context(), driver, UpdateCustomerInput{
		ID: "customer-1", ExpectedVersion: 3,
		Customer: CustomerInput{FirstName: "  Franz ", CountryCode: " at ", NotificationPreference: NotifyNone},
	}); err != nil || store.updateCustomer.Customer.FirstName != "Franz" || store.updateCustomer.Customer.CountryCode != "AT" {
		t.Fatalf("UpdateCustomer input/error = %#v / %v", store.updateCustomer, err)
	}
	if err := service.ArchiveCustomer(t.Context(), admin, "customer-1", 4, "request"); err != nil || store.archiveCustID != "customer-1" || store.archiveCustV != 4 {
		t.Fatalf("ArchiveCustomer input/error = %q/%d/%v", store.archiveCustID, store.archiveCustV, err)
	}
	if _, err := service.ListWaitlist(t.Context(), driver, WaitlistFilter{Sort: "volume", Direction: "desc"}); err != nil || store.waitlistFilter.Sort != "volume" {
		t.Fatalf("ListWaitlist filter/error = %#v / %v", store.waitlistFilter, err)
	}
	if err := service.UpdateWaitlistPriority(t.Context(), admin, "entry-1", -10, "Termin noch offen", 3, "request"); err != nil || store.priorityID != "entry-1" || store.priority != -10 || store.priorityReason != "Termin noch offen" {
		t.Fatalf("UpdateWaitlistPriority input/error = %q/%d/%v", store.priorityID, store.priority, err)
	}
	if err := service.RemoveWaitlist(t.Context(), admin, "entry-1", 3, "scheduled", "request"); err != nil || store.removeID != "entry-1" || store.removeReason != "scheduled" {
		t.Fatalf("RemoveWaitlist input/error = %q/%q/%v", store.removeID, store.removeReason, err)
	}
	if noteID, err := service.AddNote(t.Context(), driver, "job-1", "  Interne Notiz  ", "", " key-1 ", "request"); err != nil || noteID != "note-1" || store.noteBody != "Interne Notiz" || store.noteKey != "key-1" {
		t.Fatalf("AddNote input/error = %q/%#v/%v", noteID, store, err)
	}
}

func TestCustomerServiceRejectsInvalidArgumentsBeforeStore(t *testing.T) {
	t.Parallel()
	if _, err := NewService(nil); err == nil {
		t.Fatal("NewService(nil) accepted a nil store")
	}
	if _, err := NewService(&storeStub{}, WithDurationReviewThresholds(90, 60)); err == nil {
		t.Fatal("NewService accepted inverted duration review thresholds")
	}
	service, _ := NewService(&storeStub{})
	driver := auth.Actor{UserID: "driver-1", Role: auth.RoleDriver}
	admin := auth.Actor{UserID: "admin-1", Role: auth.RoleAdmin}
	for _, test := range []struct {
		name string
		call func() error
	}{
		{name: "empty duplicate source", call: func() error { _, err := service.DuplicateJobDraft(t.Context(), driver, " "); return err }},
		{name: "empty recent customer", call: func() error { return service.RecordRecentCustomer(t.Context(), driver, " ") }},
		{name: "empty recent job", call: func() error { _, err := service.RecordRecentJob(t.Context(), driver, " "); return err }},
		{name: "empty favorite", call: func() error { return service.DeleteWaitlistFilterFavorite(t.Context(), driver, " ") }},
		{name: "stale customer", call: func() error {
			return service.UpdateCustomer(t.Context(), driver, UpdateCustomerInput{ID: "customer", Customer: validIntake().Customer})
		}},
		{name: "empty archive customer", call: func() error { return service.ArchiveCustomer(t.Context(), admin, " ", 1, "request") }},
		{name: "invalid archive job", call: func() error { return service.ArchiveJob(t.Context(), admin, "job", 0, "request") }},
		{name: "invalid waitlist priority", call: func() error {
			return service.UpdateWaitlistPriority(t.Context(), admin, "entry", 101, "Außerhalb Bereich", 1, "request")
		}},
		{name: "invalid removal reason", call: func() error { return service.RemoveWaitlist(t.Context(), admin, "entry", 1, "untrusted", "request") }},
		{name: "invalid create job", call: func() error {
			_, err := service.CreateJob(t.Context(), driver, CreateJobInput{Job: validIntake().Job})
			return err
		}},
		{name: "long note", call: func() error {
			_, err := service.AddNote(t.Context(), driver, "job", string(make([]rune, 4001)), "", "key", "request")
			return err
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := test.call(); !errors.Is(err, ErrValidation) {
				t.Fatalf("error = %v, want validation error", err)
			}
		})
	}
	if err := PrepareIntake(nil); !errors.Is(err, ErrValidation) {
		t.Fatalf("PrepareIntake(nil) = %v", err)
	}
}

func TestCustomerServiceAppliesConfiguredDurationReviewThresholds(t *testing.T) {
	t.Parallel()
	store := &storeStub{}
	service, err := NewService(store, WithDurationReviewThresholds(30, 600))
	if err != nil {
		t.Fatal(err)
	}
	actor := auth.Actor{UserID: "driver-1", Role: auth.RoleDriver}
	if _, err := service.ListWaitlist(t.Context(), actor, WaitlistFilter{}); err != nil {
		t.Fatal(err)
	}
	if store.waitlistFilter.DurationReviewMinMinutes != 30 || store.waitlistFilter.DurationReviewMaxMinutes != 600 {
		t.Fatalf("configured thresholds = %#v", store.waitlistFilter)
	}
}

func TestWaitlistAssessmentAndWorkspaceSearchAreDerivedAndBounded(t *testing.T) {
	t.Parallel()
	store := &storeStub{searchResults: []SearchResult{{Kind: "job", ID: "job-1", Title: "HA-2026-0001"}}}
	service, _ := NewService(store)
	driver := auth.Actor{UserID: "driver-1", Role: auth.RoleDriver}
	results, err := service.SearchWorkspace(t.Context(), driver, "  HA-2026  ")
	if err != nil || len(results) != 1 || results[0].ID != "job-1" {
		t.Fatalf("SearchWorkspace() = %#v, %v", results, err)
	}
	for _, query := range []string{"x", strings.Repeat("x", 121)} {
		if _, err := service.SearchWorkspace(t.Context(), driver, query); !errors.Is(err, ErrValidation) {
			t.Fatalf("SearchWorkspace(%q) error = %v", query, err)
		}
	}
	if _, err := service.SearchWorkspace(t.Context(), auth.Actor{}, "HA"); !errors.Is(err, auth.ErrForbidden) {
		t.Fatalf("unauthorized SearchWorkspace() error = %v", err)
	}

	item := WaitlistItem{JobType: JobTypeChippingWithTransport, TransportMode: TransportUndecided, PreferenceMode: PreferenceWindow, DurationIssue: true}
	assessWaitlistItem(&item)
	if item.PlanReady || item.Completeness != 0 || len(item.MissingFields) != 6 || item.NextStep != "Einsatzort vollständig erfassen" {
		t.Fatalf("incomplete assessment = %#v", item)
	}
	ready := WaitlistItem{
		JobType: JobTypeChippingOnly, TransportMode: TransportNone, PreferenceMode: PreferenceFlexible,
		HasPileLocation: true, HasPileSource: true, HasContact: true, Region: "Nord",
	}
	assessWaitlistItem(&ready)
	if !ready.PlanReady || ready.Completeness != 100 || ready.NextStep != "Planungsbereit" {
		t.Fatalf("ready assessment = %#v", ready)
	}
}

func validIntake() IntakeInput {
	return IntakeInput{
		Customer: CustomerInput{FirstName: "Franz", LastName: "Huber", CountryCode: "AT", NotificationPreference: NotifyNone},
		Job: JobInput{
			JobType: JobTypeChippingOnly, VolumeM3: "80", EstimatedHackMinutes: 180,
			TransportMode: TransportNone, PreferenceMode: PreferenceWindow, Urgency: UrgencyNormal, Source: SourcePhone,
		},
	}
}
