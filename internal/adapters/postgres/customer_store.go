package postgres

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"example.invalid/hackplan/internal/adapters/postgres/dbgen"
	"example.invalid/hackplan/internal/auth"
	"example.invalid/hackplan/internal/customers"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

type CustomerStore struct {
	pool    *pgxpool.Pool
	queries *dbgen.Queries
}

func NewCustomerStore(pool *pgxpool.Pool) *CustomerStore {
	return &CustomerStore{pool: pool, queries: dbgen.New(pool)}
}

func (store *CustomerStore) FindDuplicates(ctx context.Context, input customers.CustomerInput) ([]customers.Duplicate, error) {
	rows, err := store.queries.FindDuplicateCustomers(ctx, dbgen.FindDuplicateCustomersParams{
		PhoneNormalized: customers.NormalizePhone(input.PhoneRaw), Email: input.Email,
		FirstName: input.FirstName, LastName: input.LastName, Locality: input.Locality,
	})
	if err != nil {
		return nil, err
	}
	result := make([]customers.Duplicate, 0, len(rows))
	for _, row := range rows {
		result = append(result, customers.Duplicate{
			ID: row.ID, FirstName: row.FirstName, LastName: row.LastName,
			CompanyName: row.CompanyName, Locality: row.Locality,
		})
	}
	return result, nil
}

func (store *CustomerStore) CreateIntake(ctx context.Context, actor auth.Actor, input customers.IntakeInput, requestID string) (created customers.CreatedIntake, resultErr error) {
	resultErr = store.transaction(ctx, func(queries *dbgen.Queries) error {
		customerID, err := queries.InsertCustomer(ctx, customerParams(input.Customer))
		if err != nil {
			return err
		}
		jobNumber, err := nextJobNumber(ctx, queries)
		if err != nil {
			return err
		}
		jobParams, err := insertJobParams(customerID, jobNumber, input.Job)
		if err != nil {
			return err
		}
		jobID, err := queries.InsertJob(ctx, jobParams)
		if err != nil {
			return err
		}
		waitlistID, err := queries.InsertWaitlistEntry(ctx, dbgen.InsertWaitlistEntryParams{
			JobID: mustUUID(jobID), RegionSnapshot: input.Job.Region,
		})
		if err != nil {
			return err
		}
		if input.InitialNote != "" {
			if _, err := queries.InsertJobNote(ctx, dbgen.InsertJobNoteParams{
				JobID: mustUUID(jobID), AuthorUserID: mustUUID(actor.UserID), Body: input.InitialNote,
			}); err != nil {
				return err
			}
			if err := insertAudit(ctx, queries, actor, "job.note_added", "job", jobID, requestID, []string{"note"}); err != nil {
				return err
			}
		}
		for _, audit := range []struct{ action, kind, id string }{
			{action: "customer.created", kind: "customer", id: customerID},
			{action: "job.created", kind: "job", id: jobID},
			{action: "job.waitlisted", kind: "waitlist_entry", id: waitlistID},
		} {
			if err := insertAudit(ctx, queries, actor, audit.action, audit.kind, audit.id, requestID, []string{"created"}); err != nil {
				return err
			}
		}
		created = customers.CreatedIntake{CustomerID: customerID, JobID: jobID, WaitlistID: waitlistID, JobNumber: jobNumber}
		return nil
	})
	return created, resultErr
}

func (store *CustomerStore) CreateJob(ctx context.Context, actor auth.Actor, input customers.CreateJobInput) (created customers.CreatedIntake, resultErr error) {
	resultErr = store.transaction(ctx, func(queries *dbgen.Queries) error {
		customerID, err := uuid(input.CustomerID)
		if err != nil {
			return customers.ErrNotFound
		}
		if _, err := queries.LockActiveCustomer(ctx, customerID); errors.Is(err, pgx.ErrNoRows) {
			return customers.ErrNotFound
		} else if err != nil {
			return err
		}
		jobNumber, err := nextJobNumber(ctx, queries)
		if err != nil {
			return err
		}
		params, err := insertJobParams(input.CustomerID, jobNumber, input.Job)
		if err != nil {
			return err
		}
		jobID, err := queries.InsertJob(ctx, params)
		if err != nil {
			return err
		}
		waitlistID, err := queries.InsertWaitlistEntry(ctx, dbgen.InsertWaitlistEntryParams{JobID: mustUUID(jobID), RegionSnapshot: input.Job.Region})
		if err != nil {
			return err
		}
		if input.InitialNote != "" {
			if _, err := queries.InsertJobNote(ctx, dbgen.InsertJobNoteParams{JobID: mustUUID(jobID), AuthorUserID: mustUUID(actor.UserID), Body: input.InitialNote}); err != nil {
				return err
			}
			if err := insertAudit(ctx, queries, actor, "job.note_added", "job", jobID, input.RequestID, []string{"note"}); err != nil {
				return err
			}
		}
		for _, audit := range []struct{ action, kind, id string }{
			{action: "job.created", kind: "job", id: jobID},
			{action: "job.waitlisted", kind: "waitlist_entry", id: waitlistID},
		} {
			if err := insertAudit(ctx, queries, actor, audit.action, audit.kind, audit.id, input.RequestID, []string{"created"}); err != nil {
				return err
			}
		}
		created = customers.CreatedIntake{CustomerID: input.CustomerID, JobID: jobID, WaitlistID: waitlistID, JobNumber: jobNumber}
		return nil
	})
	return created, resultErr
}

func (store *CustomerStore) UpdateJob(ctx context.Context, actor auth.Actor, input customers.UpdateJobInput) error {
	return store.transaction(ctx, func(queries *dbgen.Queries) error {
		if err := queries.LockSchedulingMutation(ctx); err != nil {
			return err
		}
		id, err := uuid(input.ID)
		if err != nil {
			return customers.ErrNotFound
		}
		params, err := updateJobParams(id, input)
		if err != nil {
			return err
		}
		rows, err := queries.UpdateJob(ctx, params)
		if err != nil {
			return err
		}
		if rows == 0 {
			return customers.ErrConflict
		}
		return insertAudit(ctx, queries, actor, "job.updated", "job", input.ID, input.RequestID,
			[]string{"job_type", "volume_m3", "durations", "transport", "preferred_range", "urgency", "region", "source", "pile_location"})
	})
}

func (store *CustomerStore) ArchiveJob(ctx context.Context, actor auth.Actor, id string, version int32, requestID string) error {
	return store.transaction(ctx, func(queries *dbgen.Queries) error {
		jobID, err := uuid(id)
		if err != nil {
			return customers.ErrNotFound
		}
		current, err := queries.LockJobForArchive(ctx, jobID)
		if errors.Is(err, pgx.ErrNoRows) {
			return customers.ErrNotFound
		}
		if err != nil {
			return err
		}
		if current.Version != version || (current.WorkflowStatus != "waitlist" && current.WorkflowStatus != "planning") {
			return customers.ErrConflict
		}
		active, err := queries.JobHasActiveAppointment(ctx, jobID)
		if err != nil {
			return err
		}
		if active {
			return customers.ErrConflict
		}
		rows, err := queries.ArchiveJob(ctx, dbgen.ArchiveJobParams{ID: jobID, ExpectedVersion: version})
		if err != nil {
			return err
		}
		if rows == 0 {
			return customers.ErrConflict
		}
		if _, err := queries.RemoveActiveWaitlistForJob(ctx, jobID); err != nil {
			return err
		}
		return insertAudit(ctx, queries, actor, "job.archived", "job", id, requestID,
			[]string{"archived_at", "workflow_status", "waitlist"})
	})
}

func (store *CustomerStore) ListCustomers(ctx context.Context, filter customers.CustomerListFilter) (customers.Page[customers.CustomerSummary], error) {
	pageOffset, pageSize, err := pageValues(filter.Page, filter.PageSize)
	if err != nil {
		return customers.Page[customers.CustomerSummary]{}, err
	}
	params := dbgen.ListCustomersParams{
		IncludeArchived: filter.IncludeArchived, Search: filter.Search,
		SearchPhone: customers.NormalizePhone(filter.Search), Sort: filter.Sort, Direction: filter.Direction,
		PageOffset: pageOffset, PageSize: pageSize,
	}
	rows, err := store.queries.ListCustomers(ctx, params)
	if err != nil {
		return customers.Page[customers.CustomerSummary]{}, err
	}
	items := make([]customers.CustomerSummary, 0, len(rows))
	for _, row := range rows {
		item := customers.CustomerSummary{
			ID: row.CID, FirstName: row.FirstName, LastName: row.LastName, CompanyName: row.CompanyName,
			Locality: row.Locality, Region: row.Region, PhoneRaw: row.PhoneRaw, Email: row.Email,
			NotificationPreference: customers.NotificationPreference(row.NotificationPreference), Version: row.Version,
			ActiveJobCount: row.ActiveJobCount, HistoricalJobCount: row.HistoricalJobCount,
			Archived: anyBool(row.Archived), LastUsedAt: anyTime(row.LastUsedAt), UpdatedAt: row.UpdatedAt.Time,
		}
		item.JobCount = item.ActiveJobCount + item.HistoricalJobCount
		item.HasContact = item.PhoneRaw != "" || item.Email != ""
		item.AddressComplete = row.AddressFreeform != "" || (row.Street != "" && row.PostalCode != "" && item.Locality != "")
		item.MapsURL = customers.MapsURL(customers.CustomerInput{
			Street: row.Street, PostalCode: row.PostalCode, Locality: item.Locality, Region: item.Region,
			CountryCode: row.CCountryCode, AddressFreeform: row.AddressFreeform,
			Latitude: parseFloat(row.Latitude), Longitude: parseFloat(row.Longitude),
		})
		items = append(items, item)
	}
	total, err := store.queries.CountCustomers(ctx, dbgen.CountCustomersParams{
		IncludeArchived: filter.IncludeArchived, Search: filter.Search, SearchPhone: customers.NormalizePhone(filter.Search),
	})
	if err != nil {
		return customers.Page[customers.CustomerSummary]{}, err
	}
	return pageOf(items, filter.Page, filter.PageSize, total), nil
}

func (store *CustomerStore) DuplicateJobDraft(ctx context.Context, id string) (customers.JobDraft, error) {
	jobID, err := uuid(id)
	if err != nil {
		return customers.JobDraft{}, customers.ErrNotFound
	}
	row, err := store.queries.GetJob(ctx, jobID)
	if errors.Is(err, pgx.ErrNoRows) {
		return customers.JobDraft{}, customers.ErrNotFound
	}
	if err != nil {
		return customers.JobDraft{}, err
	}
	detail, err := store.CustomerDetail(ctx, row.CustomerID)
	if err != nil {
		return customers.JobDraft{}, err
	}
	return customers.JobDraft{
		CustomerID: row.CustomerID, CustomerName: customerDisplayName(detail.Customer),
		Job: customers.JobInput{
			JobType: customers.JobType(row.JobType), VolumeM3: row.VolumeM3,
			EstimatedHackMinutes: int(row.EstimatedHackMinutes), EstimatedTransportMinutes: int(row.EstimatedTransportMinutes),
			TransportTripCount: int(row.TransportTripCount), TransportMode: customers.TransportMode(row.TransportMode),
			ExternalTransportConfirmed: row.ExternalTransportConfirmed, PreferredStartDate: row.PreferredStartDate,
			PreferredEndDate: row.PreferredEndDate, PreferenceText: row.PreferenceText, Urgency: customers.Urgency(row.Urgency),
			Region: row.Region, Source: customers.Source(row.Source), PileLatitude: parseFloat(row.PileLatitude),
			PileLongitude: parseFloat(row.PileLongitude), PileLocationSource: customers.PileLocationSource(row.PileLocationSource),
		},
	}, nil
}

func (store *CustomerStore) CustomerDetail(ctx context.Context, id string) (customers.CustomerDetail, error) {
	parsedID, err := uuid(id)
	if err != nil {
		return customers.CustomerDetail{}, customers.ErrNotFound
	}
	row, err := store.queries.GetCustomer(ctx, parsedID)
	if errors.Is(err, pgx.ErrNoRows) {
		return customers.CustomerDetail{}, customers.ErrNotFound
	}
	if err != nil {
		return customers.CustomerDetail{}, err
	}
	jobs, err := store.queries.ListCustomerJobs(ctx, parsedID)
	if err != nil {
		return customers.CustomerDetail{}, err
	}
	detail := customers.CustomerDetail{Customer: customerFromRow(row), Notes: make(map[string][]customers.Note)}
	appointments, err := store.queries.ListCustomerAppointments(ctx, parsedID)
	if err != nil {
		return customers.CustomerDetail{}, err
	}
	for _, value := range appointments {
		detail.Appointments = append(detail.Appointments, customers.AppointmentHistory{
			ID: value.AID, JobNumber: value.JobNumber, Lifecycle: value.LifecycleStatus,
			Confirmation: value.ConfirmationStatus, StartsAt: value.StartsAt.Time.UTC(), EndsAt: value.EndsAt.Time.UTC(),
		})
	}
	detail.Jobs = make([]customers.Job, 0, len(jobs))
	for _, jobRow := range jobs {
		job := jobFromRow(jobRow)
		detail.Jobs = append(detail.Jobs, job)
		notes, noteErr := store.queries.ListJobNotes(ctx, mustUUID(job.ID))
		if noteErr != nil {
			return customers.CustomerDetail{}, noteErr
		}
		for _, note := range notes {
			detail.Notes[job.ID] = append(detail.Notes[job.ID], customers.Note{
				ID: note.NID, JobID: note.NJobID, AuthorUserID: note.NAuthorUserID,
				AuthorName: note.AuthorName, Body: note.Body, CorrectionOfID: note.CorrectionOfID,
				CreatedAt: note.CreatedAt.Time,
			})
		}
	}
	input := customers.CustomerInput{
		Street: row.Street, PostalCode: row.PostalCode, Locality: row.Locality, Region: row.Region,
		CountryCode: row.CountryCode, AddressFreeform: row.AddressFreeform,
		Latitude: detail.Customer.Latitude, Longitude: detail.Customer.Longitude,
	}
	detail.MapsURL = customers.MapsURL(input)
	return detail, nil
}

func (store *CustomerStore) UpdateCustomer(ctx context.Context, actor auth.Actor, input customers.UpdateCustomerInput) error {
	return store.transaction(ctx, func(queries *dbgen.Queries) error {
		if err := queries.LockSchedulingMutation(ctx); err != nil {
			return err
		}
		id, err := uuid(input.ID)
		if err != nil {
			return customers.ErrNotFound
		}
		params := customerUpdateParams(id, input)
		rows, err := queries.UpdateCustomer(ctx, params)
		if err != nil {
			return err
		}
		if rows == 0 {
			return customers.ErrConflict
		}
		return insertAudit(ctx, queries, actor, "customer.updated", "customer", input.ID, input.RequestID,
			[]string{"name", "company_name", "address", "contact", "notification_preference"})
	})
}

func (store *CustomerStore) ArchiveCustomer(ctx context.Context, actor auth.Actor, id string, version int32, requestID string) error {
	customerID, err := uuid(id)
	if err != nil {
		return customers.ErrNotFound
	}
	return store.transaction(ctx, func(queries *dbgen.Queries) error {
		currentVersion, lockErr := queries.LockCustomerForArchive(ctx, customerID)
		if errors.Is(lockErr, pgx.ErrNoRows) {
			return customers.ErrNotFound
		}
		if lockErr != nil {
			return lockErr
		}
		if currentVersion != version {
			return customers.ErrConflict
		}
		active, activeErr := queries.CustomerHasActiveWorkflow(ctx, customerID)
		if activeErr != nil {
			return activeErr
		}
		if active {
			return customers.ErrConflict
		}
		rows, err := queries.ArchiveCustomer(ctx, dbgen.ArchiveCustomerParams{ID: customerID, ExpectedVersion: version})
		if err != nil {
			return err
		}
		if rows == 0 {
			return customers.ErrConflict
		}
		return insertAudit(ctx, queries, actor, "customer.archived", "customer", id, requestID, []string{"archived_at"})
	})
}

func (store *CustomerStore) RecordRecentCustomer(ctx context.Context, userID string, customerID string) error {
	parsedUserID, err := uuid(userID)
	if err != nil {
		return auth.ErrForbidden
	}
	parsedCustomerID, err := uuid(customerID)
	if err != nil {
		return customers.ErrNotFound
	}
	return store.transaction(ctx, func(queries *dbgen.Queries) error {
		rows, err := queries.UpsertRecentCustomer(ctx, dbgen.UpsertRecentCustomerParams{UserID: parsedUserID, CustomerID: parsedCustomerID})
		if err != nil {
			return err
		}
		if rows == 0 {
			return customers.ErrNotFound
		}
		return queries.TrimRecentRecords(ctx, parsedUserID)
	})
}

func (store *CustomerStore) RecordRecentJob(ctx context.Context, userID string, jobID string) (string, error) {
	parsedUserID, err := uuid(userID)
	if err != nil {
		return "", auth.ErrForbidden
	}
	parsedJobID, err := uuid(jobID)
	if err != nil {
		return "", customers.ErrNotFound
	}
	var customerID string
	err = store.transaction(ctx, func(queries *dbgen.Queries) error {
		var queryErr error
		customerID, queryErr = queries.UpsertRecentJob(ctx, dbgen.UpsertRecentJobParams{UserID: parsedUserID, JobID: parsedJobID})
		if queryErr != nil {
			if errors.Is(queryErr, pgx.ErrNoRows) {
				return customers.ErrNotFound
			}
			return queryErr
		}
		return queries.TrimRecentRecords(ctx, parsedUserID)
	})
	return customerID, err
}

func (store *CustomerStore) ListRecent(ctx context.Context, userID string, limit int) ([]customers.RecentRecord, error) {
	parsedUserID, err := uuid(userID)
	if err != nil {
		return nil, auth.ErrForbidden
	}
	if limit < 1 || limit > 100 {
		return nil, customers.ErrValidation
	}
	resultLimit := int32(limit)
	rows, err := store.queries.ListRecentRecords(ctx, dbgen.ListRecentRecordsParams{UserID: parsedUserID, ResultLimit: resultLimit})
	if err != nil {
		return nil, err
	}
	result := make([]customers.RecentRecord, 0, len(rows))
	for _, row := range rows {
		result = append(result, customers.RecentRecord{CustomerID: row.CustomerID, JobID: row.JobID, Label: row.Label, Context: row.Context, ViewedAt: row.ViewedAt.Time})
	}
	return result, nil
}

func (store *CustomerStore) ListWaitlistFilterFavorites(ctx context.Context, userID string) ([]customers.WaitlistFilterFavorite, error) {
	parsedUserID, err := uuid(userID)
	if err != nil {
		return nil, auth.ErrForbidden
	}
	rows, err := store.queries.ListWaitlistFilterFavorites(ctx, parsedUserID)
	if err != nil {
		return nil, err
	}
	result := make([]customers.WaitlistFilterFavorite, 0, len(rows))
	for _, row := range rows {
		favorite := customers.WaitlistFilterFavorite{ID: row.ID, Name: row.Name, Filter: customers.WaitlistFilter{
			JobType: row.JobType, Region: row.Region, Urgency: row.Urgency, PreferredMonth: row.PreferredMonth,
			Workflow: row.Workflow, MissingLocation: row.MissingLocation, DurationIssue: row.DurationIssue,
			Sort: row.SortKey, Direction: row.SortDirection,
		}}
		favorite.Filter.Normalize()
		result = append(result, favorite)
	}
	return result, nil
}

func (store *CustomerStore) SaveWaitlistFilterFavorite(ctx context.Context, userID string, name string, filter customers.WaitlistFilter) error {
	parsedUserID, err := uuid(userID)
	if err != nil {
		return auth.ErrForbidden
	}
	return store.transaction(ctx, func(queries *dbgen.Queries) error {
		exists, err := queries.WaitlistFilterFavoriteExists(ctx, dbgen.WaitlistFilterFavoriteExistsParams{UserID: parsedUserID, Name: name})
		if err != nil {
			return err
		}
		count, err := queries.CountWaitlistFilterFavorites(ctx, parsedUserID)
		if err != nil {
			return err
		}
		if !exists && count >= 10 {
			return customers.ErrConflict
		}
		return queries.UpsertWaitlistFilterFavorite(ctx, dbgen.UpsertWaitlistFilterFavoriteParams{
			UserID: parsedUserID, Name: name, JobType: filter.JobType, Region: filter.Region,
			Urgency: filter.Urgency, PreferredMonth: filter.PreferredMonth, Workflow: filter.Workflow,
			MissingLocation: filter.MissingLocation, DurationIssue: filter.DurationIssue,
			SortKey: filter.Sort, SortDirection: filter.Direction,
		})
	})
}

func (store *CustomerStore) DeleteWaitlistFilterFavorite(ctx context.Context, userID string, id string) error {
	parsedUserID, err := uuid(userID)
	if err != nil {
		return auth.ErrForbidden
	}
	parsedID, err := uuid(id)
	if err != nil {
		return customers.ErrNotFound
	}
	rows, err := store.queries.DeleteWaitlistFilterFavorite(ctx, dbgen.DeleteWaitlistFilterFavoriteParams{ID: parsedID, UserID: parsedUserID})
	if err != nil {
		return err
	}
	if rows == 0 {
		return customers.ErrNotFound
	}
	return nil
}

func (store *CustomerStore) ListWaitlist(ctx context.Context, filter customers.WaitlistFilter) (customers.Page[customers.WaitlistItem], error) {
	pageOffset, pageSize, err := pageValues(filter.Page, filter.PageSize)
	if err != nil {
		return customers.Page[customers.WaitlistItem]{}, err
	}
	params := dbgen.ListWaitlistParams{
		Search: filter.Query, JobTypeFilter: filter.JobType, RegionFilter: filter.Region,
		UrgencyFilter: filter.Urgency, MonthFilter: filter.PreferredMonth, WorkflowFilter: filter.Workflow,
		MissingLocation: filter.MissingLocation, DurationIssue: filter.DurationIssue,
		Sort: filter.Sort, Direction: filter.Direction,
		PageOffset: pageOffset, PageSize: pageSize,
	}
	rows, err := store.queries.ListWaitlist(ctx, params)
	if err != nil {
		return customers.Page[customers.WaitlistItem]{}, err
	}
	items := make([]customers.WaitlistItem, 0, len(rows))
	for _, row := range rows {
		item := customers.WaitlistItem{
			WaitlistID: row.WaitlistID, JobID: row.WJobID, JobNumber: row.JobNumber, VolumeM3: row.JVolumeM3,
			PreferredStartDate: row.PreferredStartDate, PreferredEndDate: row.PreferredEndDate,
			PreferenceText: row.PreferenceText, Region: row.Region, CustomerID: row.CustomerID,
			FirstName: row.FirstName, LastName: row.LastName, CompanyName: row.CompanyName, Locality: row.Locality,
			NoteExcerpt: row.NoteExcerpt, JobType: customers.JobType(row.JobType), TransportMode: customers.TransportMode(row.TransportMode),
			Urgency: customers.Urgency(row.Urgency), EnteredAt: row.EnteredAt.Time, ManualPriority: row.ManualPriority,
			WaitlistVersion: row.WaitlistVersion, EstimatedHackMinutes: row.EstimatedHackMinutes, AgeDays: row.AgeDays,
			WorkflowStatus: row.WorkflowStatus, UpdatedAt: row.UpdatedAt.Time,
			HasPileLocation: boolPointerValue(row.HasPileLocation), HasActiveAppointment: row.HasActiveAppointment,
		}
		item.DurationIssue = customers.DurationNeedsReview(item.EstimatedHackMinutes)
		item.NextStep = waitlistNextStep(item)
		items = append(items, item)
	}
	total, err := store.queries.CountWaitlist(ctx, dbgen.CountWaitlistParams{
		Search: filter.Query, JobTypeFilter: filter.JobType, RegionFilter: filter.Region,
		UrgencyFilter: filter.Urgency, MonthFilter: filter.PreferredMonth, WorkflowFilter: filter.Workflow,
		MissingLocation: filter.MissingLocation, DurationIssue: filter.DurationIssue,
	})
	if err != nil {
		return customers.Page[customers.WaitlistItem]{}, err
	}
	return pageOf(items, filter.Page, filter.PageSize, total), nil
}

func (store *CustomerStore) UpdateWaitlistPriority(ctx context.Context, actor auth.Actor, id string, priority int32, version int32, requestID string) error {
	waitlistID, err := uuid(id)
	if err != nil {
		return customers.ErrNotFound
	}
	return store.waitlistMutation(ctx, actor, id, requestID, "waitlist.priority_changed", []string{"manual_priority"}, func(queries *dbgen.Queries) (int64, error) {
		return queries.UpdateWaitlistPriority(ctx, dbgen.UpdateWaitlistPriorityParams{Priority: priority, ID: waitlistID, ExpectedVersion: version})
	})
}

func (store *CustomerStore) RemoveWaitlist(ctx context.Context, actor auth.Actor, id string, version int32, reason string, requestID string) error {
	waitlistID, err := uuid(id)
	if err != nil {
		return customers.ErrNotFound
	}
	return store.waitlistMutation(ctx, actor, id, requestID, "waitlist.removed", []string{"removed_at", "removed_reason"}, func(queries *dbgen.Queries) (int64, error) {
		return queries.RemoveWaitlistEntry(ctx, dbgen.RemoveWaitlistEntryParams{Reason: &reason, ID: waitlistID, ExpectedVersion: version})
	})
}

func (store *CustomerStore) AddNote(ctx context.Context, actor auth.Actor, jobID string, body string, correctionOfID string, idempotencyKey string, requestID string) (noteID string, resultErr error) {
	parsedJobID, err := uuid(jobID)
	if err != nil {
		return "", customers.ErrNotFound
	}
	resultErr = store.transaction(ctx, func(queries *dbgen.Queries) error {
		row, err := queries.InsertJobNoteIdempotent(ctx, dbgen.InsertJobNoteIdempotentParams{
			JobID: parsedJobID, AuthorUserID: mustUUID(actor.UserID), Body: body,
			CorrectionOfID: correctionOfID, IdempotencyKey: &idempotencyKey,
		})
		if err != nil {
			return err
		}
		noteID = row.ID
		if !row.Inserted {
			return nil
		}
		return insertAudit(ctx, queries, actor, "job.note_added", "job", jobID, requestID, []string{"note"})
	})
	return noteID, resultErr
}

func (store *CustomerStore) waitlistMutation(ctx context.Context, actor auth.Actor, id string, requestID string, action string, fields []string, mutation func(*dbgen.Queries) (int64, error)) error {
	return store.transaction(ctx, func(queries *dbgen.Queries) error {
		rows, err := mutation(queries)
		if err != nil {
			return err
		}
		if rows == 0 {
			return customers.ErrConflict
		}
		return insertAudit(ctx, queries, actor, action, "waitlist_entry", id, requestID, fields)
	})
}

func (store *CustomerStore) transaction(ctx context.Context, operation func(*dbgen.Queries) error) (resultErr error) {
	tx, err := store.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() {
		rollbackErr := tx.Rollback(ctx)
		if rollbackErr != nil && !errors.Is(rollbackErr, pgx.ErrTxClosed) {
			resultErr = errors.Join(resultErr, rollbackErr)
		}
	}()
	if err := operation(store.queries.WithTx(tx)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func customerParams(input customers.CustomerInput) dbgen.InsertCustomerParams {
	return dbgen.InsertCustomerParams{
		FirstName: input.FirstName, LastName: input.LastName, CompanyName: input.CompanyName,
		Street: input.Street, PostalCode: input.PostalCode, Locality: input.Locality, Region: input.Region,
		CountryCode: input.CountryCode, AddressFreeform: input.AddressFreeform, PhoneRaw: input.PhoneRaw,
		PhoneNormalized: customers.NormalizePhone(input.PhoneRaw), Email: input.Email,
		NotificationPreference: string(input.NotificationPreference), Latitude: floatString(input.Latitude), Longitude: floatString(input.Longitude),
	}
}

func customerUpdateParams(id pgtype.UUID, input customers.UpdateCustomerInput) dbgen.UpdateCustomerParams {
	value := input.Customer
	return dbgen.UpdateCustomerParams{
		FirstName: value.FirstName, LastName: value.LastName, CompanyName: value.CompanyName,
		Street: value.Street, PostalCode: value.PostalCode, Locality: value.Locality, Region: value.Region,
		AddressFreeform: value.AddressFreeform, PhoneRaw: value.PhoneRaw,
		PhoneNormalized: customers.NormalizePhone(value.PhoneRaw), Email: value.Email,
		NotificationPreference: string(value.NotificationPreference), ID: id, ExpectedVersion: input.ExpectedVersion,
	}
}

func insertJobParams(customerID string, jobNumber string, input customers.JobInput) (dbgen.InsertJobParams, error) {
	var volume pgtype.Numeric
	if err := volume.Scan(input.VolumeM3); err != nil {
		return dbgen.InsertJobParams{}, customers.ErrValidation
	}
	hackMinutes, transportMinutes, transportTrips, err := jobIntegerValues(input)
	if err != nil {
		return dbgen.InsertJobParams{}, err
	}
	return dbgen.InsertJobParams{
		JobNumber: jobNumber, CustomerID: mustUUID(customerID), JobType: string(input.JobType), VolumeM3: volume,
		EstimatedHackMinutes: hackMinutes, EstimatedTransportMinutes: transportMinutes,
		TransportTripCount: transportTrips, TransportMode: string(input.TransportMode),
		ExternalTransportConfirmed: input.ExternalTransportConfirmed, PreferredStartDate: input.PreferredStartDate,
		PreferredEndDate: input.PreferredEndDate, PreferenceText: input.PreferenceText, Urgency: string(input.Urgency),
		Region: input.Region, Source: string(input.Source), PileLatitude: floatString(input.PileLatitude),
		PileLongitude: floatString(input.PileLongitude), PileLocationSource: string(input.PileLocationSource),
	}, nil
}

func updateJobParams(id pgtype.UUID, input customers.UpdateJobInput) (dbgen.UpdateJobParams, error) {
	var volume pgtype.Numeric
	if err := volume.Scan(input.Job.VolumeM3); err != nil {
		return dbgen.UpdateJobParams{}, customers.ErrValidation
	}
	hackMinutes, transportMinutes, transportTrips, err := jobIntegerValues(input.Job)
	if err != nil {
		return dbgen.UpdateJobParams{}, err
	}
	return dbgen.UpdateJobParams{
		JobType: string(input.Job.JobType), VolumeM3: volume,
		EstimatedHackMinutes:      hackMinutes,
		EstimatedTransportMinutes: transportMinutes,
		TransportTripCount:        transportTrips, TransportMode: string(input.Job.TransportMode),
		ExternalTransportConfirmed: input.Job.ExternalTransportConfirmed,
		PreferredStartDate:         input.Job.PreferredStartDate, PreferredEndDate: input.Job.PreferredEndDate,
		PreferenceText: input.Job.PreferenceText, Urgency: string(input.Job.Urgency),
		Region: input.Job.Region, Source: string(input.Job.Source),
		PileLatitude: floatString(input.Job.PileLatitude), PileLongitude: floatString(input.Job.PileLongitude),
		PileLocationSource: string(input.Job.PileLocationSource), ID: id, ExpectedVersion: input.ExpectedVersion,
	}, nil
}

func pageValues(page, pageSize int) (int32, int32, error) {
	if page < 1 || pageSize < 1 || pageSize > 100 {
		return 0, 0, customers.ErrValidation
	}
	if page-1 > math.MaxInt32/pageSize {
		return 0, 0, customers.ErrValidation
	}
	offset := int64(page-1) * int64(pageSize)
	// #nosec G115 -- offset and pageSize are explicitly bounded above before conversion.
	return int32(offset), int32(pageSize), nil
}

func jobIntegerValues(input customers.JobInput) (int32, int32, int32, error) {
	invalidDuration := input.EstimatedHackMinutes < 1 ||
		input.EstimatedHackMinutes > customers.MaxJobDurationMinutes ||
		input.EstimatedTransportMinutes < 0 ||
		input.EstimatedTransportMinutes > customers.MaxJobDurationMinutes
	invalidTrips := input.TransportTripCount < 0 || input.TransportTripCount > customers.MaxTransportTrips
	if invalidDuration || invalidTrips {
		return 0, 0, 0, customers.ErrValidation
	}
	// #nosec G115 -- all three values are explicitly bounded to small domain maxima above.
	return int32(input.EstimatedHackMinutes), int32(input.EstimatedTransportMinutes), int32(input.TransportTripCount), nil
}

func nextJobNumber(ctx context.Context, queries *dbgen.Queries) (string, error) {
	value, err := queries.NextJobNumber(ctx)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("HA-%d-%04d", value.Year, value.Sequence), nil
}

func customerFromRow(row dbgen.GetCustomerRow) customers.Customer {
	return customers.Customer{
		ID: row.ID, FirstName: row.FirstName, LastName: row.LastName, CompanyName: row.CompanyName,
		Street: row.Street, PostalCode: row.PostalCode, Locality: row.Locality, Region: row.Region,
		CountryCode: row.CountryCode, AddressFreeform: row.AddressFreeform, PhoneRaw: row.PhoneRaw,
		PhoneNormalized: row.PhoneNormalized, Email: row.Email, GeocodingStatus: row.GeocodingStatus,
		NotificationPreference: customers.NotificationPreference(row.NotificationPreference),
		Latitude:               parseFloat(row.Latitude), Longitude: parseFloat(row.Longitude), ArchivedAt: optionalTime(row.ArchivedAt),
		Version: row.Version, CreatedAt: row.CreatedAt.Time, UpdatedAt: row.UpdatedAt.Time,
	}
}

func customerDisplayName(customer customers.Customer) string {
	if value := strings.TrimSpace(customer.CompanyName); value != "" {
		return value
	}
	return strings.TrimSpace(customer.FirstName + " " + customer.LastName)
}

func waitlistNextStep(item customers.WaitlistItem) string {
	if !item.HasPileLocation {
		return "Haufenstandort erfassen"
	}
	if item.DurationIssue {
		return "Dauer prüfen"
	}
	switch item.WorkflowStatus {
	case "proposal":
		return "Vorschlag prüfen"
	case "scheduled":
		return "Eingeplant"
	default:
		return "Einplanen"
	}
}

func jobFromRow(row dbgen.ListCustomerJobsRow) customers.Job {
	job := customers.Job{
		ID: row.ID, JobNumber: row.JobNumber, JobType: customers.JobType(row.JobType), VolumeM3: row.VolumeM3,
		EstimatedHackMinutes: row.EstimatedHackMinutes, EstimatedTransportMinutes: row.EstimatedTransportMinutes,
		TransportTripCount: row.TransportTripCount, TransportMode: customers.TransportMode(row.TransportMode),
		ExternalTransportConfirmed: row.ExternalTransportConfirmed, PreferredStartDate: row.PreferredStartDate,
		PreferredEndDate: row.PreferredEndDate, PreferenceText: row.PreferenceText, Urgency: customers.Urgency(row.Urgency),
		Region: row.Region, Source: customers.Source(row.Source), WorkflowStatus: row.WorkflowStatus,
		ReceivedAt: row.ReceivedAt.Time, ArchivedAt: optionalTime(row.ArchivedAt), Version: row.Version,
		PileLatitude: parseFloat(row.PileLatitude), PileLongitude: parseFloat(row.PileLongitude),
		PileLocationSource: customers.PileLocationSource(row.PileLocationSource),
	}
	job.PileMapsURL = customers.PointMapsURL(job.PileLatitude, job.PileLongitude)
	return job
}

func pageOf[T any](items []T, page int, pageSize int, total int64) customers.Page[T] {
	totalPages := int((total + int64(pageSize) - 1) / int64(pageSize))
	return customers.Page[T]{Items: items, Page: page, PageSize: pageSize, Total: total, TotalPages: totalPages}
}

func mustUUID(value string) pgtype.UUID {
	id, _ := uuid(value)
	return id
}

func floatString(value *float64) string {
	if value == nil {
		return ""
	}
	return strconv.FormatFloat(*value, 'f', -1, 64)
}

func parseFloat(value string) *float64 {
	if value == "" {
		return nil
	}
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return nil
	}
	return &parsed
}

func optionalTime(value pgtype.Timestamptz) *time.Time {
	if !value.Valid {
		return nil
	}
	return &value.Time
}

func anyBool(value any) bool {
	result, _ := value.(bool)
	return result
}

func anyTime(value any) time.Time {
	switch typed := value.(type) {
	case time.Time:
		return typed
	case pgtype.Timestamptz:
		return typed.Time
	default:
		return time.Time{}
	}
}

func boolPointerValue(value *bool) bool {
	return value != nil && *value
}
