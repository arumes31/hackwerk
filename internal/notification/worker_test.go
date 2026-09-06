package notification

import (
	"bytes"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func TestProcessorMarksInvalidDeliveriesWithoutContactingProviders(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name      string
		store     *processorStore
		wantCode  string
		wantRetry bool
	}{
		{
			name:      "delivery cannot be loaded",
			store:     &processorStore{events: []ClaimedEvent{{OutboxID: "outbox", NotificationID: "notification", Attempt: 1, MaxAttempts: 3}}, loadErr: errors.New("database unavailable")},
			wantCode:  "delivery_load_failed",
			wantRetry: true,
		},
		{
			name:      "confirmation inactive",
			store:     &processorStore{events: []ClaimedEvent{{OutboxID: "outbox", NotificationID: "notification", Attempt: 1, MaxAttempts: 3}}, delivery: activeTestDelivery(now, func(value *Delivery) { value.Lifecycle = "cancelled" })},
			wantCode:  "confirmation_inactive",
			wantRetry: false,
		},
		{
			name:      "token key unavailable",
			store:     &processorStore{events: []ClaimedEvent{{OutboxID: "outbox", NotificationID: "notification", Attempt: 1, MaxAttempts: 3}}, delivery: activeTestDelivery(now, func(value *Delivery) { value.TokenKeyID = "removed" })},
			wantCode:  "token_key_unavailable",
			wantRetry: false,
		},
		{
			name:      "template input invalid",
			store:     &processorStore{events: []ClaimedEvent{{OutboxID: "outbox", NotificationID: "notification", Attempt: 1, MaxAttempts: 3}}, delivery: activeTestDelivery(now, func(value *Delivery) { value.StartsAt = time.Time{} })},
			wantCode:  "template_invalid",
			wantRetry: false,
		},
		{
			name:      "marking send state fails",
			store:     &processorStore{events: []ClaimedEvent{{OutboxID: "outbox", NotificationID: "notification", Attempt: 1, MaxAttempts: 3}}, markErr: errors.New("database unavailable"), delivery: activeTestDelivery(now, nil)},
			wantCode:  "notification_state_failed",
			wantRetry: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			processor := newTestProcessor(t, test.store, map[Channel]Provider{ChannelEmail: NewFakeProvider(nil)}, now, slog.New(slog.NewTextHandler(io.Discard, nil)))
			count, err := processor.RunOnce(t.Context())
			if err != nil || count != 1 || test.store.errorCode != test.wantCode || test.store.retried != test.wantRetry || test.store.dead == test.wantRetry {
				t.Fatalf("RunOnce count/error/store = %d / %v / %#v", count, err, test.store)
			}
		})
	}
}

func TestProcessorClassifiesProviderFailuresAndCompletionErrors(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name        string
		event       ClaimedEvent
		provider    Provider
		completeErr error
		wantCode    string
		wantDead    bool
		wantDone    bool
	}{
		{name: "permanent provider rejection", event: ClaimedEvent{OutboxID: "outbox", NotificationID: "notification", Attempt: 1, MaxAttempts: 3}, provider: NewFakeProvider(ErrPermanent), wantCode: "provider_permanent", wantDead: true},
		{name: "maximum temporary attempts", event: ClaimedEvent{OutboxID: "outbox", NotificationID: "notification", Attempt: 3, MaxAttempts: 3}, provider: NewFakeProvider(ErrTemporary), wantCode: "provider_temporary", wantDead: true},
		{name: "completion persistence failure is logged", event: ClaimedEvent{OutboxID: "outbox", NotificationID: "notification", Attempt: 1, MaxAttempts: 3}, provider: NewFakeProvider(nil), completeErr: errors.New("database unavailable"), wantDone: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := &processorStore{events: []ClaimedEvent{test.event}, delivery: activeTestDelivery(now, nil), completeErr: test.completeErr}
			var logs bytes.Buffer
			processor := newTestProcessor(t, store, map[Channel]Provider{ChannelEmail: test.provider}, now, slog.New(slog.NewTextHandler(&logs, nil)))
			count, err := processor.RunOnce(t.Context())
			if err != nil || count != 1 || store.errorCode != test.wantCode || store.dead != test.wantDead || store.completed != test.wantDone {
				t.Fatalf("RunOnce count/error/store = %d / %v / %#v", count, err, store)
			}
			if test.completeErr != nil && !strings.Contains(logs.String(), "notification_completion_failed") {
				t.Fatalf("completion failure was not redacted/logged: %s", logs.String())
			}
		})
	}
}

func TestProcessorDependencyValidationAndClaimFailure(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
	if _, err := NewProcessor(nil, nil, DevelopmentKeyRing(), time.UTC, ProcessorConfig{BaseURL: "https://hackwerk.example", Lease: time.Minute, BatchSize: 1}, nil, nil); err == nil {
		t.Fatal("NewProcessor accepted nil store")
	}
	if id := NewWorkerID(); !strings.HasPrefix(id, "worker-") || len(id) != len("worker-")+16 {
		t.Fatalf("NewWorkerID() = %q", id)
	}
	store := &processorStore{claimErr: errors.New("database unavailable")}
	processor := newTestProcessor(t, store, nil, now, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if count, err := processor.RunOnce(t.Context()); count != 0 || err == nil {
		t.Fatalf("RunOnce claim failure = %d / %v", count, err)
	}
}

func activeTestDelivery(now time.Time, mutate func(*Delivery)) Delivery {
	value := Delivery{
		NotificationID: "notification", AppointmentID: "appointment", ConfirmationRequestID: "request",
		Channel: "email", Recipient: "private@example.test", TokenKeyID: DevelopmentKeyID, TokenVersion: 1,
		ConfirmationStatus: "active", Lifecycle: "fixed", StartsAt: now.Add(time.Hour), EndsAt: now.Add(2 * time.Hour), ExpiresAt: now.Add(24 * time.Hour),
		JobType: "chipping_only", VolumeM3: "20", CustomerName: "Private Customer",
	}
	if mutate != nil {
		mutate(&value)
	}
	return value
}

func newTestProcessor(t *testing.T, store WorkerStore, providers map[Channel]Provider, now time.Time, logger *slog.Logger) *Processor {
	t.Helper()
	processor, err := NewProcessor(store, providers, DevelopmentKeyRing(), time.UTC, ProcessorConfig{BaseURL: "https://hackwerk.example", Lease: time.Minute, BatchSize: 2, WorkerID: "worker-test"}, func() time.Time { return now }, logger)
	if err != nil {
		t.Fatal(err)
	}
	return processor
}
