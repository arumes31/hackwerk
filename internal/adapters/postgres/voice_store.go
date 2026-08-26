package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"example.invalid/hackplan/internal/adapters/postgres/dbgen"
	"example.invalid/hackplan/internal/auth"
	"example.invalid/hackplan/internal/customers"
	"example.invalid/hackplan/internal/voice"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

type VoiceStore struct {
	pool    *pgxpool.Pool
	queries *dbgen.Queries
	now     func() time.Time
}

func NewVoiceStore(pool *pgxpool.Pool) *VoiceStore {
	return &VoiceStore{pool: pool, queries: dbgen.New(pool), now: time.Now}
}

func (store *VoiceStore) Create(ctx context.Context, actor auth.Actor, expiresAt time.Time) (voice.Draft, error) {
	row, err := store.queries.InsertVoiceDraft(ctx, dbgen.InsertVoiceDraftParams{OwnerUserID: mustUUID(actor.UserID), ExpiresAt: timestamp(expiresAt.UTC())})
	if err != nil {
		return voice.Draft{}, err
	}
	return voice.Draft{ID: row.ID, OwnerUserID: actor.UserID, Status: voice.StatusTranscribing, Version: row.Version, ExpiresAt: expiresAt.UTC()}, nil
}

func (store *VoiceStore) Complete(ctx context.Context, actor auth.Actor, id string, transcript voice.Transcript, fields voice.Fields, warnings []string, confidence float64, parserVersion string) error {
	payload, err := json.Marshal(fields)
	if err != nil {
		return err
	}
	var numeric pgtype.Numeric
	if err = numeric.Scan(strconv.FormatFloat(confidence, 'f', 3, 64)); err != nil {
		return err
	}
	rows, err := store.queries.CompleteVoiceDraft(ctx, dbgen.CompleteVoiceDraftParams{Transcript: &transcript.Text, ExtractedFields: payload, Warnings: warnings, OverallConfidence: numeric, ProviderName: transcript.Provider, ProviderVersion: transcript.Version, ParserVersion: parserVersion, ID: mustUUID(id), OwnerUserID: mustUUID(actor.UserID)})
	if err != nil {
		return err
	}
	if rows == 0 {
		return voice.ErrConflict
	}
	return nil
}

func (store *VoiceStore) Fail(ctx context.Context, actor auth.Actor, id, code string) error {
	rows, err := store.queries.FailVoiceDraft(ctx, dbgen.FailVoiceDraftParams{FailureCode: code, ID: mustUUID(id), OwnerUserID: mustUUID(actor.UserID)})
	if err != nil {
		return err
	}
	if rows == 0 {
		return voice.ErrConflict
	}
	return nil
}

func (store *VoiceStore) Get(ctx context.Context, actor auth.Actor, id string) (voice.Draft, error) {
	parsedID, err := uuid(id)
	if err != nil {
		return voice.Draft{}, voice.ErrNotFound
	}
	row, err := store.queries.GetVoiceDraftForOwner(ctx, dbgen.GetVoiceDraftForOwnerParams{ID: parsedID, OwnerUserID: mustUUID(actor.UserID)})
	if errors.Is(err, pgx.ErrNoRows) {
		return voice.Draft{}, voice.ErrNotFound
	}
	if err != nil {
		return voice.Draft{}, err
	}
	var fields voice.Fields
	if err := json.Unmarshal(row.ExtractedFields, &fields); err != nil {
		return voice.Draft{}, fmt.Errorf("postgres: decoding voice draft: %w", err)
	}
	confidence, _ := strconv.ParseFloat(row.OverallConfidence, 64)
	status := voice.Status(row.Status)
	if row.ExpiresAt.Time.Before(store.now().UTC()) && status != voice.StatusCommitted {
		status = voice.StatusExpired
	}
	return voice.Draft{ID: row.ID, OwnerUserID: row.OwnerUserID, Status: status, Transcript: row.Transcript, Fields: fields, Warnings: row.Warnings, OverallConfidence: confidence, ProviderName: row.ProviderName, ProviderVersion: row.ProviderVersion, ParserVersion: row.ParserVersion, FailureCode: row.FailureCode, RetryCount: int32(row.RetryCount), Version: row.Version, Committed: customers.CreatedIntake{CustomerID: row.CommittedCustomerID, JobID: row.CommittedJobID, WaitlistID: row.CommittedWaitlistID}, CreatedAt: row.CreatedAt.Time.UTC(), UpdatedAt: row.UpdatedAt.Time.UTC(), ExpiresAt: row.ExpiresAt.Time.UTC()}, nil
}

func (store *VoiceStore) FindDuplicates(ctx context.Context, input customers.CustomerInput) ([]customers.Duplicate, error) {
	rows, err := store.queries.FindDuplicateCustomers(ctx, dbgen.FindDuplicateCustomersParams{PhoneNormalized: customers.NormalizePhone(input.PhoneRaw), Email: input.Email, FirstName: input.FirstName, LastName: input.LastName, Locality: input.Locality})
	if err != nil {
		return nil, err
	}
	result := make([]customers.Duplicate, 0, len(rows))
	for _, row := range rows {
		result = append(result, customers.Duplicate{ID: row.ID, FirstName: row.FirstName, LastName: row.LastName, CompanyName: row.CompanyName, Locality: row.Locality})
	}
	return result, nil
}

func (store *VoiceStore) Commit(ctx context.Context, actor auth.Actor, input voice.CommitInput) (created customers.CreatedIntake, resultErr error) {
	draftID, err := uuid(input.DraftID)
	if err != nil {
		return created, voice.ErrNotFound
	}
	resultErr = withQueries(ctx, store.pool, func(queries *dbgen.Queries) error {
		row, lockErr := queries.LockVoiceDraftForOwner(ctx, dbgen.LockVoiceDraftForOwnerParams{ID: draftID, OwnerUserID: mustUUID(actor.UserID)})
		if errors.Is(lockErr, pgx.ErrNoRows) {
			return voice.ErrNotFound
		}
		if lockErr != nil {
			return lockErr
		}
		if row.Status == string(voice.StatusCommitted) {
			created = customers.CreatedIntake{CustomerID: row.CommittedCustomerID, JobID: row.CommittedJobID, WaitlistID: row.CommittedWaitlistID}
			return nil
		}
		if row.Status != string(voice.StatusNeedsReview) || row.Version != input.ExpectedVersion || !row.ExpiresAt.Time.After(store.now().UTC()) {
			return voice.ErrConflict
		}
		customerID := input.ExistingCustomerID
		if customerID == "" {
			customerID, err = queries.InsertCustomer(ctx, customerParams(input.Intake.Customer))
			if err != nil {
				return err
			}
			if err = insertAudit(ctx, queries, actor, "customer.created", "customer", customerID, input.RequestID, []string{"created"}); err != nil {
				return err
			}
		} else {
			parsedCustomerID, parseErr := uuid(customerID)
			if parseErr != nil {
				return voice.ErrValidation
			}
			if _, findErr := queries.GetActiveCustomer(ctx, parsedCustomerID); errors.Is(findErr, pgx.ErrNoRows) {
				return voice.ErrValidation
			} else if findErr != nil {
				return findErr
			}
		}
		jobNumber, err := nextJobNumber(ctx, queries)
		if err != nil {
			return err
		}
		params, err := insertJobParams(customerID, jobNumber, input.Intake.Job)
		if err != nil {
			return err
		}
		jobID, err := queries.InsertJob(ctx, params)
		if err != nil {
			return err
		}
		waitlistID, err := queries.InsertWaitlistEntry(ctx, dbgen.InsertWaitlistEntryParams{JobID: mustUUID(jobID), RegionSnapshot: input.Intake.Job.Region})
		if err != nil {
			return err
		}
		if input.Intake.InitialNote != "" {
			if _, err = queries.InsertJobNote(ctx, dbgen.InsertJobNoteParams{JobID: mustUUID(jobID), AuthorUserID: mustUUID(actor.UserID), Body: input.Intake.InitialNote}); err != nil {
				return err
			}
		}
		rows, err := queries.CommitVoiceDraft(ctx, dbgen.CommitVoiceDraftParams{CustomerID: mustUUID(customerID), JobID: mustUUID(jobID), WaitlistID: mustUUID(waitlistID), ID: draftID, OwnerUserID: mustUUID(actor.UserID), ExpectedVersion: input.ExpectedVersion})
		if err != nil {
			return err
		}
		if rows == 0 {
			return voice.ErrConflict
		}
		for _, event := range []struct{ action, kind, id string }{{"job.created", "job", jobID}, {"job.waitlisted", "waitlist_entry", waitlistID}} {
			if err = insertAudit(ctx, queries, actor, event.action, event.kind, event.id, input.RequestID, []string{"created"}); err != nil {
				return err
			}
		}
		metadata, err := json.Marshal(map[string]string{"customer_id": customerID, "job_id": jobID, "waitlist_id": waitlistID})
		if err != nil {
			return err
		}
		if err = queries.InsertAuditEvent(ctx, dbgen.InsertAuditEventParams{ActorType: "user", ActorUserID: actor.UserID, Action: "voice.draft_committed", ObjectType: "voice_draft", ObjectID: input.DraftID, RequestID: input.RequestID, Metadata: metadata}); err != nil {
			return err
		}
		created = customers.CreatedIntake{CustomerID: customerID, JobID: jobID, WaitlistID: waitlistID, JobNumber: jobNumber}
		return nil
	})
	return created, resultErr
}

func (store *VoiceStore) Discard(ctx context.Context, actor auth.Actor, id string) error {
	parsed, err := uuid(id)
	if err != nil {
		return voice.ErrNotFound
	}
	rows, err := store.queries.ExpireVoiceDraft(ctx, dbgen.ExpireVoiceDraftParams{ID: parsed, OwnerUserID: mustUUID(actor.UserID)})
	if err != nil {
		return err
	}
	if rows == 0 {
		return voice.ErrConflict
	}
	return nil
}
func (store *VoiceStore) Cleanup(ctx context.Context) (int64, error) {
	return store.queries.CleanupExpiredVoiceDrafts(ctx)
}
