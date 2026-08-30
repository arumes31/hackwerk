//go:build integration

package integration_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"example.invalid/hackplan/internal/adapters/postgres"
	"example.invalid/hackplan/internal/adapters/postgres/migrate"
	"example.invalid/hackplan/internal/auth"
	"example.invalid/hackplan/internal/config"
	"example.invalid/hackplan/internal/customers"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestCustomersPersistence(t *testing.T) {
	t.Run("intake is atomic and audit contains no pii", func(t *testing.T) {
		ctx, pool, service, admin, _ := customerFixture(t)
		input := customerIntake("Franz", "Huber", "80", customers.UrgencyNormal, "Unterneukirchen")
		input.Customer.PhoneRaw = "0664 1234567"
		input.Customer.Email = "franz.huber@example.test"
		input.InitialNote = "Hackplatz über den Forstweg erreichbar"

		created, err := service.CreateIntake(ctx, admin, input, "request-intake")
		if err != nil {
			t.Fatalf("CreateIntake() error = %v", err)
		}
		if created.CustomerID == "" || created.JobID == "" || created.WaitlistID == "" || created.JobNumber == "" {
			t.Fatalf("CreateIntake() = %#v", created)
		}
		assertTableCount(t, ctx, pool, "customers", 1)
		assertTableCount(t, ctx, pool, "jobs", 1)
		assertTableCount(t, ctx, pool, "waitlist_entries", 1)
		assertTableCount(t, ctx, pool, "job_notes", 1)

		rows, err := pool.Query(ctx, "SELECT action, metadata::text FROM audit_events ORDER BY id")
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		actions := make([]string, 0, 4)
		for rows.Next() {
			var action, metadata string
			if err := rows.Scan(&action, &metadata); err != nil {
				t.Fatal(err)
			}
			actions = append(actions, action)
			for _, forbidden := range []string{"Franz", "Huber", "0664", "example.test", "Forstweg"} {
				if strings.Contains(metadata, forbidden) {
					t.Fatalf("audit metadata contains PII %q: %s", forbidden, metadata)
				}
			}
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		sort.Strings(actions)
		want := []string{"customer.created", "job.created", "job.note_added", "job.waitlisted"}
		if fmt.Sprint(actions) != fmt.Sprint(want) {
			t.Fatalf("audit actions = %v, want %v", actions, want)
		}
	})

	t.Run("late failure rolls back customer job waitlist and number", func(t *testing.T) {
		ctx, pool, service, admin, _ := customerFixture(t)
		input := customerIntake("Rollback", "Test", "10", customers.UrgencyLow, "Test")
		input.InitialNote = "forces author foreign key"
		admin.UserID = "00000000-0000-0000-0000-000000000099"
		if _, err := service.CreateIntake(ctx, admin, input, "request-rollback"); err == nil {
			t.Fatal("CreateIntake() error = nil, want foreign-key failure")
		}
		assertTableCount(t, ctx, pool, "customers", 0)
		assertTableCount(t, ctx, pool, "jobs", 0)
		assertTableCount(t, ctx, pool, "waitlist_entries", 0)
		assertTableCount(t, ctx, pool, "job_number_counters", 0)
	})

	t.Run("job numbers remain unique under concurrency", func(t *testing.T) {
		ctx, pool, service, admin, driver := customerFixture(t)
		created, err := service.CreateIntake(ctx, admin, customerIntake("Franz", "Huber", "80", customers.UrgencyNormal, "Unterneukirchen"), "request-base")
		if err != nil {
			t.Fatal(err)
		}

		const workers = 16
		start := make(chan struct{})
		numbers := make(chan string, workers)
		errorsFound := make(chan error, workers)
		var group sync.WaitGroup
		for index := range workers {
			group.Add(1)
			go func(index int) {
				defer group.Done()
				<-start
				input := customers.CreateJobInput{
					CustomerID: created.CustomerID,
					Job:        customerIntake("", "", fmt.Sprint(20+index), customers.UrgencyNormal, "Unterneukirchen").Job,
					RequestID:  fmt.Sprintf("request-%d", index),
				}
				job, createErr := service.CreateJob(ctx, driver, input)
				if createErr != nil {
					errorsFound <- createErr
					return
				}
				numbers <- job.JobNumber
			}(index)
		}
		close(start)
		group.Wait()
		close(numbers)
		close(errorsFound)
		for err := range errorsFound {
			t.Errorf("concurrent CreateJob() error = %v", err)
		}
		seen := map[string]struct{}{created.JobNumber: {}}
		for number := range numbers {
			if _, duplicate := seen[number]; duplicate {
				t.Errorf("duplicate job number %q", number)
			}
			seen[number] = struct{}{}
		}
		if len(seen) != workers+1 {
			t.Fatalf("unique job numbers = %d, want %d", len(seen), workers+1)
		}
		assertTableCount(t, ctx, pool, "jobs", workers+1)
	})

	t.Run("optimistic locking uniqueness append-only notes and archive", func(t *testing.T) {
		ctx, pool, service, admin, driver := customerFixture(t)
		created, err := service.CreateIntake(ctx, driver, customerIntake("Maria", "Maier", "150", customers.UrgencyUrgent, "Musterort"), "request-create")
		if err != nil {
			t.Fatal(err)
		}
		detail, err := service.CustomerDetail(ctx, driver, created.CustomerID)
		if err != nil {
			t.Fatal(err)
		}
		update := customers.UpdateCustomerInput{
			ID: created.CustomerID, ExpectedVersion: detail.Customer.Version, RequestID: "request-update",
			Customer: customers.CustomerInput{FirstName: "Maria", LastName: "Maier", Locality: "Neuer Ort", CountryCode: "AT", NotificationPreference: customers.NotifyNone},
		}
		if err := service.UpdateCustomer(ctx, driver, update); err != nil {
			t.Fatal(err)
		}
		if err := service.UpdateCustomer(ctx, driver, update); !errors.Is(err, customers.ErrConflict) {
			t.Fatalf("stale UpdateCustomer() error = %v, want conflict", err)
		}

		_, err = pool.Exec(ctx, "INSERT INTO waitlist_entries (job_id) VALUES ($1)", created.JobID)
		assertPostgresCode(t, err, "23505")

		noteID, err := service.AddNote(ctx, driver, created.JobID, "Bleibt unverändert", "", "integration-note-key", "request-note")
		if err != nil {
			t.Fatal(err)
		}
		_, err = pool.Exec(ctx, "UPDATE job_notes SET body = 'manipuliert' WHERE id = $1", noteID)
		assertPostgresCode(t, err, "55000")
		_, err = pool.Exec(ctx, "DELETE FROM job_notes WHERE id = $1", noteID)
		assertPostgresCode(t, err, "55000")

		if err := service.ArchiveJob(ctx, driver, created.JobID, detail.Jobs[0].Version, "request-driver"); !errors.Is(err, auth.ErrForbidden) {
			t.Fatalf("driver ArchiveJob() error = %v, want forbidden", err)
		}
		if err := service.ArchiveJob(ctx, admin, created.JobID, detail.Jobs[0].Version, "request-archive"); err != nil {
			t.Fatal(err)
		}
		var status string
		var archived, removed bool
		if err := pool.QueryRow(ctx, "SELECT workflow_status, archived_at IS NOT NULL FROM jobs WHERE id = $1", created.JobID).Scan(&status, &archived); err != nil {
			t.Fatal(err)
		}
		if err := pool.QueryRow(ctx, "SELECT removed_at IS NOT NULL FROM waitlist_entries WHERE id = $1", created.WaitlistID).Scan(&removed); err != nil {
			t.Fatal(err)
		}
		if status != "cancelled" || !archived || !removed {
			t.Fatalf("archive state = status %q, archived %v, waitlist removed %v", status, archived, removed)
		}
	})

	t.Run("archived customers reject direct new jobs and duplicates only warn", func(t *testing.T) {
		ctx, _, service, admin, driver := customerFixture(t)
		input := customerIntake("Franz", "Huber", "80", customers.UrgencyNormal, "Unterneukirchen")
		input.Customer.PhoneRaw = "0664 1234567"
		created, err := service.CreateIntake(ctx, driver, input, "request-first")
		if err != nil {
			t.Fatal(err)
		}
		second := customerIntake("Fritz", "Huberer", "40", customers.UrgencyLow, "Unterneukirchen")
		second.Customer.PhoneRaw = "+43 664 1234567"
		duplicate, err := service.CreateIntake(ctx, driver, second, "request-duplicate")
		if err != nil {
			t.Fatal(err)
		}
		if len(duplicate.Duplicates) == 0 || duplicate.Duplicates[0].ID != created.CustomerID {
			t.Fatalf("duplicate warnings = %#v", duplicate.Duplicates)
		}
		detail, err := service.CustomerDetail(ctx, admin, created.CustomerID)
		if err != nil {
			t.Fatal(err)
		}
		if err := service.ArchiveJob(ctx, admin, created.JobID, detail.Jobs[0].Version, "request-job-archive"); err != nil {
			t.Fatal(err)
		}
		if err := service.ArchiveCustomer(ctx, admin, created.CustomerID, detail.Customer.Version, "request-customer-archive"); err != nil {
			t.Fatal(err)
		}
		_, err = service.CreateJob(ctx, driver, customers.CreateJobInput{CustomerID: created.CustomerID, Job: customerIntake("", "", "25", customers.UrgencyNormal, "Unterneukirchen").Job})
		if !errors.Is(err, customers.ErrNotFound) {
			t.Fatalf("CreateJob() for archived customer error = %v, want not found", err)
		}
	})

	t.Run("waitlist filters and sort are applied server side", func(t *testing.T) {
		ctx, _, service, admin, _ := customerFixture(t)
		scenarios := []customers.IntakeInput{
			customerIntake("Franz", "Huber", "80", customers.UrgencyNormal, "Nord"),
			customerIntake("Maria", "Maier", "150", customers.UrgencyUrgent, "Süd"),
			customerIntake("Johann", "Berger", "40", customers.UrgencyLow, "Nord"),
		}
		scenarios[0].Job.PreferredStartDate = "2026-09-01"
		scenarios[1].Job.PreferredStartDate = "2026-10-01"
		scenarios[2].Job.JobType = customers.JobTypeChippingWithTransport
		scenarios[2].Job.TransportMode = customers.TransportUndecided
		for index, scenario := range scenarios {
			if _, err := service.CreateIntake(ctx, admin, scenario, fmt.Sprintf("request-filter-%d", index)); err != nil {
				t.Fatal(err)
			}
		}
		page, err := service.ListWaitlist(ctx, admin, customers.WaitlistFilter{Region: "Nord", Sort: "volume", Direction: "desc", Page: 1, PageSize: 25})
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Items) != 2 || page.Items[0].VolumeM3 != "80.00" || page.Items[1].VolumeM3 != "40.00" {
			t.Fatalf("volume sorted Nord waitlist = %#v", page.Items)
		}
		page, err = service.ListWaitlist(ctx, admin, customers.WaitlistFilter{JobType: string(customers.JobTypeChippingWithTransport), Sort: "entered", Direction: "asc", Page: 1, PageSize: 25})
		if err != nil || len(page.Items) != 1 || page.Items[0].LastName != "Berger" {
			t.Fatalf("transport filter = %#v, error = %v", page.Items, err)
		}
		page, err = service.ListWaitlist(ctx, admin, customers.WaitlistFilter{Urgency: string(customers.UrgencyUrgent), PreferredMonth: "2026-10", Sort: "preferred", Direction: "asc", Page: 1, PageSize: 25})
		if err != nil || len(page.Items) != 1 || page.Items[0].LastName != "Maier" {
			t.Fatalf("urgency/month filter = %#v, error = %v", page.Items, err)
		}
	})

	t.Run("customer list filters and count are applied server side", func(t *testing.T) {
		ctx, _, service, admin, _ := customerFixture(t)

		activeEmail := customerIntake("Erika", "Aktiv", "60", customers.UrgencyNormal, "Nord")
		activeEmail.Customer.Locality = "Nordstadt"
		activeEmail.Customer.Email = "erika.aktiv@example.test"
		activeEmail.Customer.NotificationPreference = customers.NotifyEmail
		if _, err := service.CreateIntake(ctx, admin, activeEmail, "request-customer-filter-active"); err != nil {
			t.Fatal(err)
		}

		missing := customerIntake("Konrad", "Fehlt", "40", customers.UrgencyNormal, "Süd")
		missing.Customer.Street = ""
		missing.Customer.PostalCode = ""
		missing.Customer.Locality = "Südort"
		missing.Customer.NotificationPreference = customers.NotifyNone
		if _, err := service.CreateIntake(ctx, admin, missing, "request-customer-filter-missing"); err != nil {
			t.Fatal(err)
		}

		historicalSMS := customerIntake("Heidi", "Historisch", "30", customers.UrgencyNormal, "Nord")
		historicalSMS.Customer.Locality = "Westdorf"
		historicalSMS.Customer.PhoneRaw = "0664 1234567"
		historicalSMS.Customer.NotificationPreference = customers.NotifySMS
		historical, err := service.CreateIntake(ctx, admin, historicalSMS, "request-customer-filter-historical")
		if err != nil {
			t.Fatal(err)
		}
		detail, err := service.CustomerDetail(ctx, admin, historical.CustomerID)
		if err != nil {
			t.Fatal(err)
		}
		if err := service.ArchiveJob(ctx, admin, historical.JobID, detail.Jobs[0].Version, "request-customer-filter-archive"); err != nil {
			t.Fatal(err)
		}

		tests := []struct {
			name   string
			filter customers.CustomerListFilter
			want   []string
		}{
			{name: "missing contact", filter: customers.CustomerListFilter{MissingContact: true}, want: []string{"Fehlt"}},
			{name: "incomplete address", filter: customers.CustomerListFilter{IncompleteAddress: true}, want: []string{"Fehlt"}},
			{name: "active jobs", filter: customers.CustomerListFilter{JobActivity: customers.CustomerJobsActive}, want: []string{"Aktiv", "Fehlt"}},
			{name: "without active job", filter: customers.CustomerListFilter{JobActivity: customers.CustomerJobsNone}, want: []string{"Historisch"}},
			{name: "email preference", filter: customers.CustomerListFilter{NotificationPreference: customers.NotifyEmail}, want: []string{"Aktiv"}},
			{name: "no notification", filter: customers.CustomerListFilter{NotificationPreference: customers.NotifyNone}, want: []string{"Fehlt"}},
			{name: "sms preference", filter: customers.CustomerListFilter{NotificationPreference: customers.NotifySMS}, want: []string{"Historisch"}},
			{name: "locality", filter: customers.CustomerListFilter{Locality: "nord"}, want: []string{"Aktiv"}},
			{name: "region", filter: customers.CustomerListFilter{Region: "nord"}, want: []string{"Aktiv", "Historisch"}},
			{name: "combined gaps", filter: customers.CustomerListFilter{MissingContact: true, IncompleteAddress: true, JobActivity: customers.CustomerJobsActive}, want: []string{"Fehlt"}},
		}
		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				test.filter.Sort = "name"
				test.filter.Direction = "asc"
				test.filter.Page = 1
				test.filter.PageSize = 25
				page, listErr := service.ListCustomers(ctx, admin, test.filter)
				if listErr != nil {
					t.Fatal(listErr)
				}
				got := make([]string, 0, len(page.Items))
				for _, item := range page.Items {
					got = append(got, item.LastName)
				}
				if fmt.Sprint(got) != fmt.Sprint(test.want) || page.Total != int64(len(test.want)) {
					t.Fatalf("ListCustomers() names/total = %v/%d, want %v/%d", got, page.Total, test.want, len(test.want))
				}
			})
		}
	})

	t.Run("preference mode and priority reason survive persistence constraints", func(t *testing.T) {
		ctx, pool, service, admin, _ := customerFixture(t)
		created, err := service.CreateIntake(ctx, admin, customerIntake("Petra", "Planbar", "60", customers.UrgencyNormal, "Nord"), "request-preference")
		if err != nil {
			t.Fatal(err)
		}
		detail, err := service.CustomerDetail(ctx, admin, created.CustomerID)
		if err != nil {
			t.Fatal(err)
		}
		job := customerIntake("", "", "60", customers.UrgencyNormal, "Nord").Job
		job.PreferenceMode = customers.PreferenceFixed
		job.PreferredStartDate, job.PreferredEndDate = "2026-09-03", "2026-09-03"
		if err := service.UpdateJob(ctx, admin, customers.UpdateJobInput{ID: created.JobID, ExpectedVersion: detail.Jobs[0].Version, Job: job, RequestID: "request-fixed"}); err != nil {
			t.Fatal(err)
		}
		updated, err := service.CustomerDetail(ctx, admin, created.CustomerID)
		if err != nil || updated.Jobs[0].PreferenceMode != customers.PreferenceFixed {
			t.Fatalf("persisted preference=%q error=%v", updated.Jobs[0].PreferenceMode, err)
		}
		_, err = pool.Exec(ctx, "UPDATE jobs SET preferred_start_date='2026-09-03', preferred_end_date='2026-09-04' WHERE id=$1", created.JobID)
		assertPostgresCode(t, err, "23514")
		_, err = pool.Exec(ctx, "UPDATE waitlist_entries SET manual_priority=10, priority_reason='' WHERE id=$1", created.WaitlistID)
		assertPostgresCode(t, err, "23514")
		if err := service.UpdateWaitlistPriority(ctx, admin, created.WaitlistID, 10, "Fixtermin bevorzugt", 1, "request-priority"); err != nil {
			t.Fatal(err)
		}
		var priority int32
		var reason string
		if err := pool.QueryRow(ctx, "SELECT manual_priority,priority_reason FROM waitlist_entries WHERE id=$1", created.WaitlistID).Scan(&priority, &reason); err != nil || priority != 10 || reason != "Fixtermin bevorzugt" {
			t.Fatalf("persisted priority/reason=%d/%q error=%v", priority, reason, err)
		}
	})

	t.Run("waitlist filter favorite round trips every visible filter", func(t *testing.T) {
		ctx, _, service, admin, _ := customerFixture(t)
		filter := customers.WaitlistFilter{
			JobType: string(customers.JobTypeChippingWithTransport), Region: "Nord", Urgency: string(customers.UrgencyUrgent),
			PreferredMonth: "2026-10", Workflow: "proposal", DurationGroup: "long",
			MissingLocation: true, DurationIssue: true, Overdue: true, Unassigned: true, TransportPending: true, Incomplete: true,
			Sort: "duration", Direction: "desc",
		}
		if err := service.SaveWaitlistFilterFavorite(ctx, admin, "Disposition", filter); err != nil {
			t.Fatal(err)
		}
		favorites, err := service.ListWaitlistFilterFavorites(ctx, admin)
		if err != nil || len(favorites) != 1 {
			t.Fatalf("favorites=%#v error=%v", favorites, err)
		}
		got := favorites[0].Filter
		if got.DurationGroup != "long" || !got.MissingLocation || !got.DurationIssue || !got.Overdue ||
			!got.Unassigned || !got.TransportPending || !got.Incomplete || got.Sort != "duration" || got.Direction != "desc" {
			t.Fatalf("favorite filter=%#v", got)
		}
	})
}

func customerFixture(t *testing.T) (context.Context, *pgxpool.Pool, *customers.Service, auth.Actor, auth.Actor) {
	t.Helper()
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Fatal("TEST_DATABASE_URL is required for integration tests")
	}
	ctx := t.Context()
	if err := migrate.Run(ctx, databaseURL, migrate.DirectionUp, io.Discard); err != nil {
		t.Fatal(err)
	}
	// Reapply the Task-02 migration so changes to an unreleased migration are
	// exercised even when a developer reuses an existing integration database.
	if err := migrate.Run(ctx, databaseURL, migrate.DirectionDown, io.Discard); err != nil {
		t.Fatal(err)
	}
	if err := migrate.Run(ctx, databaseURL, migrate.DirectionUp, io.Discard); err != nil {
		t.Fatal(err)
	}
	pool, err := postgres.Open(ctx, config.Database{
		URL: databaseURL, MaxConnections: 24, MinConnections: 0,
		ConnectTimeout: 5 * time.Second, ReadinessTimeout: 2 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if _, err := pool.Exec(ctx, `TRUNCATE job_notes, waitlist_entries, jobs, job_number_counters, customers,
		audit_events, auth_rate_limits, sessions, drivers, users RESTART IDENTITY CASCADE`); err != nil {
		t.Fatal(err)
	}
	admin := auth.Actor{Role: auth.RoleAdmin, DisplayName: "Anna Admin"}
	driver := auth.Actor{Role: auth.RoleDriver, DisplayName: "Franz Fahrer"}
	if err := pool.QueryRow(ctx, `INSERT INTO users (username, display_name, role, password_hash, must_change_password)
		VALUES ('integration-admin', 'Anna Admin', 'admin', 'not-used', false) RETURNING id::text`).Scan(&admin.UserID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO users (username, display_name, role, password_hash, must_change_password)
		VALUES ('integration-driver', 'Franz Fahrer', 'driver', 'not-used', false) RETURNING id::text`).Scan(&driver.UserID); err != nil {
		t.Fatal(err)
	}
	service, err := customers.NewService(postgres.NewCustomerStore(pool))
	if err != nil {
		t.Fatal(err)
	}
	return ctx, pool, service, admin, driver
}

func customerIntake(firstName, lastName, volume string, urgency customers.Urgency, region string) customers.IntakeInput {
	return customers.IntakeInput{
		Customer: customers.CustomerInput{
			FirstName: firstName, LastName: lastName, Street: "Teststraße 1", PostalCode: "4710",
			Locality: region, Region: region, CountryCode: "AT", NotificationPreference: customers.NotifyNone,
		},
		Job: customers.JobInput{
			JobType: customers.JobTypeChippingOnly, VolumeM3: volume, EstimatedHackMinutes: 120,
			TransportMode: customers.TransportNone, PreferenceMode: customers.PreferenceWindow, Urgency: urgency, Region: region, Source: customers.SourcePhone,
		},
	}
}

func assertTableCount(t *testing.T, ctx context.Context, pool *pgxpool.Pool, table string, expected int) {
	t.Helper()
	queries := map[string]string{
		"customers":           "SELECT count(*) FROM customers",
		"jobs":                "SELECT count(*) FROM jobs",
		"waitlist_entries":    "SELECT count(*) FROM waitlist_entries",
		"job_notes":           "SELECT count(*) FROM job_notes",
		"job_number_counters": "SELECT count(*) FROM job_number_counters",
	}
	query, ok := queries[table]
	if !ok {
		t.Fatalf("table %q is not allowlisted", table)
	}
	var actual int
	if err := pool.QueryRow(ctx, query).Scan(&actual); err != nil {
		t.Fatal(err)
	}
	if actual != expected {
		t.Fatalf("%s count = %d, want %d", table, actual, expected)
	}
}

func assertPostgresCode(t *testing.T, err error, expected string) {
	t.Helper()
	var postgresError *pgconn.PgError
	if !errors.As(err, &postgresError) || postgresError.Code != expected {
		t.Fatalf("PostgreSQL error = %v, want code %s", err, expected)
	}
}
