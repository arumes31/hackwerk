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

func testVoiceService(t *testing.T, store *voiceStoreStub, transcriber Transcriber) *Service {
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
