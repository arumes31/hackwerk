//go:build integration

package integration_test

import (
	"context"
	"errors"
	"io"
	"os"
	"sync"
	"testing"
	"time"

	"example.invalid/hackplan/internal/adapters/postgres"
	"example.invalid/hackplan/internal/adapters/postgres/migrate"
	"example.invalid/hackplan/internal/appointment"
	"example.invalid/hackplan/internal/auth"
	"example.invalid/hackplan/internal/config"
	"example.invalid/hackplan/internal/driver"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestCalendarReservationsAndAtomicFix(t *testing.T) {
	fixture := newCalendarFixture(t)
	start := time.Date(2026, 9, 1, 6, 0, 0, 0, time.UTC) // 08:00 Europe/Vienna.

	first := fixture.proposal(t, fixture.job(t, "HW-2026-0101"), fixture.driver1, fixture.chipper1, start, 3*time.Hour)
	adjacent := fixture.proposal(t, fixture.job(t, "HW-2026-0102"), fixture.driver1, fixture.chipper1, start.Add(3*time.Hour), 150*time.Minute)
	if adjacent.Lifecycle != appointment.LifecycleProposal {
		t.Fatalf("adjacent lifecycle = %s", adjacent.Lifecycle)
	}

	overlapJob := fixture.job(t, "HW-2026-0103")
	overlap := fixture.draftAssigned(t, overlapJob, fixture.driver2, fixture.chipper1, start.Add(150*time.Minute), 2*time.Hour)
	_, err := fixture.service.ProposeAppointment(fixture.ctx, fixture.admin, appointment.MutateInput{ID: overlap.ID, ExpectedVersion: overlap.Version, RequestID: "overlap"}, "")
	if !errors.Is(err, appointment.ErrConflict) {
		t.Fatalf("overlapping proposal error = %v, want conflict", err)
	}
	_, err = fixture.service.ProposeAppointment(fixture.ctx, fixture.admin, appointment.MutateInput{ID: overlap.ID, ExpectedVersion: overlap.Version, RequestID: "overlap-override"}, "Verfügbarkeit ausdrücklich bestätigt")
	if !errors.Is(err, appointment.ErrConflict) {
		t.Fatalf("physical overlap with override error = %v, want conflict", err)
	}

	secondMachine := fixture.proposal(t, fixture.job(t, "HW-2026-0104"), fixture.driver2, fixture.chipper2, start.Add(time.Hour), 90*time.Minute)
	if secondMachine.Lifecycle != appointment.LifecycleProposal {
		t.Fatalf("second machine lifecycle = %s", secondMachine.Lifecycle)
	}

	if _, err := fixture.service.CreateDraftFromWaitlist(fixture.ctx, fixture.admin, appointment.CreateDraftInput{
		JobID: first.JobID, RequestID: "second-active", Time: appointment.TimeInput{StartsAt: start.Add(24 * time.Hour), EndsAt: start.Add(26 * time.Hour)},
	}); !errors.Is(err, appointment.ErrConflict) {
		t.Fatalf("second active appointment error = %v, want conflict", err)
	}

	fixed, err := fixture.service.FixAppointment(fixture.ctx, fixture.admin, appointment.MutateInput{ID: first.ID, ExpectedVersion: first.Version, RequestID: "fix"})
	if err != nil {
		t.Fatal(err)
	}
	if fixed.Lifecycle != appointment.LifecycleFixed || fixed.Confirmation != appointment.ConfirmationPending {
		t.Fatalf("fixed state = %s/%s", fixed.Lifecycle, fixed.Confirmation)
	}
	var workflow string
	var activeWaitlist, outboxCount, auditCount int
	if err := fixture.pool.QueryRow(fixture.ctx, "SELECT workflow_status FROM jobs WHERE id=$1", first.JobID).Scan(&workflow); err != nil {
		t.Fatal(err)
	}
	if err := fixture.pool.QueryRow(fixture.ctx, "SELECT count(*) FROM waitlist_entries WHERE job_id=$1 AND removed_at IS NULL", first.JobID).Scan(&activeWaitlist); err != nil {
		t.Fatal(err)
	}
	if err := fixture.pool.QueryRow(fixture.ctx, "SELECT count(*) FROM outbox_events WHERE aggregate_id=$1 AND event_type='appointment.fixed'", first.ID).Scan(&outboxCount); err != nil {
		t.Fatal(err)
	}
	if err := fixture.pool.QueryRow(fixture.ctx, "SELECT count(*) FROM audit_events WHERE object_id=$1 AND action='appointment.fixed'", first.ID).Scan(&auditCount); err != nil {
		t.Fatal(err)
	}
	if workflow != "scheduled" || activeWaitlist != 0 || outboxCount != 1 || auditCount != 1 {
		t.Fatalf("atomic fix workflow/waitlist/outbox/audit = %s/%d/%d/%d", workflow, activeWaitlist, outboxCount, auditCount)
	}

	moved, err := fixture.service.MoveAppointment(fixture.ctx, fixture.admin, appointment.MoveInput{
		MutateInput: appointment.MutateInput{ID: fixed.ID, ExpectedVersion: fixed.Version, RequestID: "move"},
		StartsAt:    start.Add(24 * time.Hour), EndsAt: start.Add(27 * time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	if moved.Confirmation != appointment.ConfirmationPending {
		t.Fatalf("moved confirmation = %s", moved.Confirmation)
	}
	_, err = fixture.service.MoveAppointment(fixture.ctx, fixture.admin, appointment.MoveInput{
		MutateInput: appointment.MutateInput{ID: fixed.ID, ExpectedVersion: fixed.Version, RequestID: "stale"},
		StartsAt:    start.Add(48 * time.Hour), EndsAt: start.Add(51 * time.Hour),
	})
	if !errors.Is(err, appointment.ErrConflict) {
		t.Fatalf("stale move error = %v, want conflict", err)
	}
}

func TestConcurrentFixIsIdempotentByVersionAndAtomic(t *testing.T) {
	fixture := newCalendarFixture(t)
	start := time.Date(2026, 9, 1, 6, 0, 0, 0, time.UTC)
	proposed := fixture.proposal(t, fixture.job(t, "HW-2026-0301"), fixture.driver1, fixture.chipper1, start, 3*time.Hour)
	gate := make(chan struct{})
	results := make(chan error, 2)
	var group sync.WaitGroup
	for range 2 {
		group.Add(1)
		go func() {
			defer group.Done()
			<-gate
			_, err := fixture.service.FixAppointment(fixture.ctx, fixture.admin, appointment.MutateInput{ID: proposed.ID, ExpectedVersion: proposed.Version, RequestID: "parallel-fix"})
			results <- err
		}()
	}
	close(gate)
	group.Wait()
	close(results)
	successes, conflicts := 0, 0
	for err := range results {
		if err == nil {
			successes++
		} else if errors.Is(err, appointment.ErrConflict) {
			conflicts++
		} else {
			t.Fatalf("parallel fix error = %v", err)
		}
	}
	var outbox, activeWaitlist int
	if err := fixture.pool.QueryRow(fixture.ctx, "SELECT count(*) FROM outbox_events WHERE aggregate_id=$1 AND event_type='appointment.fixed'", proposed.ID).Scan(&outbox); err != nil {
		t.Fatal(err)
	}
	if err := fixture.pool.QueryRow(fixture.ctx, "SELECT count(*) FROM waitlist_entries WHERE job_id=$1 AND removed_at IS NULL", proposed.JobID).Scan(&activeWaitlist); err != nil {
		t.Fatal(err)
	}
	if successes != 1 || conflicts != 1 || outbox != 1 || activeWaitlist != 0 {
		t.Fatalf("parallel fix successes/conflicts/outbox/waitlist = %d/%d/%d/%d", successes, conflicts, outbox, activeWaitlist)
	}
}

func TestFixRejectsDeactivatedAssignmentWithoutSideEffects(t *testing.T) {
	fixture := newCalendarFixture(t)
	start := time.Date(2026, 9, 1, 6, 0, 0, 0, time.UTC)
	proposed := fixture.proposal(t, fixture.job(t, "HW-2026-0302"), fixture.driver1, fixture.chipper1, start, 3*time.Hour)
	if _, err := fixture.pool.Exec(fixture.ctx, "UPDATE drivers SET active=false WHERE id=$1", fixture.driver1); err != nil {
		t.Fatal(err)
	}
	_, err := fixture.service.FixAppointment(fixture.ctx, fixture.admin, appointment.MutateInput{ID: proposed.ID, ExpectedVersion: proposed.Version, RequestID: "inactive-fix"})
	if !errors.Is(err, appointment.ErrValidation) {
		t.Fatalf("inactive assignment fix error = %v, want validation", err)
	}
	var lifecycle, workflow string
	var outbox int
	if err := fixture.pool.QueryRow(fixture.ctx, "SELECT a.lifecycle_status,j.workflow_status FROM appointments a JOIN jobs j ON j.id=a.job_id WHERE a.id=$1", proposed.ID).Scan(&lifecycle, &workflow); err != nil {
		t.Fatal(err)
	}
	if err := fixture.pool.QueryRow(fixture.ctx, "SELECT count(*) FROM outbox_events WHERE aggregate_id=$1", proposed.ID).Scan(&outbox); err != nil {
		t.Fatal(err)
	}
	if lifecycle != "proposal" || workflow != "planning" || outbox != 0 {
		t.Fatalf("failed fix lifecycle/workflow/outbox = %s/%s/%d", lifecycle, workflow, outbox)
	}
}

func TestConcurrentProposalsHaveOneWinner(t *testing.T) {
	fixture := newCalendarFixture(t)
	start := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	left := fixture.draftAssigned(t, fixture.job(t, "HW-2026-0201"), fixture.driver1, fixture.chipper1, start, 2*time.Hour)
	right := fixture.draftAssigned(t, fixture.job(t, "HW-2026-0202"), fixture.driver2, fixture.chipper1, start.Add(30*time.Minute), 2*time.Hour)

	gate := make(chan struct{})
	results := make(chan error, 2)
	var group sync.WaitGroup
	for _, value := range []appointment.Appointment{left, right} {
		group.Add(1)
		go func(candidate appointment.Appointment) {
			defer group.Done()
			<-gate
			_, err := fixture.service.ProposeAppointment(fixture.ctx, fixture.admin, appointment.MutateInput{ID: candidate.ID, ExpectedVersion: candidate.Version, RequestID: "parallel"}, "")
			results <- err
		}(value)
	}
	close(gate)
	group.Wait()
	close(results)
	successes, conflicts := 0, 0
	for err := range results {
		if err == nil {
			successes++
		} else if errors.Is(err, appointment.ErrConflict) {
			conflicts++
		} else {
			t.Fatalf("parallel proposal error = %v", err)
		}
	}
	var active int
	if err := fixture.pool.QueryRow(fixture.ctx, "SELECT count(*) FROM appointment_resources WHERE resource_id=$1 AND active", fixture.chipper1).Scan(&active); err != nil {
		t.Fatal(err)
	}
	if successes != 1 || conflicts != 1 || active != 1 {
		t.Fatalf("parallel successes/conflicts/active = %d/%d/%d", successes, conflicts, active)
	}
}

func TestConcurrentMovesIntoSameSlotHaveOneWinner(t *testing.T) {
	fixture := newCalendarFixture(t)
	start := time.Date(2026, 9, 1, 6, 0, 0, 0, time.UTC)
	left := fixture.proposal(t, fixture.job(t, "HW-2026-0251"), fixture.driver1, fixture.chipper1, start, 3*time.Hour)
	right := fixture.proposal(t, fixture.job(t, "HW-2026-0252"), fixture.driver1, fixture.chipper1, start.Add(3*time.Hour), 150*time.Minute)
	target := start.Add(6 * time.Hour)
	gate := make(chan struct{})
	results := make(chan error, 2)
	var group sync.WaitGroup
	for index, value := range []appointment.Appointment{left, right} {
		group.Add(1)
		go func(offset int, candidate appointment.Appointment) {
			defer group.Done()
			<-gate
			startsAt := target.Add(time.Duration(offset) * 30 * time.Minute)
			_, err := fixture.service.MoveAppointment(fixture.ctx, fixture.admin, appointment.MoveInput{
				MutateInput: appointment.MutateInput{ID: candidate.ID, ExpectedVersion: candidate.Version, RequestID: "parallel-move"},
				StartsAt: startsAt, EndsAt: startsAt.Add(3 * time.Hour),
			})
			results <- err
		}(index, value)
	}
	close(gate)
	group.Wait()
	close(results)
	successes, conflicts := 0, 0
	for err := range results {
		if err == nil {
			successes++
		} else if errors.Is(err, appointment.ErrConflict) {
			conflicts++
		} else {
			t.Fatalf("parallel move error = %v", err)
		}
	}
	var targetReservations int
	if err := fixture.pool.QueryRow(fixture.ctx, `SELECT count(*) FROM appointment_resources
		WHERE resource_id=$1 AND active AND reserved_range && tstzrange($2,$3,'[)')`, fixture.chipper1, target, target.Add(4*time.Hour)).Scan(&targetReservations); err != nil {
		t.Fatal(err)
	}
	if successes != 1 || conflicts != 1 || targetReservations != 1 {
		t.Fatalf("parallel move successes/conflicts/target reservations = %d/%d/%d", successes, conflicts, targetReservations)
	}
}

type calendarFixture struct {
	ctx                context.Context
	pool               *pgxpool.Pool
	service            *appointment.Service
	admin              auth.Actor
	driver1, driver2   string
	chipper1, chipper2 string
}

func newCalendarFixture(t *testing.T) calendarFixture {
	t.Helper()
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Fatal("TEST_DATABASE_URL is required for integration tests")
	}
	ctx := t.Context()
	if err := migrate.Run(ctx, databaseURL, migrate.DirectionUp, io.Discard); err != nil {
		t.Fatal(err)
	}
	pool, err := postgres.Open(ctx, config.Database{URL: databaseURL, MaxConnections: 16, ConnectTimeout: 5 * time.Second, ReadinessTimeout: 2 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if _, err := pool.Exec(ctx, "TRUNCATE outbox_events, appointments, waitlist_entries, jobs, customers, availability_exceptions, availability_rules, resources, audit_events, auth_rate_limits, sessions, drivers, users RESTART IDENTITY CASCADE"); err != nil {
		t.Fatal(err)
	}
	admin := auth.Actor{Role: auth.RoleAdmin, DisplayName: "Admin"}
	if err := pool.QueryRow(ctx, "INSERT INTO users (username, display_name, role, password_hash, must_change_password) VALUES ('calendar-admin', 'Admin', 'admin', 'not-used', false) RETURNING id::text").Scan(&admin.UserID); err != nil {
		t.Fatal(err)
	}
	var driver1, driver2 string
	if err := pool.QueryRow(ctx, "INSERT INTO drivers (display_name) VALUES ('Franz'), ('Maria') RETURNING id::text").Scan(&driver1); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, "SELECT id::text FROM drivers WHERE display_name='Maria'").Scan(&driver2); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "INSERT INTO availability_rules (driver_id, iso_weekday, local_start, local_end, valid_from, status) VALUES ($1, 2, '06:00', '20:00', '2026-01-01', 'available'), ($2, 2, '06:00', '20:00', '2026-01-01', 'available'), ($1, 3, '06:00', '20:00', '2026-01-01', 'available')", driver1, driver2); err != nil {
		t.Fatal(err)
	}
	var chipper1, chipper2 string
	if err := pool.QueryRow(ctx, "INSERT INTO resources (resource_type, name, exclusive) VALUES ('chipper', 'Hackmaschine 1', true) RETURNING id::text").Scan(&chipper1); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, "INSERT INTO resources (resource_type, name, exclusive) VALUES ('chipper', 'Hackmaschine 2', true) RETURNING id::text").Scan(&chipper2); err != nil {
		t.Fatal(err)
	}
	location, err := time.LoadLocation("Europe/Vienna")
	if err != nil {
		t.Fatal(err)
	}
	drivers, err := driver.New(postgres.NewDriverStore(pool), location)
	if err != nil {
		t.Fatal(err)
	}
	service, err := appointment.New(postgres.NewAppointmentStore(pool), drivers, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	return calendarFixture{ctx: ctx, pool: pool, service: service, admin: admin, driver1: driver1, driver2: driver2, chipper1: chipper1, chipper2: chipper2}
}

func (fixture calendarFixture) job(t *testing.T, number string) string {
	t.Helper()
	var customerID, jobID string
	if err := fixture.pool.QueryRow(fixture.ctx, "INSERT INTO customers (first_name, last_name, street, postal_code, locality) VALUES ('Test', $1, 'Waldweg 1', '4710', 'Grieskirchen') RETURNING id::text", number).Scan(&customerID); err != nil {
		t.Fatal(err)
	}
	if err := fixture.pool.QueryRow(fixture.ctx, "INSERT INTO jobs (job_number, customer_id, job_type, volume_m3, estimated_hack_minutes) VALUES ($1, $2, 'chipping_only', 30, 180) RETURNING id::text", number, customerID).Scan(&jobID); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.pool.Exec(fixture.ctx, "INSERT INTO waitlist_entries (job_id) VALUES ($1)", jobID); err != nil {
		t.Fatal(err)
	}
	return jobID
}

func (fixture calendarFixture) draftAssigned(t *testing.T, jobID, driverID, resourceID string, start time.Time, duration time.Duration) appointment.Appointment {
	t.Helper()
	draft, err := fixture.service.CreateDraftFromWaitlist(fixture.ctx, fixture.admin, appointment.CreateDraftInput{
		JobID: jobID, RequestID: "draft", Time: appointment.TimeInput{StartsAt: start, EndsAt: start.Add(duration)},
	})
	if err != nil {
		t.Fatal(err)
	}
	assigned, err := fixture.service.AssignDriversAndResources(fixture.ctx, fixture.admin, appointment.AssignInput{
		MutateInput: appointment.MutateInput{ID: draft.ID, ExpectedVersion: draft.Version, RequestID: "assign"},
		Assignments: appointment.AssignmentInput{
			DriverIDs: []string{driverID}, PrimaryDriverID: driverID,
			Resources: []appointment.ResourceAssignment{{ID: resourceID, Purpose: appointment.PurposeChipping}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return assigned
}

func (fixture calendarFixture) proposal(t *testing.T, jobID, driverID, resourceID string, start time.Time, duration time.Duration) appointment.Appointment {
	t.Helper()
	assigned := fixture.draftAssigned(t, jobID, driverID, resourceID, start, duration)
	proposed, err := fixture.service.ProposeAppointment(fixture.ctx, fixture.admin, appointment.MutateInput{
		ID: assigned.ID, ExpectedVersion: assigned.Version, RequestID: "propose",
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	return proposed
}
