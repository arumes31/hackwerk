//go:build integration

package integration_test

import (
	"context"
	"io"
	"os"
	"sync"
	"testing"
	"time"

	"example.invalid/hackplan/internal/adapters/postgres"
	"example.invalid/hackplan/internal/adapters/postgres/migrate"
	"example.invalid/hackplan/internal/config"
)

func TestOperationsReadinessHeartbeatAndMetricsSnapshot(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Fatal("TEST_DATABASE_URL is required")
	}
	ctx := context.Background()
	if err := migrate.Run(ctx, databaseURL, migrate.DirectionUp, io.Discard); err != nil {
		t.Fatal(err)
	}
	pool, err := postgres.Open(ctx, config.Database{URL: databaseURL, MaxConnections: 5, ConnectTimeout: 5 * time.Second, ReadinessTimeout: 2 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	store := postgres.NewOperationsStore(pool, time.Minute)
	if err = store.Ready(ctx, config.CurrentSchemaVersion); err != nil {
		t.Fatalf("Ready()=%v", err)
	}
	if err = store.Ready(ctx, config.CurrentSchemaVersion-1); err == nil {
		t.Fatal("Ready() accepted incompatible schema")
	}
	if _, err = pool.Exec(ctx, "TRUNCATE worker_heartbeats"); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err = store.Heartbeat(ctx, "integration-worker", now.Add(-time.Minute), now, "running"); err != nil {
		t.Fatal(err)
	}
	heartbeat, healthy, err := store.WorkerHealthy(ctx, time.Minute)
	if err != nil || !healthy || heartbeat.IsZero() {
		t.Fatalf("WorkerHealthy()=%v/%v/%v", heartbeat, healthy, err)
	}
	snapshot, err := store.Collect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !snapshot.WorkerHealthy || snapshot.DBMax != 5 || snapshot.OutboxPending < 0 || snapshot.OutboxOldestSeconds < 0 {
		t.Fatalf("snapshot=%+v", snapshot)
	}
}

func TestMigrationCommandsSerialize(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Fatal("TEST_DATABASE_URL is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var wait sync.WaitGroup
	errorsOut := make(chan error, 2)
	for range 2 {
		wait.Add(1)
		go func() { defer wait.Done(); errorsOut <- migrate.Run(ctx, databaseURL, migrate.DirectionUp, io.Discard) }()
	}
	wait.Wait()
	close(errorsOut)
	for err := range errorsOut {
		if err != nil {
			t.Fatalf("concurrent migration=%v", err)
		}
	}
}
