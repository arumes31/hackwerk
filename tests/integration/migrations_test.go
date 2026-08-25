//go:build integration

package integration_test

import (
	"bytes"
	"context"
	"os"
	"testing"

	"example.invalid/hackplan/internal/adapters/postgres/migrate"
)

func TestMigrationsUpDownUp(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Fatal("TEST_DATABASE_URL is required for integration tests")
	}

	var output bytes.Buffer
	ctx := context.Background()
	if err := migrate.Run(ctx, databaseURL, migrate.DirectionUp, &output); err != nil {
		t.Fatalf("first up: %v", err)
	}
	if err := migrate.Run(ctx, databaseURL, migrate.DirectionUp, &output); err != nil {
		t.Fatalf("idempotent up: %v", err)
	}
	if err := migrate.Run(ctx, databaseURL, migrate.DirectionStatus, &output); err != nil {
		t.Fatalf("status: %v", err)
	}
	if err := migrate.Run(ctx, databaseURL, migrate.DirectionDown, &output); err != nil {
		t.Fatalf("down: %v", err)
	}
	if err := migrate.Run(ctx, databaseURL, migrate.DirectionUp, &output); err != nil {
		t.Fatalf("second up: %v", err)
	}
}
