//go:build integration

package integration_test

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"example.invalid/hackplan/internal/adapters/postgres"
	"example.invalid/hackplan/internal/adapters/postgres/migrate"
	"example.invalid/hackplan/internal/appointment"
	"example.invalid/hackplan/internal/auth"
	"example.invalid/hackplan/internal/config"
	"example.invalid/hackplan/internal/customers"
	"example.invalid/hackplan/internal/driver"
	"example.invalid/hackplan/internal/resource"
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

	fixed, err := fixture.service.FixAppointment(fixture.ctx, fixture.admin, appointment.FixInput{MutateInput: appointment.MutateInput{ID: first.ID, ExpectedVersion: first.Version, RequestID: "fix"}})
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

func TestPlanFromWaitlistCommitsProposalAtomicallyAndRollsBackLateConflict(t *testing.T) {
	fixture := newCalendarFixture(t)
	start := time.Date(2026, 9, 1, 6, 0, 0, 0, time.UTC)
	plan := func(jobID, driverID, requestID string) (appointment.Appointment, error) {
		return fixture.service.PlanFromWaitlist(fixture.ctx, fixture.admin, appointment.PlanInput{
			CreateDraftInput: appointment.CreateDraftInput{
				JobID: jobID, RequestID: requestID,
				Time: appointment.TimeInput{StartsAt: start, EndsAt: start.Add(90 * time.Minute)},
			},
			Assignments: appointment.AssignmentInput{
				DriverIDs: []string{driverID}, PrimaryDriverID: driverID,
				Resources: []appointment.ResourceAssignment{{ID: fixture.chipper1, Purpose: appointment.PurposeChipping}},
			},
		})
	}

	jobID := fixture.job(t, "HW-2026-ATOMIC-PLAN")
	proposed, err := plan(jobID, fixture.driver1, "atomic-plan-success")
	if err != nil {
		t.Fatal(err)
	}
	var lifecycle, workflow string
	var drivers, resources, audits, outbox int
	if err := fixture.pool.QueryRow(fixture.ctx, `SELECT a.lifecycle_status, j.workflow_status,
		(SELECT count(*) FROM appointment_drivers WHERE appointment_id=a.id),
		(SELECT count(*) FROM appointment_resources WHERE appointment_id=a.id),
		(SELECT count(*) FROM audit_events WHERE object_id=a.id::text),
		(SELECT count(*) FROM outbox_events WHERE aggregate_id=a.id)
		FROM appointments a JOIN jobs j ON j.id=a.job_id WHERE a.id=$1`, proposed.ID).
		Scan(&lifecycle, &workflow, &drivers, &resources, &audits, &outbox); err != nil {
		t.Fatal(err)
	}
	if lifecycle != "proposal" || workflow != "planning" || drivers != 1 || resources != 1 || audits != 3 || outbox != 0 {
		t.Fatalf("atomic plan state=%s/%s drivers=%d resources=%d audits=%d outbox=%d", lifecycle, workflow, drivers, resources, audits, outbox)
	}

	failedJobID := fixture.job(t, "HW-2026-ATOMIC-ROLLBACK")
	var auditBefore int
	if err := fixture.pool.QueryRow(fixture.ctx, "SELECT count(*) FROM audit_events").Scan(&auditBefore); err != nil {
		t.Fatal(err)
	}
	if _, err := plan(failedJobID, fixture.driver2, "atomic-plan-conflict"); !errors.Is(err, appointment.ErrConflict) {
		t.Fatalf("conflicting atomic plan error=%v want conflict", err)
	}
	var appointmentCount, auditAfter, activeWaitlist int
	if err := fixture.pool.QueryRow(fixture.ctx, "SELECT workflow_status FROM jobs WHERE id=$1", failedJobID).Scan(&workflow); err != nil {
		t.Fatal(err)
	}
	if err := fixture.pool.QueryRow(fixture.ctx, "SELECT count(*) FROM appointments WHERE job_id=$1", failedJobID).Scan(&appointmentCount); err != nil {
		t.Fatal(err)
	}
	if err := fixture.pool.QueryRow(fixture.ctx, "SELECT count(*) FROM waitlist_entries WHERE job_id=$1 AND removed_at IS NULL", failedJobID).Scan(&activeWaitlist); err != nil {
		t.Fatal(err)
	}
	if err := fixture.pool.QueryRow(fixture.ctx, "SELECT count(*) FROM audit_events").Scan(&auditAfter); err != nil {
		t.Fatal(err)
	}
	if workflow != "waitlist" || appointmentCount != 0 || activeWaitlist != 1 || auditAfter != auditBefore {
		t.Fatalf("rollback state workflow=%s appointments=%d waitlist=%d audits=%d/%d", workflow, appointmentCount, activeWaitlist, auditAfter, auditBefore)
	}
}

func TestScheduledJobEditPreservesAndRevalidatesFixedAppointment(t *testing.T) {
	fixture := newCalendarFixture(t)
	start := time.Date(2026, 9, 1, 6, 0, 0, 0, time.UTC)
	jobID := fixture.job(t, "HW-2026-EDIT-FIXED")
	proposed := fixture.proposal(t, jobID, fixture.driver1, fixture.chipper1, start, 3*time.Hour)
	fixed, err := fixture.service.FixAppointment(fixture.ctx, fixture.admin, appointment.FixInput{MutateInput: appointment.MutateInput{
		ID: proposed.ID, ExpectedVersion: proposed.Version, RequestID: "fix-before-job-edit",
	}})
	if err != nil {
		t.Fatal(err)
	}
	var customerID string
	if err := fixture.pool.QueryRow(fixture.ctx, "SELECT customer_id::text FROM jobs WHERE id=$1", jobID).Scan(&customerID); err != nil {
		t.Fatal(err)
	}
	customerService, err := customers.NewService(postgres.NewCustomerStore(fixture.pool))
	if err != nil {
		t.Fatal(err)
	}
	detail, err := customerService.CustomerDetail(fixture.ctx, fixture.admin, customerID)
	if err != nil {
		t.Fatal(err)
	}
	latitude, longitude := 46.712345, 15.56789
	update := customers.UpdateJobInput{
		ID: jobID, ExpectedVersion: detail.Jobs[0].Version, RequestID: "edit-scheduled-job",
		Job: customers.JobInput{
			JobType: customers.JobTypeChippingOnly, VolumeM3: "42.50", EstimatedHackMinutes: 90,
			TransportMode: customers.TransportNone, PreferredStartDate: "2026-09-01", PreferredEndDate: "2026-09-30",
			PreferenceText: "Zufahrt vorher prüfen", Urgency: customers.UrgencyHigh, Region: "Süd", Source: customers.SourceEmail,
			PileLatitude: &latitude, PileLongitude: &longitude, PileLocationSource: customers.PileSourceMapPin,
		},
	}
	if err := customerService.UpdateJob(fixture.ctx, fixture.admin, update); err != nil {
		t.Fatalf("update compatible scheduled job: %v", err)
	}

	var lifecycle, workflow, confirmation, volume, region, source, pileSource string
	var appointmentStart, appointmentEnd, driverFrom, driverTo, resourceFrom, resourceTo time.Time
	var hackMinutes, activeConfirmations int
	if err := fixture.pool.QueryRow(fixture.ctx, `SELECT a.lifecycle_status,j.workflow_status,a.confirmation_status,
		j.volume_m3::text,j.estimated_hack_minutes,j.region,j.source,j.pile_location_source,
		a.starts_at,a.ends_at,ad.reserved_starts_at,ad.reserved_ends_at,ar.reserved_starts_at,ar.reserved_ends_at,
		(SELECT count(*) FROM confirmation_requests cr WHERE cr.appointment_id=a.id AND cr.status='active')
		FROM appointments a JOIN jobs j ON j.id=a.job_id
		JOIN appointment_drivers ad ON ad.appointment_id=a.id
		JOIN appointment_resources ar ON ar.appointment_id=a.id
		WHERE a.id=$1`, fixed.ID).Scan(
		&lifecycle, &workflow, &confirmation, &volume, &hackMinutes, &region, &source, &pileSource,
		&appointmentStart, &appointmentEnd, &driverFrom, &driverTo, &resourceFrom, &resourceTo, &activeConfirmations,
	); err != nil {
		t.Fatal(err)
	}
	if lifecycle != "fixed" || workflow != "scheduled" || confirmation != "not_requested" || volume != "42.50" || hackMinutes != 90 ||
		region != "Süd" || source != "email" || pileSource != "map_pin" || !appointmentStart.Equal(start) || !appointmentEnd.Equal(start.Add(3*time.Hour)) ||
		!driverFrom.Equal(start) || !driverTo.Equal(start.Add(3*time.Hour)) || !resourceFrom.Equal(start) || !resourceTo.Equal(start.Add(3*time.Hour)) || activeConfirmations != 0 {
		t.Fatalf("scheduled edit state = lifecycle=%s workflow=%s confirmation=%s volume=%s minutes=%d region=%s source=%s pile=%s appointment=%s..%s driver=%s..%s resource=%s..%s active confirmations=%d",
			lifecycle, workflow, confirmation, volume, hackMinutes, region, source, pileSource, appointmentStart, appointmentEnd, driverFrom, driverTo, resourceFrom, resourceTo, activeConfirmations)
	}
	appointmentDetail, err := fixture.service.AppointmentDetail(fixture.ctx, fixture.admin, fixed.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(appointmentDetail.MapsURL, "46.712345%2C15.567890") {
		t.Fatalf("appointment navigation does not prefer updated pile location: %q", appointmentDetail.MapsURL)
	}

	refreshed, err := customerService.CustomerDetail(fixture.ctx, fixture.admin, customerID)
	if err != nil {
		t.Fatal(err)
	}
	tooLong := update
	tooLong.ExpectedVersion = refreshed.Jobs[0].Version
	tooLong.Job.EstimatedHackMinutes = 181
	if err := customerService.UpdateJob(fixture.ctx, fixture.admin, tooLong); !errors.Is(err, customers.ErrConflict) {
		t.Fatalf("oversized scheduled job update error = %v, want conflict", err)
	}
	missingTransport := update
	missingTransport.ExpectedVersion = refreshed.Jobs[0].Version
	missingTransport.Job.JobType = customers.JobTypeChippingWithTransport
	missingTransport.Job.TransportMode = customers.TransportInternal
	missingTransport.Job.EstimatedTransportMinutes = 30
	if err := customerService.UpdateJob(fixture.ctx, fixture.admin, missingTransport); !errors.Is(err, customers.ErrConflict) {
		t.Fatalf("scheduled internal transport without resource error = %v, want conflict", err)
	}
}

func TestRescheduleMayOverlapOwnPreviousReservation(t *testing.T) {
	fixture := newCalendarFixture(t)
	start := time.Date(2026, 9, 8, 6, 0, 0, 0, time.UTC)
	proposed := fixture.proposal(
		t,
		fixture.job(t, "HW-2026-SELF-OVERLAP"),
		fixture.driver1,
		fixture.chipper1,
		start,
		3*time.Hour,
	)

	moved, err := fixture.service.MoveAppointment(fixture.ctx, fixture.admin, appointment.MoveInput{
		MutateInput: appointment.MutateInput{
			ID:              proposed.ID,
			ExpectedVersion: proposed.Version,
			RequestID:       "move-inside-own-reservation",
		},
		StartsAt: start.Add(30 * time.Minute),
		EndsAt:   start.Add(3*time.Hour + 30*time.Minute),
	})
	if err != nil {
		t.Fatalf("move overlapping own previous reservation: %v", err)
	}

	resized, err := fixture.service.ResizeAppointment(fixture.ctx, fixture.admin, appointment.ResizeInput{
		MutateInput: appointment.MutateInput{
			ID:              moved.ID,
			ExpectedVersion: moved.Version,
			RequestID:       "resize-inside-own-reservation",
		},
		StartsAt: moved.StartsAt,
		EndsAt:   moved.EndsAt.Add(30 * time.Minute),
	})
	if err != nil {
		t.Fatalf("resize overlapping own previous reservation: %v", err)
	}
	if !resized.StartsAt.Equal(start.Add(30*time.Minute)) || !resized.EndsAt.Equal(start.Add(4*time.Hour)) {
		t.Fatalf("resized range = %s..%s", resized.StartsAt, resized.EndsAt)
	}

	var driverStart, driverEnd, resourceStart, resourceEnd time.Time
	if err := fixture.pool.QueryRow(fixture.ctx, `
		SELECT d.reserved_starts_at, d.reserved_ends_at,
		       r.reserved_starts_at, r.reserved_ends_at
		FROM appointment_drivers d
		JOIN appointment_resources r ON r.appointment_id = d.appointment_id
		WHERE d.appointment_id = $1`, proposed.ID).Scan(
		&driverStart,
		&driverEnd,
		&resourceStart,
		&resourceEnd,
	); err != nil {
		t.Fatal(err)
	}
	if !driverStart.Equal(resized.StartsAt) || !driverEnd.Equal(resized.EndsAt) ||
		!resourceStart.Equal(resized.StartsAt) || !resourceEnd.Equal(resized.EndsAt) {
		t.Fatalf(
			"reservations not refreshed: driver=%s..%s resource=%s..%s appointment=%s..%s",
			driverStart,
			driverEnd,
			resourceStart,
			resourceEnd,
			resized.StartsAt,
			resized.EndsAt,
		)
	}

	blocker := fixture.proposal(
		t,
		fixture.job(t, "HW-2026-REAL-OVERLAP"),
		fixture.driver1,
		fixture.chipper1,
		resized.EndsAt,
		3*time.Hour,
	)
	if blocker.Lifecycle != appointment.LifecycleProposal {
		t.Fatalf("blocker lifecycle = %s", blocker.Lifecycle)
	}
	_, err = fixture.service.ResizeAppointment(fixture.ctx, fixture.admin, appointment.ResizeInput{
		MutateInput: appointment.MutateInput{
			ID:              resized.ID,
			ExpectedVersion: resized.Version,
			RequestID:       "resize-into-other-reservation",
		},
		StartsAt: resized.StartsAt,
		EndsAt:   resized.EndsAt.Add(15 * time.Minute),
	})
	if !errors.Is(err, appointment.ErrConflict) {
		t.Fatalf("resize into other appointment error = %v, want conflict", err)
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
			_, err := fixture.service.FixAppointment(fixture.ctx, fixture.admin, appointment.FixInput{MutateInput: appointment.MutateInput{ID: proposed.ID, ExpectedVersion: proposed.Version, RequestID: "parallel-fix"}})
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
	_, err := fixture.service.FixAppointment(fixture.ctx, fixture.admin, appointment.FixInput{MutateInput: appointment.MutateInput{ID: proposed.ID, ExpectedVersion: proposed.Version, RequestID: "inactive-fix"}})
	if !errors.Is(err, appointment.ErrValidation) && !errors.Is(err, appointment.ErrAvailability) {
		t.Fatalf("inactive assignment fix error = %v, want validation or availability rejection", err)
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

func TestAppointmentStoreRevalidatesAvailabilityInsideFixTransaction(t *testing.T) {
	fixture := newCalendarFixture(t)
	start := time.Date(2026, 9, 1, 6, 0, 0, 0, time.UTC)
	proposed := fixture.proposal(t, fixture.job(t, "HW-2026-0303"), fixture.driver1, fixture.chipper1, start, 3*time.Hour)
	if _, err := fixture.pool.Exec(fixture.ctx, `INSERT INTO availability_exceptions
		(driver_id, exception_type, all_day, starts_at, ends_at)
		VALUES ($1,'vacation',false,$2,$3)`, fixture.driver1, start, start.Add(3*time.Hour)); err != nil {
		t.Fatal(err)
	}
	store := postgres.NewAppointmentStore(fixture.pool)
	_, err := store.Fix(fixture.ctx, fixture.admin, appointment.FixInput{MutateInput: appointment.MutateInput{
		ID: proposed.ID, ExpectedVersion: proposed.Version, RequestID: "transactional-availability",
	}})
	if !errors.Is(err, appointment.ErrAvailability) {
		t.Fatalf("store fix error=%v want availability rejection", err)
	}
	var lifecycle string
	if err := fixture.pool.QueryRow(fixture.ctx, "SELECT lifecycle_status FROM appointments WHERE id=$1", proposed.ID).Scan(&lifecycle); err != nil {
		t.Fatal(err)
	}
	if lifecycle != "proposal" {
		t.Fatalf("failed transactional revalidation changed lifecycle to %q", lifecycle)
	}
}

func TestProposalRejectsDurationShorterThanJobEstimate(t *testing.T) {
	fixture := newCalendarFixture(t)
	jobID := fixture.job(t, "HW-2026-0304")
	if _, err := fixture.pool.Exec(fixture.ctx, "UPDATE jobs SET estimated_hack_minutes=180 WHERE id=$1", jobID); err != nil {
		t.Fatal(err)
	}
	assigned := fixture.draftAssigned(t, jobID, fixture.driver1, fixture.chipper1, time.Date(2026, 9, 1, 6, 0, 0, 0, time.UTC), 2*time.Hour)
	_, err := fixture.service.ProposeAppointment(fixture.ctx, fixture.admin, appointment.MutateInput{
		ID: assigned.ID, ExpectedVersion: assigned.Version, RequestID: "short-duration",
	}, "")
	if !errors.Is(err, appointment.ErrValidation) {
		t.Fatalf("short proposal error=%v want validation", err)
	}
}

func TestActiveResourceRejectsCriticalMutationAndDeactivation(t *testing.T) {
	fixture := newCalendarFixture(t)
	_ = fixture.proposal(t, fixture.job(t, "HW-2026-0305"), fixture.driver1, fixture.chipper1, time.Date(2026, 9, 1, 6, 0, 0, 0, time.UTC), 3*time.Hour)
	service, err := resource.New(postgres.NewResourceStore(fixture.pool))
	if err != nil {
		t.Fatal(err)
	}
	values, err := service.List(fixture.ctx, fixture.admin)
	if err != nil {
		t.Fatal(err)
	}
	var current resource.Resource
	for _, value := range values {
		if value.ID == fixture.chipper1 {
			current = value
		}
	}
	input := resource.Input{Type: current.Type, Name: current.Name, IsExclusive: false, Capacity: current.Capacity, InternalNote: current.InternalNote}
	if err := service.Update(fixture.ctx, fixture.admin, current.ID, current.Version, input, "critical-update"); !errors.Is(err, resource.ErrConflict) {
		t.Fatalf("critical resource update error=%v want conflict", err)
	}
	if err := service.Deactivate(fixture.ctx, fixture.admin, current.ID, current.Version, "deactivate-active"); !errors.Is(err, resource.ErrConflict) {
		t.Fatalf("active resource deactivation error=%v want conflict", err)
	}
}

func TestActiveDriverRejectsDeactivation(t *testing.T) {
	fixture := newCalendarFixture(t)
	_ = fixture.proposal(t, fixture.job(t, "HW-2026-0306"), fixture.driver1, fixture.chipper1, time.Date(2026, 9, 1, 6, 0, 0, 0, time.UTC), 3*time.Hour)
	location, err := time.LoadLocation("Europe/Vienna")
	if err != nil {
		t.Fatal(err)
	}
	service, err := driver.New(postgres.NewDriverStore(fixture.pool), location)
	if err != nil {
		t.Fatal(err)
	}
	profiles, err := service.ListProfiles(fixture.ctx, fixture.admin)
	if err != nil {
		t.Fatal(err)
	}
	var version int32
	for _, profile := range profiles {
		if profile.ID == fixture.driver1 {
			version = profile.Version
			break
		}
	}
	if err := service.DeactivateProfile(fixture.ctx, fixture.admin, fixture.driver1, version, "deactivate-reserved"); !errors.Is(err, driver.ErrConflict) {
		t.Fatalf("active driver deactivation error=%v want conflict", err)
	}
	var active bool
	if err := fixture.pool.QueryRow(fixture.ctx, "SELECT active FROM drivers WHERE id=$1", fixture.driver1).Scan(&active); err != nil {
		t.Fatal(err)
	}
	if !active {
		t.Fatal("reserved driver was deactivated")
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
				StartsAt:    startsAt, EndsAt: startsAt.Add(3 * time.Hour),
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

func TestSwapProposalWindowsIsAtomicAndNotificationFree(t *testing.T) {
	fixture := newCalendarFixture(t)
	start := time.Date(2026, 9, 1, 6, 0, 0, 0, time.UTC)
	first := fixture.proposal(t, fixture.job(t, "HW-2026-SWAP-1"), fixture.driver1, fixture.chipper1, start, 90*time.Minute)
	second := fixture.proposal(t, fixture.job(t, "HW-2026-SWAP-2"), fixture.driver2, fixture.chipper2, start.Add(4*time.Hour), 2*time.Hour)

	values, err := fixture.service.SwapAppointments(fixture.ctx, fixture.admin, appointment.SwapInput{
		FirstID: first.ID, SecondID: second.ID, FirstVersion: first.Version, SecondVersion: second.Version, RequestID: "swap-integration",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(values) != 2 || !values[0].StartsAt.Equal(second.StartsAt) || !values[1].StartsAt.Equal(first.StartsAt) || values[0].Lifecycle != appointment.LifecycleProposal || values[1].Lifecycle != appointment.LifecycleProposal {
		t.Fatalf("swapped appointments = %#v", values)
	}
	var outboxCount int
	if err := fixture.pool.QueryRow(fixture.ctx, "SELECT count(*) FROM outbox_events").Scan(&outboxCount); err != nil {
		t.Fatal(err)
	}
	if outboxCount != 0 {
		t.Fatalf("swap created %d outbox events", outboxCount)
	}
	if _, err := fixture.service.SwapAppointments(fixture.ctx, fixture.admin, appointment.SwapInput{FirstID: first.ID, SecondID: second.ID, FirstVersion: first.Version, SecondVersion: second.Version}); !errors.Is(err, appointment.ErrConflict) {
		t.Fatalf("stale swap error = %v", err)
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
	if err := fixture.pool.QueryRow(fixture.ctx, "INSERT INTO customers (first_name, last_name, street, postal_code, locality, email, notification_preference) VALUES ('Test', $1, 'Waldweg 1', '4710', 'Grieskirchen', $1 || '@example.test', 'email') RETURNING id::text", number).Scan(&customerID); err != nil {
		t.Fatal(err)
	}
	if err := fixture.pool.QueryRow(fixture.ctx, "INSERT INTO jobs (job_number, customer_id, job_type, volume_m3, estimated_hack_minutes) VALUES ($1, $2, 'chipping_only', 30, 60) RETURNING id::text", number, customerID).Scan(&jobID); err != nil {
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
