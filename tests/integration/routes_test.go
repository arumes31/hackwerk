//go:build integration

package integration_test

import (
	"errors"
	"testing"
	"time"

	"example.invalid/hackplan/internal/adapters/postgres"
	"example.invalid/hackplan/internal/appointment"
	"example.invalid/hackplan/internal/auth"
	"example.invalid/hackplan/internal/planning"
)

func TestRouteStoreAssignsEveryStopAsProposalWithoutOutbox(t *testing.T) {
	fixture := newCalendarFixture(t)
	store := postgres.NewRouteStore(fixture.pool)
	jobID := routeJob(t, fixture, "HW-ROUTE-001", 48.21, 14.21)
	candidates, err := store.LoadRouteCandidates(fixture.ctx, []string{jobID})
	if err != nil || len(candidates) != 1 {
		t.Fatalf("LoadRouteCandidates() = %#v, %v", candidates, err)
	}

	departure := time.Date(2026, 9, 1, 6, 0, 0, 0, time.UTC)
	draft, err := store.SaveRouteDraft(fixture.ctx, fixture.admin, planning.SaveRouteDraftInput{
		Route: func() planning.RouteDraft {
			route := routeDraftForCandidates(fixture, departure, candidates, false)
			route.ChipperResourceID = ""
			return route
		}(), RequestID: "route-create",
	})
	if err != nil {
		t.Fatalf("SaveRouteDraft() error = %v", err)
	}
	if draft.Status != planning.RouteStatusDraft || draft.Version != 1 || draft.StartLabel != "Betriebshof" || draft.EndLabel != "Betriebshof" || len(draft.Stops) != 1 || draft.Stops[0].ID == "" {
		t.Fatalf("saved route = %#v", draft)
	}

	assigned, err := store.AssignRoute(fixture.ctx, fixture.admin, planning.AssignRouteInput{
		ID: draft.ID, ExpectedVersion: draft.Version, RequestID: "route-assign",
	})
	if err != nil {
		t.Fatalf("AssignRoute() error = %v", err)
	}
	if assigned.Status != planning.RouteStatusAssigned || assigned.Version != 2 || assigned.Stops[0].AppointmentID == "" {
		t.Fatalf("assigned route = %#v", assigned)
	}
	var lifecycle, workflow string
	var bufferBefore, bufferAfter int
	var outbox int
	if err := fixture.pool.QueryRow(fixture.ctx, `SELECT a.lifecycle_status, j.workflow_status, a.buffer_before_minutes, a.buffer_after_minutes
		FROM appointments a JOIN jobs j ON j.id=a.job_id WHERE a.id=$1`, assigned.Stops[0].AppointmentID).Scan(&lifecycle, &workflow, &bufferBefore, &bufferAfter); err != nil {
		t.Fatal(err)
	}
	if err := fixture.pool.QueryRow(fixture.ctx, "SELECT count(*) FROM outbox_events WHERE aggregate_id=$1", assigned.Stops[0].AppointmentID).Scan(&outbox); err != nil {
		t.Fatal(err)
	}
	if lifecycle != "proposal" || workflow != "planning" || bufferBefore != 15 || bufferAfter != 15 || outbox != 0 {
		t.Fatalf("proposal state = %s/%s, buffers=%d/%d outbox=%d", lifecycle, workflow, bufferBefore, bufferAfter, outbox)
	}
	var chipperAssignments int
	if err := fixture.pool.QueryRow(fixture.ctx, `SELECT count(*) FROM appointment_resources WHERE appointment_id=$1 AND purpose='chipping'`, assigned.Stops[0].AppointmentID).Scan(&chipperAssignments); err != nil {
		t.Fatal(err)
	}
	if chipperAssignments != 0 || assigned.ChipperResourceID != "" || assigned.ChipperName != "" {
		t.Fatalf("optional chipper assignment count/route = %d/%#v", chipperAssignments, assigned)
	}
	overview, err := store.LoadRouteCandidates(fixture.ctx, nil)
	if err != nil || len(overview) != 1 || overview[0].JobID != jobID || overview[0].UnavailableReason != "Bereits eingeplant" {
		t.Fatalf("map overview after assignment = %#v, %v", overview, err)
	}
	selectable, err := store.LoadRouteCandidates(fixture.ctx, []string{jobID})
	if err != nil || len(selectable) != 0 {
		t.Fatalf("selectable candidates after assignment = %#v, %v", selectable, err)
	}
	var appointmentVersion int32
	if err := fixture.pool.QueryRow(fixture.ctx, "SELECT version FROM appointments WHERE id=$1", assigned.Stops[0].AppointmentID).Scan(&appointmentVersion); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.FixAppointment(fixture.ctx, fixture.admin, appointment.FixInput{
		MutateInput:          appointment.MutateInput{ID: assigned.Stops[0].AppointmentID, ExpectedVersion: appointmentVersion, RequestID: "route-without-chipper-unconfirmed"},
		MissingChipperReason: "Maschine wird vor Ort organisiert",
	}); !errors.Is(err, appointment.ErrValidation) {
		t.Fatalf("FixAppointment() error = %v, want validation without explicit confirmation", err)
	}
	var notesBeforeFix int
	if err := fixture.pool.QueryRow(fixture.ctx, "SELECT count(*) FROM job_notes WHERE job_id=$1", jobID).Scan(&notesBeforeFix); err != nil {
		t.Fatal(err)
	}
	if notesBeforeFix != 0 {
		t.Fatalf("notes before confirmed fix = %d", notesBeforeFix)
	}
	fixed, err := fixture.service.FixAppointment(fixture.ctx, fixture.admin, appointment.FixInput{
		MutateInput:           appointment.MutateInput{ID: assigned.Stops[0].AppointmentID, ExpectedVersion: appointmentVersion, RequestID: "route-without-chipper-confirmed"},
		ConfirmWithoutChipper: true,
		MissingChipperReason:  "  Maschine wird vor Ort organisiert  ",
	})
	if err != nil {
		t.Fatalf("FixAppointment() confirmed error = %v", err)
	}
	if fixed.Lifecycle != appointment.LifecycleFixed || len(fixed.Resources) != 0 {
		t.Fatalf("fixed appointment = %#v", fixed)
	}
	var noteBody, noteAuthor string
	if err := fixture.pool.QueryRow(fixture.ctx, `SELECT n.body, n.author_user_id::text FROM job_notes n WHERE n.job_id=$1 ORDER BY n.created_at DESC, n.id DESC LIMIT 1`, jobID).Scan(&noteBody, &noteAuthor); err != nil {
		t.Fatal(err)
	}
	if noteBody != "Termin ohne Hackmaschine fixiert: Maschine wird vor Ort organisiert" || noteAuthor != fixture.admin.UserID {
		t.Fatalf("missing chipper note/author = %q/%q", noteBody, noteAuthor)
	}
	var fixedAudit, noteAudit, fixedOutbox int
	if err := fixture.pool.QueryRow(fixture.ctx, "SELECT count(*) FROM audit_events WHERE action='appointment.fixed' AND object_id=$1", fixed.ID).Scan(&fixedAudit); err != nil {
		t.Fatal(err)
	}
	if err := fixture.pool.QueryRow(fixture.ctx, "SELECT count(*) FROM audit_events WHERE action='job.note_added' AND object_id=$1", jobID).Scan(&noteAudit); err != nil {
		t.Fatal(err)
	}
	if err := fixture.pool.QueryRow(fixture.ctx, "SELECT count(*) FROM outbox_events WHERE aggregate_id=$1", fixed.ID).Scan(&fixedOutbox); err != nil {
		t.Fatal(err)
	}
	if fixedAudit != 1 || noteAudit != 1 || fixedOutbox == 0 {
		t.Fatalf("fixed audit/note audit/outbox = %d/%d/%d", fixedAudit, noteAudit, fixedOutbox)
	}
	overview, err = store.LoadRouteCandidates(fixture.ctx, nil)
	if err != nil || len(overview) != 0 {
		t.Fatalf("map overview after fixing = %#v, %v", overview, err)
	}
	latest, err := store.LatestAssignedRouteForDriver(fixture.ctx, fixture.driver1, "2026-09-01")
	if err != nil || latest.ID != assigned.ID {
		t.Fatalf("LatestAssignedRouteForDriver() = %q, %v", latest.ID, err)
	}
	available, err := store.AssignedRouteExistsForDriver(fixture.ctx, fixture.driver1, "2026-09-01")
	if err != nil || !available {
		t.Fatalf("AssignedRouteExistsForDriver() = %v, %v", available, err)
	}
	available, err = store.AssignedRouteExistsForDriver(fixture.ctx, fixture.driver1, "2026-09-02")
	if err != nil || available {
		t.Fatalf("AssignedRouteExistsForDriver() for empty day = %v, %v", available, err)
	}
}

func TestRouteStoreAssignmentRollsBackEveryProposalOnConflict(t *testing.T) {
	fixture := newCalendarFixture(t)
	store := postgres.NewRouteStore(fixture.pool)
	jobIDs := []string{
		routeJob(t, fixture, "HW-ROUTE-RACE-1", 48.21, 14.21),
		routeJob(t, fixture, "HW-ROUTE-RACE-2", 48.22, 14.22),
	}
	candidates, err := store.LoadRouteCandidates(fixture.ctx, jobIDs)
	if err != nil || len(candidates) != 2 {
		t.Fatalf("LoadRouteCandidates() = %#v, %v", candidates, err)
	}
	departure := time.Date(2026, 9, 1, 6, 0, 0, 0, time.UTC)
	route := routeDraftForCandidates(fixture, departure, candidates, true)
	draft, err := store.SaveRouteDraft(fixture.ctx, fixture.admin, planning.SaveRouteDraftInput{
		Route: route, RequestID: "route-conflict-create",
	})
	if err != nil {
		t.Fatalf("SaveRouteDraft() error = %v", err)
	}
	_, err = store.AssignRoute(fixture.ctx, fixture.admin, planning.AssignRouteInput{
		ID: draft.ID, ExpectedVersion: draft.Version, RequestID: "route-conflict-assign",
	})
	if !errors.Is(err, planning.ErrConflict) {
		t.Fatalf("AssignRoute() error = %v, want conflict", err)
	}
	var appointments, linked int
	var status string
	if err := fixture.pool.QueryRow(fixture.ctx, "SELECT count(*) FROM appointments WHERE job_id=ANY($1::uuid[])", jobIDs).Scan(&appointments); err != nil {
		t.Fatal(err)
	}
	if err := fixture.pool.QueryRow(fixture.ctx, "SELECT count(*) FROM route_stops WHERE route_draft_id=$1 AND appointment_id IS NOT NULL", draft.ID).Scan(&linked); err != nil {
		t.Fatal(err)
	}
	if err := fixture.pool.QueryRow(fixture.ctx, "SELECT status FROM route_drafts WHERE id=$1", draft.ID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if appointments != 0 || linked != 0 || status != "draft" {
		t.Fatalf("rollback appointments=%d linked=%d status=%s", appointments, linked, status)
	}
}

func TestRouteStoreAssignmentRejectsConflictDuringTravel(t *testing.T) {
	fixture := newCalendarFixture(t)
	store := postgres.NewRouteStore(fixture.pool)
	departure := time.Date(2026, 9, 1, 6, 0, 0, 0, time.UTC)
	blockingJobID := fixture.job(t, "HW-ROUTE-TRAVEL-BLOCK")
	var blockingAppointmentID string
	if err := fixture.pool.QueryRow(fixture.ctx, `INSERT INTO appointments (job_id,lifecycle_status,starts_at,ends_at)
		VALUES ($1,'proposal',$2,$3) RETURNING id::text`, blockingJobID, departure, departure.Add(10*time.Minute)).Scan(&blockingAppointmentID); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.pool.Exec(fixture.ctx, `INSERT INTO appointment_drivers
		(appointment_id,driver_id,is_primary,active,reserved_starts_at,reserved_ends_at)
		VALUES ($1,$2,true,true,$3,$4)`, blockingAppointmentID, fixture.driver1, departure, departure.Add(10*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.pool.Exec(fixture.ctx, `INSERT INTO appointment_resources
		(appointment_id,resource_id,purpose,exclusive,active,reserved_starts_at,reserved_ends_at)
		SELECT $1,id,'chipping',exclusive,true,$3,$4 FROM resources WHERE id=$2`, blockingAppointmentID, fixture.chipper1, departure, departure.Add(10*time.Minute)); err != nil {
		t.Fatal(err)
	}

	jobID := routeJob(t, fixture, "HW-ROUTE-TRAVEL", 48.21, 14.21)
	candidates, err := store.LoadRouteCandidates(fixture.ctx, []string{jobID})
	if err != nil || len(candidates) != 1 {
		t.Fatalf("LoadRouteCandidates() = %#v, %v", candidates, err)
	}
	draft, err := store.SaveRouteDraft(fixture.ctx, fixture.admin, planning.SaveRouteDraftInput{
		Route: routeDraftForCandidates(fixture, departure, candidates, false), RequestID: "route-travel-create",
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = store.AssignRoute(fixture.ctx, fixture.admin, planning.AssignRouteInput{
		ID: draft.ID, ExpectedVersion: draft.Version, RequestID: "route-travel-assign",
	})
	if !errors.Is(err, planning.ErrConflict) {
		t.Fatalf("AssignRoute() error = %v, want travel conflict", err)
	}
	var appointments, outbox int
	var status string
	if err := fixture.pool.QueryRow(fixture.ctx, "SELECT count(*) FROM appointments WHERE job_id=$1", jobID).Scan(&appointments); err != nil {
		t.Fatal(err)
	}
	if err := fixture.pool.QueryRow(fixture.ctx, "SELECT count(*) FROM outbox_events WHERE aggregate_id IN (SELECT id FROM appointments WHERE job_id=$1)", jobID).Scan(&outbox); err != nil {
		t.Fatal(err)
	}
	if err := fixture.pool.QueryRow(fixture.ctx, "SELECT status FROM route_drafts WHERE id=$1", draft.ID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if appointments != 0 || outbox != 0 || status != "draft" {
		t.Fatalf("travel conflict rollback appointments=%d outbox=%d status=%s", appointments, outbox, status)
	}
}

func TestRouteStoreDriverReorderPreservesProposalTimes(t *testing.T) {
	fixture := newCalendarFixture(t)
	store := postgres.NewRouteStore(fixture.pool)
	jobIDs := []string{
		routeJob(t, fixture, "HW-ROUTE-ORDER-1", 48.21, 14.21),
		routeJob(t, fixture, "HW-ROUTE-ORDER-2", 48.22, 14.22),
	}
	candidates, err := store.LoadRouteCandidates(fixture.ctx, jobIDs)
	if err != nil {
		t.Fatal(err)
	}
	draft, err := store.SaveRouteDraft(fixture.ctx, fixture.admin, planning.SaveRouteDraftInput{
		Route: routeDraftForCandidates(fixture, time.Date(2026, 9, 1, 6, 0, 0, 0, time.UTC), candidates, false), RequestID: "route-order-create",
	})
	if err != nil {
		t.Fatal(err)
	}
	assigned, err := store.AssignRoute(fixture.ctx, fixture.admin, planning.AssignRouteInput{ID: draft.ID, ExpectedVersion: draft.Version, RequestID: "route-order-assign"})
	if err != nil {
		t.Fatal(err)
	}

	originalTimes := make(map[string][2]time.Time, len(assigned.Stops))
	for _, stop := range assigned.Stops {
		originalTimes[stop.AppointmentID] = [2]time.Time{stop.StartsAt, stop.EndsAt}
	}
	assigned.Stops[0], assigned.Stops[1] = assigned.Stops[1], assigned.Stops[0]
	order := []string{assigned.Stops[0].ID, assigned.Stops[1].ID}
	driverActor := auth.Actor{UserID: fixture.admin.UserID, Role: auth.RoleDriver, DriverID: fixture.driver1}
	reordered, err := store.SaveRouteOrder(fixture.ctx, driverActor, planning.SaveRouteOrderInput{
		Route: assigned, ExpectedVersion: assigned.Version, StopIDs: order, RequestID: "route-order-driver",
	})
	if err != nil {
		t.Fatal(err)
	}
	if reordered.Version != assigned.Version+1 || reordered.Stops[0].ID != order[0] {
		t.Fatalf("reordered route=%#v", reordered)
	}
	for appointmentID, expected := range originalTimes {
		var startsAt, endsAt time.Time
		if err := fixture.pool.QueryRow(fixture.ctx, "SELECT starts_at, ends_at FROM appointments WHERE id=$1", appointmentID).Scan(&startsAt, &endsAt); err != nil {
			t.Fatal(err)
		}
		if !startsAt.Equal(expected[0]) || !endsAt.Equal(expected[1]) {
			t.Fatalf("appointment %s times changed to %s-%s", appointmentID, startsAt, endsAt)
		}
	}
}

func routeJob(t *testing.T, fixture calendarFixture, number string, latitude, longitude float64) string {
	t.Helper()
	jobID := fixture.job(t, number)
	if _, err := fixture.pool.Exec(fixture.ctx, `UPDATE jobs
		SET pile_latitude=$2, pile_longitude=$3, pile_location_source='map_pin', pile_location_updated_at=now()
		WHERE id=$1`, jobID, latitude, longitude); err != nil {
		t.Fatal(err)
	}
	return jobID
}

func routeDraftForCandidates(fixture calendarFixture, departure time.Time, candidates []planning.RouteCandidate, overlap bool) planning.RouteDraft {
	start := planning.Point{Latitude: 48.2, Longitude: 14.2}
	stops := make([]planning.RouteStop, 0, len(candidates))
	geometry := []planning.Point{start}
	legs := make([]planning.RouteLeg, 0, len(candidates)+1)
	cursor := departure
	for index, candidate := range candidates {
		leg := planning.RouteLeg{DistanceMeters: 1000, Duration: 15 * time.Minute}
		if !overlap || index == 0 {
			cursor = cursor.Add(leg.Duration)
		}
		startsAt := cursor
		stops = append(stops, planning.RouteStop{
			JobID: candidate.JobID, Position: index + 1, Location: candidate.Location,
			WorkDuration: candidate.WorkDuration, LegDuration: leg.Duration,
			LegDistanceMeters: leg.DistanceMeters, EstimatedArrivalAt: startsAt,
			StartsAt: startsAt, EndsAt: startsAt.Add(candidate.WorkDuration),
			JobVersion: candidate.JobVersion, WaitlistVersion: candidate.WaitlistVersion,
		})
		geometry = append(geometry, candidate.Location)
		legs = append(legs, leg)
		if !overlap {
			cursor = startsAt.Add(candidate.WorkDuration)
		}
	}
	returnLeg := planning.RouteLeg{DistanceMeters: 1000, Duration: 15 * time.Minute}
	geometry = append(geometry, start)
	legs = append(legs, returnLeg)
	return planning.RouteDraft{
		DriverID: fixture.driver1, ChipperResourceID: fixture.chipper1,
		Status: planning.RouteStatusDraft, Departure: departure, StartLabel: "Betriebshof", EndLabel: "Betriebshof", Start: start, End: start, Stops: stops,
		Directions: planning.RouteDirections{
			Geometry: geometry, Legs: legs, DistanceMeters: 1000 * len(legs),
			Duration: 15 * time.Minute * time.Duration(len(legs)), Source: "haversine", Estimated: true,
		},
	}
}
