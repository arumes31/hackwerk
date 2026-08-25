package postgres

import (
	"context"
	"errors"
	"fmt"
	"strconv"
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
		if _, err := queries.GetActiveCustomer(ctx, customerID); errors.Is(err, pgx.ErrNoRows) {
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
			[]string{"job_type", "volume_m3", "durations", "transport", "preferred_range", "urgency", "region", "source"})
	})
}

func (store *CustomerStore) ArchiveJob(ctx context.Context, actor auth.Actor, id string, version int32, requestID string) error {
	return store.transaction(ctx, func(queries *dbgen.Queries) error {
		jobID, err := uuid(id)
		if err != nil {
			return customers.ErrNotFound
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

func (store *CustomerStore) ListCustomers(ctx context.Context, search string, page int, pageSize int) (customers.Page[customers.CustomerSummary], error) {
	rows, err := store.queries.ListCustomers(ctx, dbgen.ListCustomersParams{
		Search: search, SearchPhone: customers.NormalizePhone(search),
		PageOffset: int32((page - 1) * pageSize), PageSize: int32(pageSize),
	})
	if err != nil {
		return customers.Page[customers.CustomerSummary]{}, err
	}
	total, err := store.queries.CountCustomers(ctx, dbgen.CountCustomersParams{
		Search: search, SearchPhone: customers.NormalizePhone(search),
	})
	if err != nil {
		return customers.Page[customers.CustomerSummary]{}, err
	}
	items := make([]customers.CustomerSummary, 0, len(rows))
	for _, row := range rows {
		items = append(items, customers.CustomerSummary{
			ID: row.CID, FirstName: row.FirstName, LastName: row.LastName, CompanyName: row.CompanyName,
			Locality: row.Locality, Region: row.Region, PhoneRaw: row.PhoneRaw, Email: row.Email,
			Version: row.Version, JobCount: row.JobCount,
		})
	}
	return pageOf(items, page, pageSize, total), nil
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

func (store *CustomerStore) ListWaitlist(ctx context.Context, filter customers.WaitlistFilter) (customers.Page[customers.WaitlistItem], error) {
	params := dbgen.ListWaitlistParams{
		Search: filter.Query, JobTypeFilter: filter.JobType, RegionFilter: filter.Region,
		UrgencyFilter: filter.Urgency, MonthFilter: filter.PreferredMonth, Sort: filter.Sort,
		Direction: filter.Direction, PageOffset: int32((filter.Page - 1) * filter.PageSize), PageSize: int32(filter.PageSize),
	}
	rows, err := store.queries.ListWaitlist(ctx, params)
	if err != nil {
		return customers.Page[customers.WaitlistItem]{}, err
	}
	total, err := store.queries.CountWaitlist(ctx, dbgen.CountWaitlistParams{
		Search: filter.Query, JobTypeFilter: filter.JobType, RegionFilter: filter.Region,
		UrgencyFilter: filter.Urgency, MonthFilter: filter.PreferredMonth,
	})
	if err != nil {
		return customers.Page[customers.WaitlistItem]{}, err
	}
	items := make([]customers.WaitlistItem, 0, len(rows))
	for _, row := range rows {
		items = append(items, customers.WaitlistItem{
			WaitlistID: row.WaitlistID, JobID: row.WJobID, JobNumber: row.JobNumber, VolumeM3: row.JVolumeM3,
			PreferredStartDate: row.PreferredStartDate, PreferredEndDate: row.PreferredEndDate,
			PreferenceText: row.PreferenceText, Region: row.Region, CustomerID: row.CustomerID,
			FirstName: row.FirstName, LastName: row.LastName, CompanyName: row.CompanyName,
			Locality: row.Locality, NoteExcerpt: row.NoteExcerpt, JobType: customers.JobType(row.JobType),
			TransportMode: customers.TransportMode(row.TransportMode), Urgency: customers.Urgency(row.Urgency),
			EnteredAt: row.EnteredAt.Time, ManualPriority: row.ManualPriority,
			WaitlistVersion: row.WaitlistVersion, EstimatedHackMinutes: row.EstimatedHackMinutes, AgeDays: row.AgeDays,
		})
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

func (store *CustomerStore) AddNote(ctx context.Context, actor auth.Actor, jobID string, body string, correctionOfID string, requestID string) (noteID string, resultErr error) {
	parsedJobID, err := uuid(jobID)
	if err != nil {
		return "", customers.ErrNotFound
	}
	resultErr = store.transaction(ctx, func(queries *dbgen.Queries) error {
		var err error
		noteID, err = queries.InsertJobNote(ctx, dbgen.InsertJobNoteParams{
			JobID: parsedJobID, AuthorUserID: mustUUID(actor.UserID), Body: body, CorrectionOfID: correctionOfID,
		})
		if err != nil {
			return err
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
	return dbgen.InsertJobParams{
		JobNumber: jobNumber, CustomerID: mustUUID(customerID), JobType: string(input.JobType), VolumeM3: volume,
		EstimatedHackMinutes: int32(input.EstimatedHackMinutes), EstimatedTransportMinutes: int32(input.EstimatedTransportMinutes),
		TransportTripCount: int32(input.TransportTripCount), TransportMode: string(input.TransportMode),
		ExternalTransportConfirmed: input.ExternalTransportConfirmed, PreferredStartDate: input.PreferredStartDate,
		PreferredEndDate: input.PreferredEndDate, PreferenceText: input.PreferenceText, Urgency: string(input.Urgency),
		Region: input.Region, Source: string(input.Source),
	}, nil
}

func updateJobParams(id pgtype.UUID, input customers.UpdateJobInput) (dbgen.UpdateJobParams, error) {
	var volume pgtype.Numeric
	if err := volume.Scan(input.Job.VolumeM3); err != nil {
		return dbgen.UpdateJobParams{}, customers.ErrValidation
	}
	return dbgen.UpdateJobParams{
		JobType: string(input.Job.JobType), VolumeM3: volume,
		EstimatedHackMinutes:      int32(input.Job.EstimatedHackMinutes),
		EstimatedTransportMinutes: int32(input.Job.EstimatedTransportMinutes),
		TransportTripCount:        int32(input.Job.TransportTripCount), TransportMode: string(input.Job.TransportMode),
		ExternalTransportConfirmed: input.Job.ExternalTransportConfirmed,
		PreferredStartDate:         input.Job.PreferredStartDate, PreferredEndDate: input.Job.PreferredEndDate,
		PreferenceText: input.Job.PreferenceText, Urgency: string(input.Job.Urgency),
		Region: input.Job.Region, Source: string(input.Job.Source), ID: id, ExpectedVersion: input.ExpectedVersion,
	}, nil
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

func jobFromRow(row dbgen.ListCustomerJobsRow) customers.Job {
	return customers.Job{
		ID: row.ID, JobNumber: row.JobNumber, JobType: customers.JobType(row.JobType), VolumeM3: row.VolumeM3,
		EstimatedHackMinutes: row.EstimatedHackMinutes, EstimatedTransportMinutes: row.EstimatedTransportMinutes,
		TransportTripCount: row.TransportTripCount, TransportMode: customers.TransportMode(row.TransportMode),
		ExternalTransportConfirmed: row.ExternalTransportConfirmed, PreferredStartDate: row.PreferredStartDate,
		PreferredEndDate: row.PreferredEndDate, PreferenceText: row.PreferenceText, Urgency: customers.Urgency(row.Urgency),
		Region: row.Region, Source: customers.Source(row.Source), WorkflowStatus: row.WorkflowStatus,
		ReceivedAt: row.ReceivedAt.Time, ArchivedAt: optionalTime(row.ArchivedAt), Version: row.Version,
	}
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
