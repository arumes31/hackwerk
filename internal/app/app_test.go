package app

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"example.invalid/hackplan/internal/config"
)

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
