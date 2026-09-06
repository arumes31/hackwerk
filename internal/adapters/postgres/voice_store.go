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

func (store *VoiceStore) FindRecordingByUploadKey(ctx context.Context, actor auth.Actor, uploadKeyHash []byte) (voice.Draft, bool, error) {
	if len(uploadKeyHash) != 32 {
		return voice.Draft{}, false, voice.ErrValidation
	}
	existing, err := store.queries.GetVoiceDraftByUploadKey(ctx, dbgen.GetVoiceDraftByUploadKeyParams{
		OwnerUserID: mustUUID(actor.UserID), UploadKeyHash: uploadKeyHash,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return voice.Draft{}, false, nil
	}
	if err != nil {
		return voice.Draft{}, false, err
	}
	return voice.Draft{
		ID: existing.DraftID, OwnerUserID: actor.UserID, Status: voice.Status(existing.Status),
		Version: existing.Version, ExpiresAt: existing.ExpiresAt.Time.UTC(),
	}, true, nil
}

func (store *VoiceStore) CreateRecording(ctx context.Context, actor auth.Actor, uploadKeyHash, audio []byte, contentType string, metadata voice.Metadata, draftExpiresAt, recordingExpiresAt time.Time) (result voice.Draft, resultErr error) {
	if len(uploadKeyHash) != 32 || len(audio) == 0 || len(audio) > 15<<20 || metadata.Duration <= 0 || metadata.Duration > 5*time.Minute {
		return voice.Draft{}, voice.ErrValidation
	}
	// #nosec G115 -- the validation above bounds the payload far below MaxInt32.
	byteSize := int32(len(audio))
	// #nosec G115 -- the validation above bounds duration to at most 300,000 ms.
	durationMS := int32(metadata.Duration / time.Millisecond)
	ownerID := mustUUID(actor.UserID)
	resultErr = withQueries(ctx, store.pool, func(queries *dbgen.Queries) error {
		if err := queries.LockVoiceUploadKey(ctx, dbgen.LockVoiceUploadKeyParams{OwnerUserID: actor.UserID, UploadKeyHash: uploadKeyHash}); err != nil {
			return err
		}
		existing, err := queries.GetVoiceDraftByUploadKey(ctx, dbgen.GetVoiceDraftByUploadKeyParams{OwnerUserID: ownerID, UploadKeyHash: uploadKeyHash})
		if err == nil {
			result = voice.Draft{ID: existing.DraftID, OwnerUserID: actor.UserID, Status: voice.Status(existing.Status), Version: existing.Version, ExpiresAt: existing.ExpiresAt.Time.UTC()}
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		draftID, err := queries.InsertVoiceRecording(ctx, dbgen.InsertVoiceRecordingParams{
			OwnerUserID: ownerID, UploadKeyHash: uploadKeyHash, ContentType: contentType, AudioBytes: audio,
			ByteSize: byteSize, DurationMs: durationMS,
			RecordedAt: timestamp(metadata.RecordedAt.UTC()), RecordingExpiresAt: timestamp(recordingExpiresAt.UTC()),
			DraftExpiresAt: timestamp(draftExpiresAt.UTC()),
		})
		if err != nil {
			return err
		}
		result = voice.Draft{ID: draftID, OwnerUserID: actor.UserID, Status: voice.StatusRecorded, Version: 1, ExpiresAt: draftExpiresAt.UTC()}
		return nil
	})
	return result, resultErr
}

func (store *VoiceStore) RetryRecording(ctx context.Context, actor auth.Actor, id string, expectedVersion int32, now time.Time) (voice.Draft, error) {
	parsedID, err := uuid(id)
	if err != nil {
		return voice.Draft{}, voice.ErrNotFound
	}
	row, err := store.queries.RetryFailedVoiceRecording(ctx, dbgen.RetryFailedVoiceRecordingParams{
		NowUtc: timestamp(now.UTC()), DraftID: parsedID, OwnerUserID: mustUUID(actor.UserID), ExpectedVersion: expectedVersion,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return voice.Draft{}, voice.ErrConflict
	}
	if err != nil {
		return voice.Draft{}, err
	}
	return voice.Draft{
		ID: row.DraftID, OwnerUserID: row.DraftOwnerUserID, Status: voice.Status(row.Status), RetryCount: int32(row.RetryCount),
		ManualRetryCount: int32(row.ManualRetryCount), Version: row.Version, CreatedAt: row.CreatedAt.Time.UTC(), UpdatedAt: row.UpdatedAt.Time.UTC(), ExpiresAt: row.ExpiresAt.Time.UTC(),
	}, nil
}

func (store *VoiceStore) ClaimRecording(ctx context.Context, workerID string, now, leaseUntil time.Time) (voice.ClaimedRecording, bool, error) {
	row, err := store.queries.ClaimVoiceRecording(ctx, dbgen.ClaimVoiceRecordingParams{
		NowUtc: timestamp(now.UTC()), WorkerID: &workerID, LeaseUntil: timestamp(leaseUntil.UTC()),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return voice.ClaimedRecording{}, false, nil
	}
	if err != nil {
		return voice.ClaimedRecording{}, false, err
	}
	return voice.ClaimedRecording{
		RecordingID: row.RecordingID, DraftID: row.ClaimedDraftID, OwnerUserID: row.ClaimedOwnerUserID,
		ContentType: row.ContentType, AudioBytes: row.AudioBytes, ByteSize: int(row.ByteSize),
		Duration: time.Duration(row.DurationMs) * time.Millisecond, RecordedAt: row.RecordedAt.Time.UTC(),
		Attempt: row.AttemptCount, MaxAttempts: row.MaxAttempts,
	}, true, nil
}

func (store *VoiceStore) CompleteRecording(ctx context.Context, workerID string, job voice.ClaimedRecording, transcript voice.Transcript, fields voice.Fields, warnings []string, confidence float64, parserVersion string, now time.Time) error {
	payload, err := json.Marshal(fields)
	if err != nil {
		return err
	}
	var numeric pgtype.Numeric
	if err = numeric.Scan(strconv.FormatFloat(confidence, 'f', 3, 64)); err != nil {
		return err
	}
	rows, err := store.queries.CompleteClaimedVoiceRecording(ctx, dbgen.CompleteClaimedVoiceRecordingParams{
		Transcript: &transcript.Text, ExtractedFields: payload, Warnings: warnings, OverallConfidence: numeric,
		ProviderName: transcript.Provider, ProviderVersion: transcript.Version, ParserVersion: parserVersion,
		NowUtc: timestamp(now.UTC()), RecordingID: mustUUID(job.RecordingID), WorkerID: &workerID,
	})
	if err != nil {
		return err
	}
	if rows != 1 {
		return voice.ErrConflict
	}
	return nil
}

func (store *VoiceStore) FailRecording(ctx context.Context, workerID string, job voice.ClaimedRecording, code string, retry bool, now, availableAt time.Time) error {
	_, err := store.queries.FailClaimedVoiceRecording(ctx, dbgen.FailClaimedVoiceRecordingParams{
		Retry: retry, FailureCode: code, NowUtc: timestamp(now.UTC()), AvailableAt: timestamp(availableAt.UTC()),
		RecordingID: mustUUID(job.RecordingID), WorkerID: &workerID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return voice.ErrConflict
	}
	return err
}

func (store *VoiceStore) ListRecordings(ctx context.Context, limit, offset int32) ([]voice.Recording, error) {
	rows, err := store.queries.ListVoiceRecordingsForAdmin(ctx, dbgen.ListVoiceRecordingsForAdminParams{ResultLimit: limit, ResultOffset: offset})
	if err != nil {
		return nil, err
	}
	result := make([]voice.Recording, 0, len(rows))
	for _, row := range rows {
		result = append(result, voice.Recording{
			ID: row.RecordingID, DraftID: row.RecordingDraftID, ContentType: row.ContentType,
			OwnerDisplayName: row.OwnerDisplayName, ByteSize: int(row.ByteSize),
			Duration: time.Duration(row.DurationMs) * time.Millisecond, DraftStatus: voice.Status(row.Status),
			RecordedAt: row.RecordedAt.Time.UTC(), CreatedAt: row.CreatedAt.Time.UTC(), ExpiresAt: row.ExpiresAt.Time.UTC(),
		})
	}
	return result, nil
}

func (store *VoiceStore) GetRecordingAudio(ctx context.Context, id string) (voice.RecordingAudio, error) {
	parsed, err := uuid(id)
	if err != nil {
		return voice.RecordingAudio{}, voice.ErrNotFound
	}
	row, err := store.queries.GetVoiceRecordingAudio(ctx, parsed)
	if errors.Is(err, pgx.ErrNoRows) {
		return voice.RecordingAudio{}, voice.ErrNotFound
	}
	if err != nil {
		return voice.RecordingAudio{}, err
	}
	return voice.RecordingAudio{
		ID: row.ID, ContentType: row.ContentType, Bytes: row.AudioBytes, ByteSize: int(row.ByteSize),
		Duration:   time.Duration(row.DurationMs) * time.Millisecond,
		RecordedAt: row.RecordedAt.Time.UTC(), ExpiresAt: row.ExpiresAt.Time.UTC(),
	}, nil
}

func (store *VoiceStore) CleanupRecordings(ctx context.Context, now time.Time) (int64, error) {
	return store.queries.CleanupExpiredVoiceRecordings(ctx, timestamp(now.UTC()))
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
	return voice.Draft{ID: row.ID, OwnerUserID: row.OwnerUserID, Status: status, Transcript: row.Transcript, Fields: fields, Warnings: row.Warnings, OverallConfidence: confidence, ProviderName: row.ProviderName, ProviderVersion: row.ProviderVersion, ParserVersion: row.ParserVersion, FailureCode: row.FailureCode, RetryCount: int32(row.RetryCount), ManualRetryCount: row.ManualRetryCount, Version: row.Version, Committed: customers.CreatedIntake{CustomerID: row.CommittedCustomerID, JobID: row.CommittedJobID, WaitlistID: row.CommittedWaitlistID, JobNumber: row.CommittedJobNumber}, CreatedAt: row.CreatedAt.Time.UTC(), UpdatedAt: row.UpdatedAt.Time.UTC(), ExpiresAt: row.ExpiresAt.Time.UTC()}, nil
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
			created = customers.CreatedIntake{CustomerID: row.CommittedCustomerID, JobID: row.CommittedJobID, WaitlistID: row.CommittedWaitlistID, JobNumber: row.CommittedJobNumber}
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
