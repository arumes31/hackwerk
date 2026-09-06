package app

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"example.invalid/hackplan/internal/config"
)

type heartbeatCall struct {
	workerID    string
	startedAt   time.Time
	heartbeatAt time.Time
	status      string
}

type heartbeatFake struct {
	calls chan heartbeatCall
	err   error
}

func (fake heartbeatFake) Heartbeat(_ context.Context, workerID string, startedAt, heartbeatAt time.Time, status string) error {
	fake.calls <- heartbeatCall{workerID: workerID, startedAt: startedAt, heartbeatAt: heartbeatAt, status: status}
	return fake.err
}

type workerHealthFake struct {
	readyErr     error
	heartbeatErr error
	healthy      bool
}

func (fake workerHealthFake) Ready(context.Context, int64) error { return fake.readyErr }

func (fake workerHealthFake) WorkerHealthyByID(context.Context, string, time.Duration) (time.Time, bool, error) {
	return time.Time{}, fake.healthy, fake.heartbeatErr
}

func TestCheckWorkerHealthRequiresSchemaAndFreshHeartbeat(t *testing.T) {
	t.Parallel()
	readyErr := errors.New("schema incompatible")
	heartbeatErr := errors.New("heartbeat query failed")
	tests := []struct {
		name        string
		fake        workerHealthFake
		wantErr     error
		wantMessage string
	}{
		{name: "ready and fresh", fake: workerHealthFake{healthy: true}},
		{name: "schema not ready", fake: workerHealthFake{readyErr: readyErr}, wantErr: readyErr},
		{name: "heartbeat query failed", fake: workerHealthFake{heartbeatErr: heartbeatErr}, wantErr: heartbeatErr},
		{name: "heartbeat stale", fake: workerHealthFake{}, wantMessage: "stale"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			err := checkWorkerHealth(t.Context(), test.fake, config.CurrentSchemaVersion, "worker-a", 2*time.Minute)
			if test.wantErr == nil && test.wantMessage == "" && err != nil {
				t.Fatalf("checkWorkerHealth() error = %v", err)
			}
			if (test.wantErr != nil || test.wantMessage != "") && err == nil {
				t.Fatal("checkWorkerHealth() returned nil")
			}
			if test.wantErr != nil && !errors.Is(err, test.wantErr) {
				t.Fatalf("checkWorkerHealth() error = %v", err)
			}
			if test.wantMessage != "" && !strings.Contains(err.Error(), test.wantMessage) {
				t.Fatalf("checkWorkerHealth() error = %v", err)
			}
		})
	}
}

func TestWorkerIdentityUsesStableConfiguredValueOrHostname(t *testing.T) {
	t.Parallel()
	if value, err := workerIdentity("worker-a", func() (string, error) { return "ignored", nil }); err != nil || value != "worker-a" {
		t.Fatalf("configured identity = %q/%v", value, err)
	}
	if value, err := workerIdentity("", func() (string, error) { return "container-42", nil }); err != nil || value != "container-42" {
		t.Fatalf("hostname identity = %q/%v", value, err)
	}
}

func TestRunWorkerHeartbeatContinuesWhileNotificationBatchIsBusy(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	ticks := make(chan time.Time)
	calls := make(chan heartbeatCall, 1)
	done := make(chan struct{})
	startedAt := time.Date(2026, time.August, 27, 8, 0, 0, 0, time.UTC)
	heartbeatAt := startedAt.Add(30 * time.Second)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	go func() {
		defer close(done)
		runWorkerHeartbeat(ctx, heartbeatFake{calls: calls}, "worker-a", startedAt, ticks, logger)
	}()

	ticks <- heartbeatAt
	select {
	case call := <-calls:
		if call.workerID != "worker-a" || !call.startedAt.Equal(startedAt) || !call.heartbeatAt.Equal(heartbeatAt) || call.status != "running" {
			t.Fatalf("heartbeat call = %#v", call)
		}
	case <-time.After(time.Second):
		t.Fatal("heartbeat was blocked by worker processing")
	}

	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("heartbeat loop did not stop after cancellation")
	}
}
