package voice

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"example.invalid/hackplan/internal/auth"
	"example.invalid/hackplan/internal/customers"
)

type voiceStoreStub struct {
	draft                              Draft
	creates, completes, fails, commits int
	duplicates                         []customers.Duplicate
	recording                          ClaimedRecording
	claimed                            bool
}

type timeoutTranscriber struct{}

func (timeoutTranscriber) Transcribe(ctx context.Context, _ Audio, _ string, _ Metadata) (Transcript, error) {
	<-ctx.Done()
	return Transcript{}, ctx.Err()
}

func (s *voiceStoreStub) Create(_ context.Context, actor auth.Actor, expires time.Time) (Draft, error) {
	s.creates++
	s.draft = Draft{ID: "draft", OwnerUserID: actor.UserID, Status: StatusTranscribing, Version: 1, ExpiresAt: expires}
	return s.draft, nil
}
func (s *voiceStoreStub) Complete(_ context.Context, _ auth.Actor, _ string, tr Transcript, fields Fields, warnings []string, confidence float64, parser string) error {
	s.completes++
	s.draft.Status = StatusNeedsReview
	s.draft.Version = 2
	s.draft.Transcript = tr.Text
	s.draft.Fields = fields
	s.draft.Warnings = warnings
	s.draft.OverallConfidence = confidence
	s.draft.ParserVersion = parser
	return nil
}
func (s *voiceStoreStub) Fail(context.Context, auth.Actor, string, string) error {
	s.fails++
	s.draft.Status = StatusFailed
	s.draft.Version = 2
	return nil
}
func (s *voiceStoreStub) Get(context.Context, auth.Actor, string) (Draft, error) { return s.draft, nil }
func (s *voiceStoreStub) FindDuplicates(context.Context, customers.CustomerInput) ([]customers.Duplicate, error) {
	return s.duplicates, nil
}
func (s *voiceStoreStub) Commit(_ context.Context, _ auth.Actor, _ CommitInput) (customers.CreatedIntake, error) {
	s.commits++
	return customers.CreatedIntake{CustomerID: "customer", JobID: "job", WaitlistID: "waitlist"}, nil
}
func (s *voiceStoreStub) Discard(context.Context, auth.Actor, string) error { return nil }
func (s *voiceStoreStub) Cleanup(context.Context) (int64, error)            { return 0, nil }
func (s *voiceStoreStub) CreateRecording(_ context.Context, actor auth.Actor, audio []byte, contentType string, metadata Metadata, expires, _ time.Time) (Draft, error) {
	s.creates++
	s.draft = Draft{ID: "draft", OwnerUserID: actor.UserID, Status: StatusRecorded, Version: 1, ExpiresAt: expires}
	s.recording = ClaimedRecording{RecordingID: "recording", DraftID: s.draft.ID, OwnerUserID: actor.UserID, ContentType: contentType, AudioBytes: append([]byte(nil), audio...), ByteSize: len(audio), Duration: metadata.Duration, RecordedAt: metadata.RecordedAt, Attempt: 1, MaxAttempts: 3}
	return s.draft, nil
}
func (s *voiceStoreStub) ClaimRecording(context.Context, string, time.Time, time.Time) (ClaimedRecording, bool, error) {
	if s.claimed || s.recording.RecordingID == "" {
		return ClaimedRecording{}, false, nil
	}
	s.claimed = true
	s.draft.Status = StatusTranscribing
	return s.recording, true, nil
}
func (s *voiceStoreStub) CompleteRecording(_ context.Context, _ string, _ ClaimedRecording, transcript Transcript, fields Fields, warnings []string, confidence float64, parser string, _ time.Time) error {
	s.completes++
	s.draft.Status = StatusNeedsReview
	s.draft.Transcript, s.draft.Fields, s.draft.Warnings = transcript.Text, fields, warnings
	s.draft.OverallConfidence, s.draft.ParserVersion = confidence, parser
	return nil
}
func (s *voiceStoreStub) FailRecording(context.Context, string, ClaimedRecording, string, bool, time.Time, time.Time) error {
	s.fails++
	s.draft.Status = StatusFailed
	return nil
}
func (s *voiceStoreStub) ListRecordings(context.Context, int32, int32) ([]Recording, error) {
	return []Recording{{ID: "recording"}}, nil
}
func (s *voiceStoreStub) GetRecordingAudio(context.Context, string) (RecordingAudio, error) {
	return RecordingAudio{ID: "recording", Bytes: []byte("audio"), ByteSize: 5, ContentType: "audio/webm"}, nil
}
func (s *voiceStoreStub) CleanupRecordings(context.Context, time.Time) (int64, error) { return 0, nil }

func testVoiceService(t *testing.T, store Store, transcriber Transcriber) *Service {
	t.Helper()
	location, _ := time.LoadLocation("Europe/Vienna")
	service, err := New(store, transcriber, RuleExtractor{}, Config{Enabled: true, Retention: time.Hour, RateLimitPerMinute: 10, ConcurrentPerUser: 1, Timezone: location}, func() time.Time { return time.Date(2026, 8, 25, 10, 0, 0, 0, location) })
	if err != nil {
		t.Fatal(err)
	}
	return service
}
func voiceActor(role auth.Role) auth.Actor {
	return auth.Actor{UserID: "00000000-0000-0000-0000-000000000001", Role: role}
}

func TestProcessOnlyCreatesDraft(t *testing.T) {
	store := &voiceStoreStub{}
	service := testVoiceService(t, store, FakeTranscriber{Text: "Franz Huber, Unterneukirchen 15, 80 m³, drei Stunden Hackzeit"})
	draft, err := service.Process(context.Background(), voiceActor(auth.RoleDriver), Audio{Reader: bytes.NewReader([]byte("audio")), Size: 5}, Metadata{RecordedAt: time.Now(), Duration: time.Second})
	if err != nil || draft.Status != StatusNeedsReview || store.creates != 1 || store.completes != 1 || store.commits != 0 {
		t.Fatalf("draft/store/err = %#v/%#v/%v", draft, store, err)
	}
}

func TestEnqueueReturnsBeforeTranscriptionAndWorkerCompletesDraft(t *testing.T) {
	store := &voiceStoreStub{}
	transcriber := &countingVoiceTranscriber{text: "Franz Huber, 80 m³"}
	service := testVoiceService(t, store, transcriber)
	recordedAt := time.Date(2026, 8, 25, 10, 0, 0, 0, time.UTC)
	draft, err := service.EnqueuePrepared(t.Context(), voiceActor(auth.RoleDriver), func() (Audio, Metadata, error) {
		return Audio{Reader: bytes.NewReader([]byte("audio")), Size: 5, ContentType: "audio/webm"}, Metadata{RecordedAt: recordedAt, Duration: time.Second}, nil
	})
	if err != nil || draft.Status != StatusRecorded || transcriber.calls != 0 || store.creates != 1 {
		t.Fatalf("enqueue draft/calls/creates/error = %#v/%d/%d/%v", draft, transcriber.calls, store.creates, err)
	}
	processed, err := service.ProcessNext(t.Context(), "worker-1", 2*time.Minute)
	if err != nil || !processed || transcriber.calls != 1 || store.completes != 1 || store.draft.Status != StatusNeedsReview {
		t.Fatalf("process result/calls/completes/draft/error = %v/%d/%d/%#v/%v", processed, transcriber.calls, store.completes, store.draft, err)
	}
}

type countingVoiceTranscriber struct {
	text  string
	calls int
}

type drainingRecordingStore struct {
	*voiceStoreStub
	batches []int64
	calls   int
}

func (store *drainingRecordingStore) CleanupRecordings(context.Context, time.Time) (int64, error) {
	store.calls++
	if len(store.batches) == 0 {
		return 0, nil
	}
	result := store.batches[0]
	store.batches = store.batches[1:]
	return result, nil
}

func TestCleanupDrainsAllExpiredRecordingBatches(t *testing.T) {
	store := &drainingRecordingStore{voiceStoreStub: &voiceStoreStub{}, batches: []int64{100, 37, 0}}
	service := testVoiceService(t, store, FakeTranscriber{Text: "fixture"})
	count, err := service.Cleanup(t.Context())
	if err != nil || count != 137 || store.calls != 3 {
		t.Fatalf("Cleanup() = %d, %v; calls = %d", count, err, store.calls)
	}
}

func (transcriber *countingVoiceTranscriber) Transcribe(context.Context, Audio, string, Metadata) (Transcript, error) {
	transcriber.calls++
	return Transcript{Text: transcriber.text, Provider: "test", Version: "1", Confidence: .9}, nil
}

func TestOriginalRecordingAccessIsAdminOnly(t *testing.T) {
	service := testVoiceService(t, &voiceStoreStub{}, FakeTranscriber{Text: "fixture"})
	if _, err := service.ListRecordings(t.Context(), voiceActor(auth.RoleDriver), 100, 0); !errors.Is(err, auth.ErrForbidden) {
		t.Fatalf("driver list error = %v", err)
	}
	if _, err := service.RecordingAudio(t.Context(), voiceActor(auth.RoleDriver), "recording"); !errors.Is(err, auth.ErrForbidden) {
		t.Fatalf("driver playback error = %v", err)
	}
	admin := voiceActor(auth.RoleAdmin)
	if recordings, err := service.ListRecordings(t.Context(), admin, 100, 0); err != nil || len(recordings) != 1 {
		t.Fatalf("admin recordings/error = %#v/%v", recordings, err)
	}
	if audio, err := service.RecordingAudio(t.Context(), admin, "recording"); err != nil || string(audio.Bytes) != "audio" {
		t.Fatalf("admin audio/error = %#v/%v", audio, err)
	}
}

func TestProcessPreparedRunsPreparationOnlyAfterAdmission(t *testing.T) {
	store := &voiceStoreStub{}
	service := testVoiceService(t, store, FakeTranscriber{Text: "fixture"})
	actor := voiceActor(auth.RoleDriver)
	release, ok := service.limiter.acquire(actor.UserID, service.now())
	if !ok {
		t.Fatal("failed to reserve the fixture admission slot")
	}
	defer release()
	preparations := 0
	_, err := service.ProcessPrepared(context.Background(), actor, func() (Audio, Metadata, error) {
		preparations++
		return Audio{Reader: bytes.NewReader([]byte("audio")), Size: 5}, Metadata{}, nil
	})
	if !errors.Is(err, ErrRateLimit) || preparations != 0 || store.creates != 0 {
		t.Fatalf("error/preparations/creates = %v/%d/%d", err, preparations, store.creates)
	}
}

func TestProviderFailureIsRedactedStatus(t *testing.T) {
	store := &voiceStoreStub{}
	service := testVoiceService(t, store, DisabledTranscriber{})
	draft, err := service.Process(context.Background(), voiceActor(auth.RoleDriver), Audio{Reader: bytes.NewReader([]byte("secret audio")), Size: 12}, Metadata{RecordedAt: time.Now(), Duration: time.Second})
	if err != nil || draft.Status != StatusFailed || store.fails != 1 || draft.Transcript != "" {
		t.Fatalf("draft/err = %#v/%v", draft, err)
	}
}

func TestProviderTimeoutStillPersistsRedactedFailure(t *testing.T) {
	store := &voiceStoreStub{}
	service := testVoiceService(t, store, timeoutTranscriber{})
	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	draft, err := service.Process(ctx, voiceActor(auth.RoleDriver), Audio{Reader: bytes.NewReader([]byte("private audio")), Size: 13}, Metadata{RecordedAt: time.Now(), Duration: time.Second})
	if err != nil || draft.Status != StatusFailed || store.fails != 1 || draft.Transcript != "" {
		t.Fatalf("draft/error = %#v/%v", draft, err)
	}
}
func TestDirectCommitRequiresReviewAndRequiredFields(t *testing.T) {
	store := &voiceStoreStub{}
	service := testVoiceService(t, store, FakeTranscriber{Text: "fixture"})
	_, err := service.Commit(context.Background(), voiceActor(auth.RoleDriver), CommitInput{DraftID: "draft", ExpectedVersion: 2})
	if !errors.Is(err, ErrValidation) || store.commits != 0 {
		t.Fatalf("error/commits = %v/%d", err, store.commits)
	}
}
func TestDuplicateRequiresConsciousDecision(t *testing.T) {
	store := &voiceStoreStub{duplicates: []customers.Duplicate{{ID: "existing"}}}
	service := testVoiceService(t, store, FakeTranscriber{Text: "fixture"})
	input := validVoiceIntake()
	_, err := service.Commit(context.Background(), voiceActor(auth.RoleDriver), CommitInput{DraftID: "draft", ExpectedVersion: 2, Reviewed: true, Intake: input})
	if !errors.Is(err, ErrValidation) || store.commits != 0 {
		t.Fatalf("error/commits = %v/%d", err, store.commits)
	}
	_, err = service.Commit(context.Background(), voiceActor(auth.RoleDriver), CommitInput{DraftID: "draft", ExpectedVersion: 2, Reviewed: true, DuplicateReviewed: true, ExistingCustomerID: "existing", Intake: input})
	if err != nil || store.commits != 1 {
		t.Fatalf("error/commits = %v/%d", err, store.commits)
	}
}
func TestDriverAllowedAndUnknownRoleRejected(t *testing.T) {
	service := testVoiceService(t, &voiceStoreStub{}, FakeTranscriber{Text: "fixture"})
	_, err := service.Process(context.Background(), auth.Actor{UserID: "x"}, Audio{Reader: bytes.NewReader([]byte("a")), Size: 1}, Metadata{})
	if !errors.Is(err, auth.ErrForbidden) {
		t.Fatalf("error = %v", err)
	}
}

func validVoiceIntake() customers.IntakeInput {
	return customers.IntakeInput{Customer: customers.CustomerInput{FirstName: "Franz", LastName: "Huber", CountryCode: "AT", NotificationPreference: customers.NotifyNone}, Job: customers.JobInput{JobType: customers.JobTypeChippingOnly, VolumeM3: "80", EstimatedHackMinutes: 180, TransportMode: customers.TransportNone, Urgency: customers.UrgencyNormal, Source: customers.SourceVoice}}
}
