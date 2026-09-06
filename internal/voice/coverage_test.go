package voice

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"example.invalid/hackplan/internal/auth"
	"example.invalid/hackplan/internal/customers"
)

type voiceObserverStub struct {
	calls  int
	status string
}

func (observer *voiceObserverStub) ObserveVoice(status string, _ time.Duration) {
	observer.calls++
	observer.status = status
}

type voiceStoreSpy struct {
	*voiceStoreStub
	getIDs      []string
	discardedID string
	cleanup     int64
	commitInput CommitInput
}

func (store *voiceStoreSpy) Get(ctx context.Context, actor auth.Actor, id string) (Draft, error) {
	store.getIDs = append(store.getIDs, id)
	return store.voiceStoreStub.Get(ctx, actor, id)
}

func (store *voiceStoreSpy) Commit(ctx context.Context, actor auth.Actor, input CommitInput) (customers.CreatedIntake, error) {
	store.commitInput = input
	return store.voiceStoreStub.Commit(ctx, actor, input)
}

func (store *voiceStoreSpy) Discard(_ context.Context, _ auth.Actor, id string) error {
	store.discardedID = id
	return nil
}

func (store *voiceStoreSpy) Cleanup(context.Context) (int64, error) { return store.cleanup, nil }

func newObservedVoiceService(t *testing.T, store Store, observer Observer) *Service {
	t.Helper()
	location, err := time.LoadLocation("Europe/Vienna")
	if err != nil {
		t.Fatal(err)
	}
	service, err := New(store, FakeTranscriber{Text: "Franz Huber, Waldweg 1, 80 m³, drei Stunden Hackzeit"}, RuleExtractor{}, Config{
		Enabled: true, Retention: time.Hour, RateLimitPerMinute: 2, ConcurrentPerUser: 1, Timezone: location,
	}, func() time.Time { return time.Date(2026, 8, 25, 10, 0, 0, 0, location) }, WithObserver(observer))
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func TestVoiceServiceConfigurationAndProcessValidation(t *testing.T) {
	t.Parallel()
	location, _ := time.LoadLocation("Europe/Vienna")
	config := Config{Enabled: true, Retention: time.Hour, RateLimitPerMinute: 1, ConcurrentPerUser: 1, Timezone: location}
	if _, err := New(nil, FakeTranscriber{Text: "x"}, RuleExtractor{}, config, time.Now); err == nil {
		t.Fatal("New() accepted missing store")
	}
	config.Retention = time.Minute
	if _, err := New(&voiceStoreStub{}, FakeTranscriber{Text: "x"}, RuleExtractor{}, config, time.Now); err == nil {
		t.Fatal("New() accepted short retention")
	}

	store := &voiceStoreStub{}
	observer := &voiceObserverStub{}
	service := newObservedVoiceService(t, store, observer)
	actor := voiceActor(auth.RoleDriver)
	if service.Enabled() != true {
		t.Fatal("Enabled() = false")
	}
	if _, err := service.ProcessPrepared(t.Context(), actor, nil); !errors.Is(err, ErrValidation) || observer.status != "rejected" {
		t.Fatalf("ProcessPrepared(nil) error/status = %v/%q", err, observer.status)
	}
	if _, err := service.ProcessPrepared(t.Context(), actor, func() (Audio, Metadata, error) { return Audio{}, Metadata{}, nil }); !errors.Is(err, ErrValidation) || store.creates != 0 {
		t.Fatalf("ProcessPrepared(empty) error/creates = %v/%d", err, store.creates)
	}
	draft, err := service.Process(t.Context(), actor, Audio{Reader: bytes.NewReader([]byte("audio")), Size: 5}, Metadata{RecordedAt: time.Now()})
	if err != nil || draft.Status != StatusNeedsReview || observer.status != "needs_review" {
		t.Fatalf("Process() draft/error/status = %#v/%v/%q", draft, err, observer.status)
	}
	service.config.Enabled = false
	if _, err := service.Process(t.Context(), actor, Audio{Reader: bytes.NewReader([]byte("audio")), Size: 5}, Metadata{}); !errors.Is(err, ErrDisabled) {
		t.Fatalf("disabled Process() error = %v", err)
	}
}

func TestVoiceReadActionsConversionsAndCommitGuards(t *testing.T) {
	t.Parallel()
	base := &voiceStoreStub{draft: Draft{ID: "draft", Status: StatusNeedsReview}, duplicates: []customers.Duplicate{{ID: "existing"}}}
	store := &voiceStoreSpy{voiceStoreStub: base, cleanup: 3}
	service := newObservedVoiceService(t, store, nil)
	actor := voiceActor(auth.RoleDriver)

	if _, err := service.Get(t.Context(), actor, " draft "); err != nil || store.getIDs[len(store.getIDs)-1] != "draft" {
		t.Fatalf("Get() error/IDs = %v/%v", err, store.getIDs)
	}
	draft := Draft{Fields: Fields{FirstName: Field{Value: "Franz"}, LastName: Field{Value: "Huber"}, Email: Field{Value: "f@example.test"}}}
	duplicates, err := service.Duplicates(t.Context(), actor, draft)
	if err != nil || len(duplicates) != 1 {
		t.Fatalf("Duplicates() = %#v, %v", duplicates, err)
	}
	if err := service.Discard(t.Context(), actor, " draft "); err != nil || store.discardedID != "draft" {
		t.Fatalf("Discard() error/id = %v/%q", err, store.discardedID)
	}
	if count, err := service.Cleanup(t.Context()); err != nil || count != 3 {
		t.Fatalf("Cleanup() = %d, %v", count, err)
	}

	fields := Fields{
		FirstName: Field{Value: "Franz"}, LastName: Field{Value: "Huber"}, VolumeM3: Field{Value: "80"},
		EstimatedHackMinutes: Field{Value: "180"}, EstimatedTransportMinutes: Field{Value: "25"}, TransportTripCount: Field{Value: "2"},
		TransportMode: Field{Value: "internal"}, Urgency: Field{Value: "unexpected"}, Note: Field{Value: "Bitte anrufen"},
	}
	intake := IntakeFromFields(fields)
	if intake.Job.JobType != customers.JobTypeChippingWithTransport || intake.Job.TransportMode != customers.TransportMode("internal") || intake.Job.Urgency != customers.UrgencyNormal || intake.InitialNote != "Bitte anrufen" {
		t.Fatalf("IntakeFromFields() = %#v", intake)
	}
	if customer := CustomerFromFields(fields); customer.FirstName != "Franz" || customer.CountryCode != "AT" || customer.NotificationPreference != customers.NotifyNone {
		t.Fatalf("CustomerFromFields() = %#v", customer)
	}

	input := CommitInput{DraftID: " draft ", ExistingCustomerID: "missing", ExpectedVersion: 1, Reviewed: true, DuplicateReviewed: true, Intake: validVoiceIntake()}
	if _, err := service.Commit(t.Context(), actor, input); !errors.Is(err, ErrValidation) || base.commits != 0 {
		t.Fatalf("Commit() unreviewed duplicate error/commits = %v/%d", err, base.commits)
	}
	input.ExistingCustomerID = "existing"
	result, err := service.Commit(t.Context(), actor, input)
	if err != nil || result.JobID != "job" || store.commitInput.Intake.Job.Source != customers.SourceVoice {
		t.Fatalf("Commit() result/error/input = %#v/%v/%#v", result, err, store.commitInput)
	}
	base.draft.Status = StatusCommitted
	base.draft.Committed = customers.CreatedIntake{CustomerID: "already"}
	before := base.commits
	result, err = service.Commit(t.Context(), actor, input)
	if err != nil || result.CustomerID != "already" || base.commits != before {
		t.Fatalf("idempotent Commit() result/error/commits = %#v/%v/%d", result, err, base.commits)
	}
	if containsDuplicate(base.duplicates, "not-present") {
		t.Fatal("containsDuplicate() matched unknown ID")
	}
}

func TestVoiceExtractorAndProviderConstructors(t *testing.T) {
	t.Parallel()
	if got := (HybridExtractor{Rules: RuleExtractor{}}).Version(); got != ruleParserVersion {
		t.Fatalf("HybridExtractor without provider Version() = %q", got)
	}
	if got := (HybridExtractor{Rules: RuleExtractor{}, Provider: structuredStub{}}).Version(); got != ruleParserVersion+"+stub-v1" {
		t.Fatalf("HybridExtractor with provider Version() = %q", got)
	}
	if !validModelFields(Fields{}) {
		t.Fatal("validModelFields() rejected an empty schema-compatible value")
	}
	tooLong := Fields{FirstName: Field{Confidence: .5, Warnings: []string{strings.Repeat("x", 201)}}}
	if validModelFields(tooLong) {
		t.Fatal("validModelFields() accepted an oversized warning")
	}
	if _, err := NewOpenAITranscriber("", "model", time.Second, 1024); err == nil {
		t.Fatal("NewOpenAITranscriber() accepted missing key")
	}
	transcriber, err := NewOpenAITranscriber("key", "model", time.Second, 1024)
	if err != nil || transcriber.model != "model" {
		t.Fatalf("NewOpenAITranscriber() = %#v, %v", transcriber, err)
	}
	if _, err := NewWhisperCPPTranscriber("base", time.Second, 1024); err == nil {
		t.Fatal("NewWhisperCPPTranscriber() accepted a model other than small")
	}
	localTranscriber, err := NewWhisperCPPTranscriber("small", 10*time.Minute, 1024)
	if err != nil || localTranscriber.endpoint != whisperCPPInferenceURL {
		t.Fatalf("NewWhisperCPPTranscriber() = %#v, %v", localTranscriber, err)
	}
	transport, ok := localTranscriber.client.Transport.(*http.Transport)
	if !ok || transport.Proxy != nil || localTranscriber.client.CheckRedirect == nil {
		t.Fatalf("local whisper client is not proxy-free and redirect-rejecting: %#v", localTranscriber.client)
	}
	if _, err := NewOpenAIStructuredProvider("key", "model", 0, 1024); err == nil {
		t.Fatal("NewOpenAIStructuredProvider() accepted zero timeout")
	}
	provider, err := NewOpenAIStructuredProvider("key", "model", time.Second, 1024)
	if err != nil || provider.Version() != "openai-model" {
		t.Fatalf("NewOpenAIStructuredProvider() = %#v, %v", provider, err)
	}
	if name := safeAudioName("private.exe"); !strings.HasPrefix(name, "aufnahme-") || !strings.HasSuffix(name, ".bin") {
		t.Fatalf("safeAudioName() = %q", name)
	}
}
