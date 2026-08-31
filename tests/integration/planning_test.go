//go:build integration

package integration_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"example.invalid/hackplan/internal/adapters/postgres"
	"example.invalid/hackplan/internal/auth"
	"example.invalid/hackplan/internal/customers"
	"example.invalid/hackplan/internal/driver"
	"example.invalid/hackplan/internal/planning"
	"example.invalid/hackplan/internal/routelocation"
)

type integrationPlanningAvailability struct{ service *driver.Service }

type integrationPlanningStart struct{ store *postgres.RouteLocationStore }

func (s integrationPlanningStart) DefaultStart(ctx context.Context) (planning.Point, error) {
	location, err := s.store.DefaultStart(ctx)
	if err != nil {
		return planning.Point{}, err
	}
	return planning.Point{Latitude: location.Latitude, Longitude: location.Longitude}, nil
}

func (a integrationPlanningAvailability) Resolve(ctx context.Context, actor auth.Actor, driverID string, from, to time.Time) ([]planning.Interval, error) {
	values, err := a.service.ResolveAvailability(ctx, actor, driverID, from, to)
	if err != nil {
		return nil, err
	}
	result := make([]planning.Interval, 0, len(values))
	for _, value := range values {
		result = append(result, planning.Interval{StartsAt: value.StartsAt, EndsAt: value.EndsAt, Status: string(value.Status)})
	}
	return result, nil
}

func planningFixtureService(t *testing.T, fixture calendarFixture, now time.Time) *planning.Service {
	t.Helper()
	location, err := time.LoadLocation("Europe/Vienna")
	if err != nil {
		t.Fatal(err)
	}
	drivers, err := driver.New(postgres.NewDriverStore(fixture.pool), location)
	if err != nil {
		t.Fatal(err)
	}
	cfg := planning.DefaultConfig(location)
	cfg.HorizonDays = 56
	cfg.CandidateLimit = 2000
	starts := postgres.NewRouteLocationStore(fixture.pool)
	if _, err := starts.Create(fixture.ctx, fixture.admin, routelocation.Input{
		Label: "Betriebshof", Address: "Teststraße 1", Latitude: 48.2, Longitude: 14.2, DefaultStart: true,
	}, "planning-fixture"); err != nil {
		t.Fatal(err)
	}
	service, err := planning.New(postgres.NewPlanningStore(fixture.pool), integrationPlanningAvailability{service: drivers}, planning.NewHaversineRouter(1.3, 55), cfg, func() time.Time { return now }, planning.WithDefaultStartProvider(integrationPlanningStart{store: starts}))
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func planningJob(t *testing.T, fixture calendarFixture, number string) string {
	t.Helper()
	jobID := fixture.job(t, number)
	if _, err := fixture.pool.Exec(fixture.ctx, "UPDATE jobs SET pile_latitude=48.210000,pile_longitude=14.210000,pile_location_source='coordinates',pile_location_updated_at=now() WHERE id=$1", jobID); err != nil {
		t.Fatal(err)
	}
	return jobID
}

func TestPlanningRunWithoutSuggestionsRemainsLoadable(t *testing.T) {
	fixture := newCalendarFixture(t)
	now := time.Date(2026, 8, 31, 4, 0, 0, 0, time.UTC)
	jobID := planningJob(t, fixture, "HW-PLAN-EMPTY")
	from, to := now.Add(time.Hour), now.Add(24*time.Hour)
	store := postgres.NewPlanningStore(fixture.pool)
	snapshot, err := store.LoadSnapshot(fixture.ctx, jobID, from, to)
	if err != nil {
		t.Fatal(err)
	}
	location, err := time.LoadLocation("Europe/Vienna")
	if err != nil {
		t.Fatal(err)
	}
	cfg := planning.DefaultConfig(location)
	run, err := store.SaveRun(fixture.ctx, fixture.admin, snapshot, from, to, nil, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if run.ID == "" || run.JobID != jobID || run.Suggestions == nil || len(run.Suggestions) != 0 || run.HorizonDays != cfg.HorizonDays {
		t.Fatalf("saved empty run = %#v", run)
	}
	loaded, err := store.ListRun(fixture.ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.ID != run.ID || loaded.JobID != jobID || loaded.Suggestions == nil || len(loaded.Suggestions) != 0 || loaded.HorizonDays != cfg.HorizonDays {
		t.Fatalf("loaded empty run = %#v", loaded)
	}
	if _, err := store.ListRun(fixture.ctx, "00000000-0000-0000-0000-000000000001"); !errors.Is(err, planning.ErrNotFound) {
		t.Fatalf("missing run error = %v, want ErrNotFound", err)
	}
}

func TestPlanningSuggestionAdoptionCreatesProposalWithoutOutbox(t *testing.T) {
	fixture := newCalendarFixture(t)
	now := time.Date(2026, 8, 31, 4, 0, 0, 0, time.UTC)
	jobID := planningJob(t, fixture, "HW-PLAN-1")
	service := planningFixtureService(t, fixture, now)
	run, err := service.Suggest(fixture.ctx, fixture.admin, jobID)
	if err != nil {
		t.Fatal(err)
	}
	if len(run.Suggestions) == 0 || len(run.Suggestions) > 3 {
		t.Fatalf("suggestions=%d", len(run.Suggestions))
	}
	appointmentID, err := service.Adopt(fixture.ctx, fixture.admin, run.Suggestions[0].ID, "adopt")
	if err != nil {
		t.Fatal(err)
	}
	var lifecycle, workflow string
	var outbox int
	if err := fixture.pool.QueryRow(fixture.ctx, "SELECT a.lifecycle_status,j.workflow_status FROM appointments a JOIN jobs j ON j.id=a.job_id WHERE a.id=$1", appointmentID).Scan(&lifecycle, &workflow); err != nil {
		t.Fatal(err)
	}
	if err := fixture.pool.QueryRow(fixture.ctx, "SELECT count(*) FROM outbox_events WHERE aggregate_id=$1", appointmentID).Scan(&outbox); err != nil {
		t.Fatal(err)
	}
	if lifecycle != "proposal" || workflow != "planning" || outbox != 0 {
		t.Fatalf("state=%s/%s outbox=%d", lifecycle, workflow, outbox)
	}
	var activeWaitlist int
	if err := fixture.pool.QueryRow(fixture.ctx, "SELECT count(*) FROM waitlist_entries WHERE job_id=$1 AND removed_at IS NULL", jobID).Scan(&activeWaitlist); err != nil {
		t.Fatal(err)
	}
	if activeWaitlist != 1 {
		t.Fatalf("proposal removed waitlist: %d", activeWaitlist)
	}
}

func TestPlanningAdoptionRevalidatesConcurrentReservationAtomically(t *testing.T) {
	fixture := newCalendarFixture(t)
	now := time.Date(2026, 8, 31, 4, 0, 0, 0, time.UTC)
	jobID := planningJob(t, fixture, "HW-PLAN-RACE")
	service := planningFixtureService(t, fixture, now)
	run, err := service.Suggest(fixture.ctx, fixture.admin, jobID)
	if err != nil {
		t.Fatal(err)
	}
	candidate := run.Suggestions[0]
	_ = fixture.proposal(t, fixture.job(t, "HW-PLAN-BLOCK"), candidate.DriverID, candidate.ResourceIDs[0], candidate.StartsAt, candidate.EndsAt.Sub(candidate.StartsAt))
	if _, err := service.Adopt(fixture.ctx, fixture.admin, candidate.ID, "race"); !errors.Is(err, planning.ErrConflict) {
		t.Fatalf("adopt error=%v", err)
	}
	var appointments, outbox int
	if err := fixture.pool.QueryRow(fixture.ctx, "SELECT count(*) FROM appointments WHERE job_id=$1", jobID).Scan(&appointments); err != nil {
		t.Fatal(err)
	}
	if err := fixture.pool.QueryRow(fixture.ctx, "SELECT count(*) FROM outbox_events WHERE aggregate_id IN (SELECT id FROM appointments WHERE job_id=$1)", jobID).Scan(&outbox); err != nil {
		t.Fatal(err)
	}
	if appointments != 0 || outbox != 0 {
		t.Fatalf("rollback appointments/outbox=%d/%d", appointments, outbox)
	}
}

func TestPlanningAdoptionRejectsChangedRoutingInputFingerprint(t *testing.T) {
	fixture := newCalendarFixture(t)
	now := time.Date(2026, 8, 31, 4, 0, 0, 0, time.UTC)
	jobID := planningJob(t, fixture, "HW-PLAN-STALE-LOCATION")
	service := planningFixtureService(t, fixture, now)
	run, err := service.Suggest(fixture.ctx, fixture.admin, jobID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.pool.Exec(fixture.ctx, `UPDATE jobs
		SET pile_latitude=48.450000, pile_longitude=14.450000, pile_location_source='coordinates', pile_location_updated_at=now()
		WHERE id=$1`, jobID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Adopt(fixture.ctx, fixture.admin, run.Suggestions[0].ID, "stale-location"); !errors.Is(err, planning.ErrConflict) {
		t.Fatalf("adopt after routing input change error=%v want conflict", err)
	}
	var appointments int
	if err := fixture.pool.QueryRow(fixture.ctx, "SELECT count(*) FROM appointments WHERE job_id=$1", jobID).Scan(&appointments); err != nil {
		t.Fatal(err)
	}
	if appointments != 0 {
		t.Fatalf("stale planning input created %d appointments", appointments)
	}
}

func TestPlanningAdoptionSerializesFingerprintRecheckWithSchedulingWrites(t *testing.T) {
	fixture := newCalendarFixture(t)
	now := time.Date(2026, 8, 31, 4, 0, 0, 0, time.UTC)
	jobID := planningJob(t, fixture, "HW-PLAN-SERIALIZED")
	service := planningFixtureService(t, fixture, now)
	run, err := service.Suggest(fixture.ctx, fixture.admin, jobID)
	if err != nil {
		t.Fatal(err)
	}

	tx, err := fixture.pool.Begin(fixture.ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback(context.Background()) })
	if _, err := tx.Exec(fixture.ctx, "SELECT pg_advisory_xact_lock(1214342235, 1396919884)"); err != nil {
		t.Fatal(err)
	}
	store := postgres.NewCustomerStore(fixture.pool)
	var customerID string
	if err := fixture.pool.QueryRow(fixture.ctx, "SELECT customer_id::text FROM jobs WHERE id=$1", jobID).Scan(&customerID); err != nil {
		t.Fatal(err)
	}
	detail, err := store.CustomerDetail(fixture.ctx, customerID)
	if err != nil {
		t.Fatal(err)
	}
	job := detail.Jobs[0]
	latitude, longitude := 48.45, 14.45
	updateResult := make(chan error, 1)
	go func() {
		updateResult <- store.UpdateJob(fixture.ctx, fixture.admin, customers.UpdateJobInput{
			ID: job.ID, ExpectedVersion: job.Version, RequestID: "routing-location-update",
			Job: customers.JobInput{
				JobType: job.JobType, VolumeM3: job.VolumeM3, EstimatedHackMinutes: int(job.EstimatedHackMinutes),
				EstimatedTransportMinutes: int(job.EstimatedTransportMinutes), TransportTripCount: int(job.TransportTripCount),
				TransportMode: job.TransportMode, ExternalTransportConfirmed: job.ExternalTransportConfirmed,
				PreferredStartDate: job.PreferredStartDate, PreferredEndDate: job.PreferredEndDate,
				PreferenceText: job.PreferenceText, Urgency: job.Urgency, Region: job.Region, Source: job.Source,
				PileLatitude: &latitude, PileLongitude: &longitude, PileLocationSource: customers.PileSourceCoordinates,
			},
		})
	}()
	select {
	case updateErr := <-updateResult:
		t.Fatalf("job update bypassed scheduling serialization: %v", updateErr)
	case <-time.After(150 * time.Millisecond):
	}
	if err := tx.Commit(fixture.ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case updateErr := <-updateResult:
		if updateErr != nil {
			t.Fatalf("serialized job update: %v", updateErr)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("job update remained blocked after scheduling transaction committed")
	}
	if _, err := service.Adopt(fixture.ctx, fixture.admin, run.Suggestions[0].ID, "serialized-adopt"); !errors.Is(err, planning.ErrConflict) {
		t.Fatalf("adopt after serialized input change error=%v want conflict", err)
	}
}

func TestPlanningPerformanceBudgetWithRealisticDataset(t *testing.T) {
	fixture := newCalendarFixture(t)
	now := time.Date(2026, 8, 31, 4, 0, 0, 0, time.UTC)
	targetJob := planningJob(t, fixture, "HW-PERF-000")
	for index := 1; index < 100; index++ {
		jobID := planningJob(t, fixture, fmt.Sprintf("HW-PERF-%03d", index))
		if _, err := fixture.pool.Exec(fixture.ctx, "UPDATE jobs SET pile_latitude=48.21+($2::numeric/10000),pile_longitude=14.21+($2::numeric/10000),pile_location_source='coordinates',pile_location_updated_at=now() WHERE id=$1", jobID, index); err != nil {
			t.Fatal(err)
		}
	}
	for index := 3; index <= 6; index++ {
		var driverID string
		if err := fixture.pool.QueryRow(fixture.ctx, "INSERT INTO drivers (display_name) VALUES ($1) RETURNING id::text", fmt.Sprintf("Fahrer %d", index)).Scan(&driverID); err != nil {
			t.Fatal(err)
		}
		for weekday := 1; weekday <= 5; weekday++ {
			if _, err := fixture.pool.Exec(fixture.ctx, "INSERT INTO availability_rules (driver_id,iso_weekday,local_start,local_end,valid_from,status) VALUES ($1,$2,'07:00','17:00','2026-01-01','available')", driverID, weekday); err != nil {
				t.Fatal(err)
			}
		}
	}
	for index := 3; index <= 5; index++ {
		if _, err := fixture.pool.Exec(fixture.ctx, "INSERT INTO resources (resource_type,name,exclusive) VALUES ('chipper',$1,true)", fmt.Sprintf("Hackmaschine %d", index)); err != nil {
			t.Fatal(err)
		}
	}
	service := planningFixtureService(t, fixture, now)
	started := time.Now()
	run, err := service.Suggest(fixture.ctx, fixture.admin, targetJob)
	elapsed := time.Since(started)
	if err != nil {
		t.Fatal(err)
	}
	if len(run.Suggestions) == 0 {
		t.Fatal("no performance suggestions")
	}
	t.Logf("planning run with 100 waitlist entries, 6 drivers, 5 resources, 8-week configured model: %s", elapsed)
	if elapsed > 2*time.Second {
		t.Fatalf("planning performance budget exceeded: %s", elapsed)
	}
}
