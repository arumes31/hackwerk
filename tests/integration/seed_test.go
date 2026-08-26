//go:build integration

package integration_test

import (
	"bytes"
	"io"
	"os"
	"testing"
	"time"

	"example.invalid/hackplan/internal/adapters/postgres"
	"example.invalid/hackplan/internal/adapters/postgres/migrate"
	"example.invalid/hackplan/internal/cli"
	"example.invalid/hackplan/internal/config"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestDevelopmentSeedIsCompleteSyntheticAndIdempotent(t *testing.T) {
	databaseURL := integrationDatabaseURL(t)
	t.Setenv("APP_ENV", "test")
	t.Setenv("DATABASE_URL", databaseURL)
	t.Setenv("APP_BASE_URL", "http://localhost:18533")
	t.Setenv("MAIL_ENABLED", "false")
	t.Setenv("SMS_ENABLED", "false")
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if err := migrate.Run(t.Context(), databaseURL, migrate.DirectionUp, io.Discard); err != nil {
		t.Fatal(err)
	}
	pool, err := postgres.Open(t.Context(), config.Database{URL: databaseURL, MaxConnections: 16, ConnectTimeout: 5 * time.Second, ReadinessTimeout: 2 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if _, err := pool.Exec(t.Context(), `TRUNCATE worker_heartbeats, voice_drafts, planning_suggestions, planning_runs, calendar_feeds,
		notifications, confirmation_requests, outbox_events, appointment_resources, appointment_drivers, appointments,
		job_notes, waitlist_entries, jobs, job_number_counters, customers, availability_exceptions, availability_rules,
		resources, audit_events, auth_rate_limits, sessions, drivers, users RESTART IDENTITY CASCADE`); err != nil {
		t.Fatal(err)
	}

	var first bytes.Buffer
	if err := cli.SeedDevelopment(t.Context(), cfg, &first); err != nil {
		t.Fatal(err)
	}
	if got := countRows(t, pool, "users"); got != 6 {
		t.Fatalf("users=%d want 6", got)
	}
	if got := countRows(t, pool, "drivers"); got != 5 {
		t.Fatalf("drivers=%d want 5", got)
	}
	if got := countRows(t, pool, "customers"); got != 7 {
		t.Fatalf("customers=%d want 7", got)
	}
	if got := countRows(t, pool, "resources"); got != 3 {
		t.Fatalf("resources=%d want 3", got)
	}
	for state, query := range map[string]string{
		"proposal":  "SELECT count(*) FROM appointments WHERE lifecycle_status='proposal'",
		"pending":   "SELECT count(*) FROM appointments WHERE lifecycle_status='fixed' AND confirmation_status='pending'",
		"confirmed": "SELECT count(*) FROM appointments WHERE lifecycle_status='fixed' AND confirmation_status='confirmed'",
		"declined":  "SELECT count(*) FROM appointments WHERE lifecycle_status='fixed' AND confirmation_status='declined'",
		"completed": "SELECT count(*) FROM appointments WHERE lifecycle_status='completed'",
		"failed":    "SELECT count(*) FROM notifications WHERE status='failed' AND last_error_code='provider_permanent'",
	} {
		var count int
		if err := pool.QueryRow(t.Context(), query).Scan(&count); err != nil || count < 1 {
			t.Fatalf("seed state %s count=%d err=%v", state, count, err)
		}
	}
	var nonSynthetic int
	if err := pool.QueryRow(t.Context(), "SELECT count(*) FROM customers WHERE email IS NULL OR email::text NOT LIKE '%@example.test'").Scan(&nonSynthetic); err != nil || nonSynthetic != 0 {
		t.Fatalf("non-synthetic contacts=%d err=%v", nonSynthetic, err)
	}
	var passwordFingerprint string
	if err := pool.QueryRow(t.Context(), "SELECT md5(string_agg(password_hash, ',' ORDER BY username)) FROM users").Scan(&passwordFingerprint); err != nil {
		t.Fatal(err)
	}

	var second bytes.Buffer
	if err := cli.SeedDevelopment(t.Context(), cfg, &second); err != nil {
		t.Fatal(err)
	}
	var secondFingerprint string
	if err := pool.QueryRow(t.Context(), "SELECT md5(string_agg(password_hash, ',' ORDER BY username)) FROM users").Scan(&secondFingerprint); err != nil {
		t.Fatal(err)
	}
	if passwordFingerprint != secondFingerprint || countRows(t, pool, "users") != 6 || countRows(t, pool, "customers") != 7 || countRows(t, pool, "appointments") != 6 {
		t.Fatal("second seed changed credentials or created duplicate demo records")
	}
}

func integrationDatabaseURL(t *testing.T) string {
	t.Helper()
	value := os.Getenv("TEST_DATABASE_URL")
	if value == "" {
		t.Fatal("TEST_DATABASE_URL is required for integration tests")
	}
	return value
}

func countRows(t *testing.T, pool *pgxpool.Pool, table string) int {
	t.Helper()
	query := map[string]string{
		"users": "SELECT count(*) FROM users", "drivers": "SELECT count(*) FROM drivers",
		"customers": "SELECT count(*) FROM customers", "resources": "SELECT count(*) FROM resources",
		"appointments": "SELECT count(*) FROM appointments",
	}[table]
	if query == "" {
		t.Fatalf("countRows table %q is not allowed", table)
	}
	var count int
	if err := pool.QueryRow(t.Context(), query).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}
