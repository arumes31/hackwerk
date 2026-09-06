//go:build integration

package integration_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"example.invalid/hackplan/internal/adapters/postgres"
	"example.invalid/hackplan/internal/adapters/postgres/migrate"
	"example.invalid/hackplan/internal/auth"
	"example.invalid/hackplan/internal/config"
	"example.invalid/hackplan/internal/customers"
	"example.invalid/hackplan/internal/voice"
	"github.com/jackc/pgx/v5/pgxpool"
)

type integrationTimeoutTranscriber struct{}

func (integrationTimeoutTranscriber) Transcribe(ctx context.Context, _ voice.Audio, _ string, _ voice.Metadata) (voice.Transcript, error) {
	<-ctx.Done()
	return voice.Transcript{}, ctx.Err()
}

func TestVoiceDraftReviewCommitIsAtomicPrivateAndIdempotent(t *testing.T) {
	ctx, pool, service, driver, other := voiceFixture(t)
	draft, err := service.Process(ctx, driver, voice.Audio{Reader: bytes.NewReader([]byte("fixture audio")), Size: 13, ContentType: "audio/webm"}, voice.Metadata{RecordedAt: time.Date(2026, 8, 25, 10, 0, 0, 0, time.UTC), Duration: 3 * time.Second})
	if err != nil || draft.Status != voice.StatusNeedsReview {
		t.Fatalf("Process() draft/error=%#v/%v", draft, err)
	}
	for _, table := range []string{"customers", "jobs", "waitlist_entries", "appointments", "outbox_events"} {
		assertVoiceCount(t, ctx, pool, table, 0)
	}
	if _, err = service.Get(ctx, other, draft.ID); !errors.Is(err, voice.ErrNotFound) {
		t.Fatalf("foreign owner error=%v", err)
	}
	input := voice.IntakeFromFields(draft.Fields)
	created, err := service.Commit(ctx, driver, voice.CommitInput{DraftID: draft.ID, ExpectedVersion: draft.Version, Reviewed: true, Intake: input, RequestID: "voice-commit"})
	if err != nil {
		t.Fatal(err)
	}
	if created.CustomerID == "" || created.JobID == "" || created.WaitlistID == "" {
		t.Fatalf("created=%#v", created)
	}
	for _, table := range []string{"customers", "jobs", "waitlist_entries"} {
		assertVoiceCount(t, ctx, pool, table, 1)
	}
	for _, table := range []string{"appointments", "outbox_events", "notifications"} {
		assertVoiceCount(t, ctx, pool, table, 0)
	}
	var retainedTranscript, retainedFields string
	if err = pool.QueryRow(ctx, "SELECT COALESCE(transcript,''),extracted_fields::text FROM voice_drafts WHERE id=$1", draft.ID).Scan(&retainedTranscript, &retainedFields); err != nil {
		t.Fatal(err)
	}
	if retainedTranscript != "" || retainedFields != "{}" {
		t.Fatalf("committed draft retained PII: transcript=%q fields=%q", retainedTranscript, retainedFields)
	}
	again, err := service.Commit(ctx, driver, voice.CommitInput{DraftID: draft.ID, ExpectedVersion: draft.Version, Reviewed: true, Intake: input, RequestID: "voice-commit-retry"})
	if err != nil || again.CustomerID != created.CustomerID || again.JobID != created.JobID || again.JobNumber != created.JobNumber {
		t.Fatalf("idempotent commit=%#v/%v", again, err)
	}
	for _, table := range []string{"customers", "jobs", "waitlist_entries"} {
		assertVoiceCount(t, ctx, pool, table, 1)
	}
	var metadata string
	if err = pool.QueryRow(ctx, "SELECT metadata::text FROM audit_events WHERE action='voice.draft_committed'").Scan(&metadata); err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"Franz", "Huber", "0664", "Unterneukirchen", "Kubikmeter", "fixture audio"} {
		if strings.Contains(metadata, forbidden) {
			t.Fatalf("audit contains PII %q: %s", forbidden, metadata)
		}
	}
}

func TestVoiceDirectCommitAndDuplicateSelectionAreControlled(t *testing.T) {
	ctx, pool, service, driver, _ := voiceFixture(t)
	draft, err := service.Process(ctx, driver, voice.Audio{Reader: bytes.NewReader([]byte("audio")), Size: 5}, voice.Metadata{RecordedAt: time.Now(), Duration: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	invalid := voice.IntakeFromFields(draft.Fields)
	invalid.Job.VolumeM3 = ""
	if _, err = service.Commit(ctx, driver, voice.CommitInput{DraftID: draft.ID, ExpectedVersion: draft.Version, Reviewed: false, Intake: invalid}); !errors.Is(err, voice.ErrValidation) {
		t.Fatalf("unreviewed error=%v", err)
	}
	for _, table := range []string{"customers", "jobs", "waitlist_entries", "appointments"} {
		assertVoiceCount(t, ctx, pool, table, 0)
	}
	input := voice.IntakeFromFields(draft.Fields)
	var duplicateID string
	if err = pool.QueryRow(ctx, `INSERT INTO customers(first_name,last_name,country_code,phone_raw,phone_normalized,notification_preference) VALUES('Bestehend','Kunde','AT',$1,$2,'none') RETURNING id::text`, input.Customer.PhoneRaw, customers.NormalizePhone(input.Customer.PhoneRaw)).Scan(&duplicateID); err != nil {
		t.Fatal(err)
	}
	if _, err = service.Commit(ctx, driver, voice.CommitInput{DraftID: draft.ID, ExpectedVersion: draft.Version, Reviewed: true, Intake: input}); !errors.Is(err, voice.ErrValidation) {
		t.Fatalf("missing duplicate decision error=%v", err)
	}
	created, err := service.Commit(ctx, driver, voice.CommitInput{DraftID: draft.ID, ExpectedVersion: draft.Version, Reviewed: true, DuplicateReviewed: true, ExistingCustomerID: duplicateID, Intake: input})
	if err != nil {
		t.Fatal(err)
	}
	if created.CustomerID != duplicateID {
		t.Fatalf("customer=%s want=%s", created.CustomerID, duplicateID)
	}
	assertVoiceCount(t, ctx, pool, "customers", 1)
	assertVoiceCount(t, ctx, pool, "jobs", 1)
	assertVoiceCount(t, ctx, pool, "appointments", 0)
}

func TestVoiceConcurrentCommitCreatesOneWorkflowAndCleanupClearsPII(t *testing.T) {
	ctx, pool, service, driver, _ := voiceFixture(t)
	draft, err := service.Process(ctx, driver, voice.Audio{Reader: bytes.NewReader([]byte("audio")), Size: 5}, voice.Metadata{RecordedAt: time.Now(), Duration: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	input := voice.IntakeFromFields(draft.Fields)
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, commitErr := service.Commit(context.Background(), driver, voice.CommitInput{DraftID: draft.ID, ExpectedVersion: draft.Version, Reviewed: true, Intake: input})
			errs <- commitErr
		}()
	}
	wg.Wait()
	close(errs)
	for commitErr := range errs {
		if commitErr != nil {
			t.Fatalf("concurrent commit error=%v", commitErr)
		}
	}
	assertVoiceCount(t, ctx, pool, "customers", 1)
	assertVoiceCount(t, ctx, pool, "jobs", 1)
	assertVoiceCount(t, ctx, pool, "waitlist_entries", 1)
	assertVoiceCount(t, ctx, pool, "appointments", 0)
	second, err := service.Process(ctx, driver, voice.Audio{Reader: bytes.NewReader([]byte("audio")), Size: 5}, voice.Metadata{RecordedAt: time.Now(), Duration: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, "UPDATE voice_drafts SET created_at=now()-interval '1 hour', expires_at=now()-interval '1 second' WHERE id=$1", second.ID); err != nil {
		t.Fatal(err)
	}
	count, err := service.Cleanup(ctx)
	if err != nil || count != 1 {
		t.Fatalf("cleanup count/error=%d/%v", count, err)
	}
	var status, transcript string
	if err = pool.QueryRow(ctx, "SELECT status,COALESCE(transcript,'') FROM voice_drafts WHERE id=$1", second.ID).Scan(&status, &transcript); err != nil {
		t.Fatal(err)
	}
	if status != "expired" || transcript != "" {
		t.Fatalf("cleanup status/transcript=%q/%q", status, transcript)
	}
}

func TestVoiceProviderTimeoutPersistsOnlyFailureCode(t *testing.T) {
	ctx, pool, _, driver, _ := voiceFixture(t)
	location, _ := time.LoadLocation("Europe/Vienna")
	service, err := voice.New(postgres.NewVoiceStore(pool), integrationTimeoutTranscriber{}, voice.RuleExtractor{}, voice.Config{Enabled: true, Retention: time.Hour, RateLimitPerMinute: 10, ConcurrentPerUser: 1, Timezone: location}, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	timeoutCtx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()
	draft, err := service.Process(timeoutCtx, driver, voice.Audio{Reader: bytes.NewReader([]byte("private audio")), Size: 13}, voice.Metadata{RecordedAt: time.Now(), Duration: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if draft.Status != voice.StatusFailed || draft.Transcript != "" || draft.FailureCode != "transcription_failed" {
		t.Fatalf("draft=%#v", draft)
	}
	var transcript, fields string
	if err = pool.QueryRow(ctx, "SELECT COALESCE(transcript,''),extracted_fields::text FROM voice_drafts WHERE id=$1", draft.ID).Scan(&transcript, &fields); err != nil {
		t.Fatal(err)
	}
	if transcript != "" || fields != "{}" {
		t.Fatalf("persisted transcript/fields=%q/%q", transcript, fields)
	}
}

func TestVoiceRecordingQueueClaimsOnceAndDeletesAfterThirtyDays(t *testing.T) {
	ctx, pool, service, driver, _ := voiceFixture(t)
	for _, payload := range []string{"audio-one", "audio-two"} {
		_, err := service.EnqueuePrepared(ctx, driver, "integration-upload-"+payload, func() (voice.Audio, voice.Metadata, error) {
			return voice.Audio{Reader: bytes.NewReader([]byte(payload)), Size: int64(len(payload)), ContentType: "audio/webm"}, voice.Metadata{RecordedAt: time.Now().UTC(), Duration: time.Second}, nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}

	stores := []*postgres.VoiceStore{postgres.NewVoiceStore(pool), postgres.NewVoiceStore(pool)}
	claimed := make(chan voice.ClaimedRecording, len(stores))
	errs := make(chan error, len(stores))
	var wg sync.WaitGroup
	now := time.Now().UTC()
	for index, store := range stores {
		wg.Add(1)
		go func(index int, store *postgres.VoiceStore) {
			defer wg.Done()
			job, found, err := store.ClaimRecording(context.Background(), fmt.Sprintf("voice-worker-%d", index), now, now.Add(time.Minute))
			if err != nil {
				errs <- err
				return
			}
			if !found {
				errs <- errors.New("voice recording was not claimed")
				return
			}
			claimed <- job
		}(index, store)
	}
	wg.Wait()
	close(claimed)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	seen := map[string]bool{}
	for job := range claimed {
		if seen[job.RecordingID] {
			t.Fatalf("recording %s claimed twice", job.RecordingID)
		}
		seen[job.RecordingID] = true
	}
	if len(seen) != 2 {
		t.Fatalf("claimed recordings = %d, want 2", len(seen))
	}

	admin := auth.Actor{UserID: driver.UserID, Role: auth.RoleAdmin}
	recordings, err := service.ListRecordings(ctx, admin, 100, 0)
	if err != nil || len(recordings) != 2 {
		t.Fatalf("admin recordings/error = %#v/%v", recordings, err)
	}
	if _, err = service.ListRecordings(ctx, driver, 100, 0); !errors.Is(err, auth.ErrForbidden) {
		t.Fatalf("driver recording list error = %v", err)
	}
	var retentionSeconds int64
	if err = pool.QueryRow(ctx, "SELECT EXTRACT(EPOCH FROM (expires_at-created_at))::bigint FROM voice_recordings LIMIT 1").Scan(&retentionSeconds); err != nil {
		t.Fatal(err)
	}
	wantRetentionSeconds := int64((30 * 24 * time.Hour) / time.Second)
	if retentionSeconds < wantRetentionSeconds-5 || retentionSeconds > wantRetentionSeconds {
		t.Fatalf("recording retention seconds = %d", retentionSeconds)
	}
	firstID := recordings[0].ID
	if _, err = pool.Exec(ctx, "UPDATE voice_recordings SET created_at=now()-interval '31 days', expires_at=now()-interval '1 second' WHERE id=$1", firstID); err != nil {
		t.Fatal(err)
	}
	if count, err := service.Cleanup(ctx); err != nil || count != 1 {
		t.Fatalf("recording cleanup count/error = %d/%v", count, err)
	}
	if _, err = service.RecordingAudio(ctx, admin, firstID); !errors.Is(err, voice.ErrNotFound) {
		t.Fatalf("expired recording playback error = %v", err)
	}
}

func TestVoiceUploadIdempotencyIsOwnerScopedAndManualRetryIsBounded(t *testing.T) {
	ctx, pool, _, owner, other := voiceFixture(t)
	location, _ := time.LoadLocation("Europe/Vienna")
	service, err := voice.New(postgres.NewVoiceStore(pool), voice.FakeTranscriber{Text: "fixture"}, voice.RuleExtractor{}, voice.Config{
		Enabled: true, Retention: time.Hour, RateLimitPerMinute: 1, ConcurrentPerUser: 1, Timezone: location,
	}, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	preparations := 0
	prepare := func() (voice.Audio, voice.Metadata, error) {
		preparations++
		payload := []byte("same-audio")
		return voice.Audio{Reader: bytes.NewReader(payload), Size: int64(len(payload)), ContentType: "audio/webm"}, voice.Metadata{RecordedAt: time.Now().UTC(), Duration: time.Second}, nil
	}
	first, err := service.EnqueuePrepared(ctx, owner, "browser-recording-key-0001", prepare)
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := service.EnqueuePrepared(ctx, owner, "browser-recording-key-0001", prepare)
	if err != nil || replayed.ID != first.ID {
		t.Fatalf("same-owner replay = %#v, %v; first=%#v", replayed, err, first)
	}
	if preparations != 1 {
		t.Fatalf("same-owner replay prepared audio %d times, want once", preparations)
	}
	foreign, err := service.EnqueuePrepared(ctx, other, "browser-recording-key-0001", prepare)
	if err != nil || foreign.ID == first.ID {
		t.Fatalf("other-owner upload = %#v, %v; first=%#v", foreign, err, first)
	}
	var recordingCount, hashLength int
	if err := pool.QueryRow(ctx, "SELECT count(*), min(octet_length(upload_key_hash)) FROM voice_recordings").Scan(&recordingCount, &hashLength); err != nil {
		t.Fatal(err)
	}
	if recordingCount != 2 || hashLength != 32 {
		t.Fatalf("recordings/hash length = %d/%d", recordingCount, hashLength)
	}

	if _, err := pool.Exec(ctx, "UPDATE voice_drafts SET status='failed', version=4 WHERE id=$1", first.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "UPDATE voice_recordings SET attempt_count=max_attempts WHERE draft_id=$1", first.ID); err != nil {
		t.Fatal(err)
	}
	retried, err := service.RetryTranscription(ctx, owner, first.ID, 4)
	if err != nil || retried.Status != voice.StatusRecorded || retried.ManualRetryCount != 1 || retried.Version != 5 {
		t.Fatalf("manual retry = %#v, %v", retried, err)
	}
	if _, err := pool.Exec(ctx, "UPDATE voice_drafts SET status='failed', version=6 WHERE id=$1", first.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.RetryTranscription(ctx, owner, first.ID, 6); !errors.Is(err, voice.ErrConflict) {
		t.Fatalf("second manual retry error = %v, want conflict", err)
	}
}

func voiceFixture(t *testing.T) (context.Context, *pgxpool.Pool, *voice.Service, auth.Actor, auth.Actor) {
	t.Helper()
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Fatal("TEST_DATABASE_URL is required")
	}
	ctx := context.Background()
	if err := migrate.Run(ctx, databaseURL, migrate.DirectionUp, os.Stdout); err != nil {
		t.Fatal(err)
	}
	pool, err := postgres.Open(ctx, config.Database{URL: databaseURL, MaxConnections: 10, MinConnections: 0, ConnectTimeout: 5 * time.Second, ReadinessTimeout: 2 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if _, err = pool.Exec(ctx, "TRUNCATE outbox_events, customers, users RESTART IDENTITY CASCADE"); err != nil {
		t.Fatal(err)
	}
	var driverID, otherID string
	if err = pool.QueryRow(ctx, "INSERT INTO users(username,display_name,role,password_hash,must_change_password) VALUES('voice-driver','Voice Driver','driver','test',false) RETURNING id::text").Scan(&driverID); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, "INSERT INTO users(username,display_name,role,password_hash,must_change_password) VALUES('voice-other','Other Driver','driver','test',false) RETURNING id::text").Scan(&otherID); err != nil {
		t.Fatal(err)
	}
	location, _ := time.LoadLocation("Europe/Vienna")
	service, err := voice.New(postgres.NewVoiceStore(pool), voice.FakeTranscriber{Text: "Franz Huber, Unterneukirchen 15, Telefonnummer 0664 1234567, ungefähr 80 Kubikmeter Holz, ungefähr drei Stunden Hackzeit, möglichst Anfang September"}, voice.RuleExtractor{}, voice.Config{Enabled: true, Retention: time.Hour, RateLimitPerMinute: 20, ConcurrentPerUser: 4, Timezone: location}, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	return ctx, pool, service, auth.Actor{UserID: driverID, Role: auth.RoleDriver}, auth.Actor{UserID: otherID, Role: auth.RoleDriver}
}
func assertVoiceCount(t *testing.T, ctx context.Context, pool *pgxpool.Pool, table string, want int) {
	t.Helper()
	var count int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM "+table).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != want {
		t.Fatalf("%s count=%d want=%d", table, count, want)
	}
}
