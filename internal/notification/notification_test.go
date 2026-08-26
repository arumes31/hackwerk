package notification

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func TestTokenMaterialIsStableHashedAndRotatable(t *testing.T) {
	ring, err := NewKeyRing(map[string]string{
		"old": "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=",
		"new": "ZmVkY2JhOTg3NjU0MzIxMGZlZGNiYTk4NzY1NDMyMTA=",
	}, "new")
	if err != nil {
		t.Fatal(err)
	}
	issued, err := ring.Issue("request", "appointment", 2)
	if err != nil {
		t.Fatal(err)
	}
	reconstructed, err := ring.Reconstruct("new", "request", "appointment", 2)
	if err != nil {
		t.Fatal(err)
	}
	if issued.Raw != reconstructed.Raw || issued.FormNonce != reconstructed.FormNonce || issued.Raw == issued.FormNonce {
		t.Fatal("derived token material is not stable and separated")
	}
	hash, err := HashRawToken(issued.Raw)
	if err != nil || !ConstantTimeEqual(hash, issued.Hash) || strings.Contains(string(issued.Hash), issued.Raw) {
		t.Fatal("raw token hash mismatch")
	}
	if _, err := ring.Reconstruct("removed", "request", "appointment", 2); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("missing key error = %v", err)
	}
}

func TestTemplateUsesViennaGermanAndEscapesCustomerData(t *testing.T) {
	location, err := time.LoadLocation("Europe/Vienna")
	if err != nil {
		t.Fatal(err)
	}
	value, err := Render(TemplateInput{
		CustomerName: "<script>alert(1)</script>", JobType: "chipping_with_transport", VolumeM3: "25",
		StartsAt: time.Date(2026, 8, 26, 6, 30, 0, 0, time.UTC), EndsAt: time.Date(2026, 8, 26, 9, 0, 0, 0, time.UTC),
		ConfirmationURL: "https://hackwerk.example/termin/token", BusinessName: "HackWerk", BusinessAddress: "Werk 1", BusinessPhone: "+43 1 2",
	}, location)
	if err != nil {
		t.Fatal(err)
	}
	for _, wanted := range []string{"Mittwoch, 26.08.2026 um 08:30 Uhr", "2 Stunden 30 Minuten", "Hacken mit Transport"} {
		if !strings.Contains(value.Text, wanted) {
			t.Fatalf("text does not contain %q: %s", wanted, value.Text)
		}
	}
	if strings.Contains(value.HTML, "<script>") || !strings.Contains(value.HTML, "&lt;script&gt;") {
		t.Fatalf("HTML customer data was not escaped: %s", value.HTML)
	}
}

func TestSyntheticPreviewAndSMSSegments(t *testing.T) {
	location, err := time.LoadLocation("Europe/Vienna")
	if err != nil {
		t.Fatal(err)
	}
	preview, err := SyntheticPreview(location)
	if err != nil {
		t.Fatal(err)
	}
	if preview.Subject == "" || preview.SMSSegments < 1 || preview.SMSEncoding == "" || !strings.Contains(preview.Text, "Maria Muster") {
		t.Fatalf("preview = %+v", preview)
	}
	for _, forbidden := range []string{"@example.test", "+43 660", "api_key", "password"} {
		if strings.Contains(strings.ToLower(preview.Text+preview.SMS), forbidden) {
			t.Fatalf("synthetic preview contains forbidden value %q", forbidden)
		}
	}
	tests := []struct {
		name, value, encoding string
		segments              int
	}{
		{name: "empty", value: "", encoding: "GSM-7", segments: 0},
		{name: "single GSM", value: strings.Repeat("a", 160), encoding: "GSM-7", segments: 1},
		{name: "multipart GSM", value: strings.Repeat("a", 161), encoding: "GSM-7", segments: 2},
		{name: "extended GSM", value: strings.Repeat("^", 81), encoding: "GSM-7", segments: 2},
		{name: "single unicode", value: strings.Repeat("č", 70), encoding: "UCS-2", segments: 1},
		{name: "multipart unicode", value: strings.Repeat("č", 71), encoding: "UCS-2", segments: 2},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			segments, encoding := SMSSegmentCount(test.value)
			if segments != test.segments || encoding != test.encoding {
				t.Fatalf("SMSSegmentCount() = %d/%s, want %d/%s", segments, encoding, test.segments, test.encoding)
			}
		})
	}
}

func TestBackoffAndRecipientMasking(t *testing.T) {
	if first, repeat := Backoff(3, "same"), Backoff(3, "same"); first != repeat || first < 4*time.Second || first >= 5*time.Second {
		t.Fatalf("unexpected deterministic backoff %s/%s", first, repeat)
	}
	if got := Backoff(99, "same"); got > time.Hour {
		t.Fatalf("backoff exceeds cap: %s", got)
	}
	if got := MaskRecipient("daniel@example.test", ChannelEmail); got != "d***@example.test" {
		t.Fatalf("masked email = %q", got)
	}
	if got := MaskRecipient("+43 664 1234567", ChannelSMS); got != "***567" {
		t.Fatalf("masked phone = %q", got)
	}
}

type processorStore struct {
	events    []ClaimedEvent
	delivery  Delivery
	marked    bool
	completed bool
	retried   bool
	dead      bool
	errorCode string
	available time.Time
	markErr   error
	stateErr  error
}

func (store *processorStore) Claim(_ context.Context, _ string, _, _ time.Time, batchSize int32) ([]ClaimedEvent, error) {
	if len(store.events) == 0 {
		return nil, nil
	}
	limit := min(len(store.events), int(batchSize))
	result := append([]ClaimedEvent(nil), store.events[:limit]...)
	store.events = store.events[limit:]
	return result, nil
}
func (store *processorStore) LoadDelivery(context.Context, string) (Delivery, error) {
	return store.delivery, nil
}
func (store *processorStore) MarkSending(context.Context, ClaimedEvent, string, time.Time, time.Time) error {
	store.marked = true
	return store.markErr
}

func TestProcessorDoesNotResendUncertainDelivery(t *testing.T) {
	now := time.Date(2026, 8, 25, 10, 0, 0, 0, time.UTC)
	event := ClaimedEvent{OutboxID: "outbox", NotificationID: "notification", IdempotencyKey: "safe-key", Attempt: 2, MaxAttempts: 3}
	delivery := Delivery{
		NotificationID: "notification", AppointmentID: "appointment", ConfirmationRequestID: "request",
		Channel: "email", Recipient: "secret@example.test", TokenKeyID: DevelopmentKeyID, TokenVersion: 1,
		ConfirmationStatus: "active", Lifecycle: "fixed", StartsAt: now.Add(time.Hour), EndsAt: now.Add(3 * time.Hour), ExpiresAt: now.Add(24 * time.Hour),
		JobType: "chipping_only", VolumeM3: "20", CustomerName: "Private Customer",
	}
	store := &processorStore{events: []ClaimedEvent{event}, delivery: delivery, markErr: ErrDeliveryUncertain}
	provider := NewFakeProvider(nil)
	processor, err := NewProcessor(store, map[Channel]Provider{ChannelEmail: provider}, DevelopmentKeyRing(), time.UTC, ProcessorConfig{
		BaseURL: "https://hackwerk.example", Lease: time.Minute, BatchSize: 5,
	}, func() time.Time { return now }, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	if count, runErr := processor.RunOnce(t.Context()); runErr != nil || count != 1 {
		t.Fatalf("RunOnce count/error=%d/%v", count, runErr)
	}
	if !store.dead || store.errorCode != "delivery_uncertain" || len(provider.Deliveries()) != 0 {
		t.Fatalf("uncertain delivery state=%+v provider calls=%d", store, len(provider.Deliveries()))
	}
}
func (store *processorStore) Complete(context.Context, ClaimedEvent, string, string) error {
	store.completed = true
	return nil
}
func (store *processorStore) Retry(_ context.Context, _ ClaimedEvent, _ string, available time.Time, code string) error {
	store.retried, store.available, store.errorCode = true, available, code
	return store.stateErr
}
func (store *processorStore) Dead(_ context.Context, _ ClaimedEvent, _, code string) error {
	store.dead, store.errorCode = true, code
	return store.stateErr
}

func TestProcessorReportsFailureStatePersistenceWithoutPrivateData(t *testing.T) {
	now := time.Date(2026, 8, 25, 10, 0, 0, 0, time.UTC)
	event := ClaimedEvent{OutboxID: "outbox", NotificationID: "notification", IdempotencyKey: "safe-key", Attempt: 1, MaxAttempts: 3}
	store := &processorStore{
		events: []ClaimedEvent{event}, stateErr: errors.New("database unavailable"),
		delivery: Delivery{
			NotificationID: "notification", AppointmentID: "appointment", ConfirmationRequestID: "request",
			Channel: "email", Recipient: "private@example.test", TokenKeyID: DevelopmentKeyID, TokenVersion: 1,
			ConfirmationStatus: "active", Lifecycle: "fixed", StartsAt: now.Add(time.Hour), EndsAt: now.Add(2 * time.Hour), ExpiresAt: now.Add(24 * time.Hour),
			JobType: "chipping_only", VolumeM3: "20", CustomerName: "Private Name",
		},
	}
	var logs bytes.Buffer
	processor, err := NewProcessor(store, map[Channel]Provider{}, DevelopmentKeyRing(), time.UTC, ProcessorConfig{
		BaseURL: "https://hackwerk.example", Lease: time.Minute, BatchSize: 1,
	}, func() time.Time { return now }, slog.New(slog.NewTextHandler(&logs, nil)))
	if err != nil {
		t.Fatal(err)
	}
	_, _ = processor.RunOnce(t.Context())
	if !strings.Contains(logs.String(), "notification_state_persist_failed") {
		t.Fatalf("missing persistence error log: %s", logs.String())
	}
	for _, private := range []string{"private@example.test", "Private Name", "database unavailable"} {
		if strings.Contains(logs.String(), private) {
			t.Fatalf("persistence log leaked %q: %s", private, logs.String())
		}
	}
}

func TestProcessorSuccessRetryAndRedactedLogs(t *testing.T) {
	now := time.Date(2026, 8, 25, 10, 0, 0, 0, time.UTC)
	ring := DevelopmentKeyRing()
	event := ClaimedEvent{OutboxID: "outbox", NotificationID: "notification", IdempotencyKey: "safe-key", Attempt: 1, MaxAttempts: 3}
	delivery := Delivery{
		NotificationID: "notification", AppointmentID: "appointment", ConfirmationRequestID: "request",
		Channel: "email", Recipient: "secret@example.test", TokenKeyID: DevelopmentKeyID, TokenVersion: 1,
		ConfirmationStatus: "active", Lifecycle: "fixed", StartsAt: now.Add(time.Hour), EndsAt: now.Add(3 * time.Hour), ExpiresAt: now.Add(24 * time.Hour),
		JobType: "chipping_only", VolumeM3: "20", CustomerName: "Private Customer",
	}
	var logOutput bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logOutput, nil))
	store := &processorStore{events: []ClaimedEvent{event}, delivery: delivery}
	provider := NewFakeProvider(nil)
	processor, err := NewProcessor(store, map[Channel]Provider{ChannelEmail: provider}, ring, time.UTC, ProcessorConfig{BaseURL: "https://hackwerk.example", Lease: time.Minute, BatchSize: 5}, func() time.Time { return now }, logger)
	if err != nil {
		t.Fatal(err)
	}
	if count, err := processor.RunOnce(t.Context()); err != nil || count != 1 || !store.marked || !store.completed || len(provider.Deliveries()) != 1 {
		t.Fatalf("successful delivery count=%d err=%v store=%+v", count, err, store)
	}
	store = &processorStore{events: []ClaimedEvent{event}, delivery: delivery}
	processor, _ = NewProcessor(store, map[Channel]Provider{}, ring, time.UTC, ProcessorConfig{BaseURL: "https://hackwerk.example", Lease: time.Minute, BatchSize: 5}, func() time.Time { return now }, logger)
	_, _ = processor.RunOnce(t.Context())
	if !store.retried || store.dead || store.errorCode != "provider_disabled" {
		t.Fatalf("temporary failure state = %+v", store)
	}
	logs := logOutput.String()
	for _, secret := range []string{"secret@example.test", "Private Customer", "/termin/"} {
		if strings.Contains(logs, secret) {
			t.Fatalf("logs contain private content %q: %s", secret, logs)
		}
	}
}
