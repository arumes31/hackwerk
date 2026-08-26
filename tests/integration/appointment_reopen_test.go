//go:build integration

package integration_test

import (
	"errors"
	"testing"
	"time"

	"example.invalid/hackplan/internal/appointment"
)

func TestReopenCancelledAppointmentIsAtomicAndHasNoDeliverySideEffects(t *testing.T) {
	fixture := newCalendarFixture(t)
	start := time.Date(2026, 9, 1, 6, 0, 0, 0, time.UTC)
	proposed := fixture.proposal(
		t,
		fixture.job(t, "HW-2026-REOPEN-01"),
		fixture.driver1,
		fixture.chipper1,
		start,
		3*time.Hour,
	)
	fixed, err := fixture.service.FixAppointment(fixture.ctx, fixture.admin, appointment.FixInput{
		MutateInput: appointment.MutateInput{
			ID: proposed.ID, ExpectedVersion: proposed.Version, RequestID: "fix-before-reopen",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	cancelled, err := fixture.service.CancelAppointment(fixture.ctx, fixture.admin, appointment.CancelInput{
		MutateInput: appointment.MutateInput{
			ID: fixed.ID, ExpectedVersion: fixed.Version, RequestID: "cancel-before-reopen",
		},
		Reason: "Kunde verschiebt den Auftrag",
	})
	if err != nil {
		t.Fatal(err)
	}
	var notificationsBefore, outboxBefore int
	if err := fixture.pool.QueryRow(fixture.ctx, "SELECT count(*) FROM notifications WHERE appointment_id=$1", cancelled.ID).Scan(&notificationsBefore); err != nil {
		t.Fatal(err)
	}
	if err := fixture.pool.QueryRow(fixture.ctx, "SELECT count(*) FROM outbox_events WHERE aggregate_id=$1", cancelled.ID).Scan(&outboxBefore); err != nil {
		t.Fatal(err)
	}

	reopened, err := fixture.service.ReopenAppointment(fixture.ctx, fixture.admin, appointment.ReopenInput{
		MutateInput: appointment.MutateInput{
			ID: cancelled.ID, ExpectedVersion: cancelled.Version, RequestID: "reopen-cancelled",
		},
		Reason: "Kunde hat einen neuen Termin angefragt",
	})
	if err != nil {
		t.Fatal(err)
	}
	if reopened.Lifecycle != appointment.LifecycleProposal || reopened.Confirmation != appointment.ConfirmationNotRequested {
		t.Fatalf("reopened state = %s/%s, want proposal/not_requested", reopened.Lifecycle, reopened.Confirmation)
	}

	var workflow string
	var activeDrivers, activeResources, activeConfirmations, revokedConfirmations, notifications, outbox, audits int
	var reopenReason, previousCancellationReason string
	if err := fixture.pool.QueryRow(fixture.ctx, "SELECT workflow_status FROM jobs WHERE id=$1", reopened.JobID).Scan(&workflow); err != nil {
		t.Fatal(err)
	}
	if err := fixture.pool.QueryRow(fixture.ctx, "SELECT count(*) FROM appointment_drivers WHERE appointment_id=$1 AND active", reopened.ID).Scan(&activeDrivers); err != nil {
		t.Fatal(err)
	}
	if err := fixture.pool.QueryRow(fixture.ctx, "SELECT count(*) FROM appointment_resources WHERE appointment_id=$1 AND active", reopened.ID).Scan(&activeResources); err != nil {
		t.Fatal(err)
	}
	if err := fixture.pool.QueryRow(fixture.ctx, "SELECT count(*) FROM confirmation_requests WHERE appointment_id=$1 AND status='active'", reopened.ID).Scan(&activeConfirmations); err != nil {
		t.Fatal(err)
	}
	if err := fixture.pool.QueryRow(fixture.ctx, "SELECT count(*) FROM confirmation_requests WHERE appointment_id=$1 AND status='revoked'", reopened.ID).Scan(&revokedConfirmations); err != nil {
		t.Fatal(err)
	}
	if err := fixture.pool.QueryRow(fixture.ctx, "SELECT count(*) FROM notifications WHERE appointment_id=$1", reopened.ID).Scan(&notifications); err != nil {
		t.Fatal(err)
	}
	if err := fixture.pool.QueryRow(fixture.ctx, "SELECT count(*) FROM outbox_events WHERE aggregate_id=$1", reopened.ID).Scan(&outbox); err != nil {
		t.Fatal(err)
	}
	if err := fixture.pool.QueryRow(fixture.ctx, "SELECT count(*) FROM audit_events WHERE object_id=$1 AND action='appointment.reopened'", reopened.ID).Scan(&audits); err != nil {
		t.Fatal(err)
	}
	if err := fixture.pool.QueryRow(fixture.ctx, "SELECT metadata->>'reason' FROM audit_events WHERE object_id=$1 AND action='appointment.reopened'", reopened.ID).Scan(&reopenReason); err != nil {
		t.Fatal(err)
	}
	if err := fixture.pool.QueryRow(fixture.ctx, "SELECT metadata->>'previous_cancellation_reason' FROM audit_events WHERE object_id=$1 AND action='appointment.reopened'", reopened.ID).Scan(&previousCancellationReason); err != nil {
		t.Fatal(err)
	}
	if workflow != "planning" || activeDrivers != 1 || activeResources != 1 || activeConfirmations != 0 || revokedConfirmations != 1 || notifications != notificationsBefore || outbox != outboxBefore || audits != 1 || reopenReason != "Kunde hat einen neuen Termin angefragt" || previousCancellationReason != "Kunde verschiebt den Auftrag" {
		t.Fatalf(
			"reopen workflow/drivers/resources/active-confirmations/revoked-confirmations/notifications/outbox/audit/reason/previous-reason = %s/%d/%d/%d/%d/%d/%d/%d/%q/%q",
			workflow,
			activeDrivers,
			activeResources,
			activeConfirmations,
			revokedConfirmations,
			notifications,
			outbox,
			audits,
			reopenReason,
			previousCancellationReason,
		)
	}
}

func TestReopenCancelledAppointmentRollsBackOnReservationConflict(t *testing.T) {
	fixture := newCalendarFixture(t)
	start := time.Date(2026, 9, 1, 6, 0, 0, 0, time.UTC)
	first := fixture.proposal(
		t,
		fixture.job(t, "HW-2026-REOPEN-02"),
		fixture.driver1,
		fixture.chipper1,
		start,
		3*time.Hour,
	)
	cancelled, err := fixture.service.CancelAppointment(fixture.ctx, fixture.admin, appointment.CancelInput{
		MutateInput: appointment.MutateInput{ID: first.ID, ExpectedVersion: first.Version, RequestID: "cancel-conflicting"},
		Reason:      "Kunde verschiebt den Auftrag",
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = fixture.proposal(
		t,
		fixture.job(t, "HW-2026-REOPEN-03"),
		fixture.driver1,
		fixture.chipper1,
		start,
		3*time.Hour,
	)

	_, err = fixture.service.ReopenAppointment(fixture.ctx, fixture.admin, appointment.ReopenInput{
		MutateInput: appointment.MutateInput{ID: cancelled.ID, ExpectedVersion: cancelled.Version, RequestID: "reopen-conflicting"},
		Reason:      "Kunde hat einen neuen Termin angefragt",
	})
	if !errors.Is(err, appointment.ErrConflict) {
		t.Fatalf("ReopenAppointment() conflict error = %v, want %v", err, appointment.ErrConflict)
	}

	var lifecycle, workflow string
	var activeReservations, audits int
	if err := fixture.pool.QueryRow(fixture.ctx, "SELECT a.lifecycle_status, j.workflow_status FROM appointments a JOIN jobs j ON j.id=a.job_id WHERE a.id=$1", cancelled.ID).Scan(&lifecycle, &workflow); err != nil {
		t.Fatal(err)
	}
	if err := fixture.pool.QueryRow(fixture.ctx, "SELECT count(*) FROM appointment_resources WHERE appointment_id=$1 AND active", cancelled.ID).Scan(&activeReservations); err != nil {
		t.Fatal(err)
	}
	if err := fixture.pool.QueryRow(fixture.ctx, "SELECT count(*) FROM audit_events WHERE object_id=$1 AND action='appointment.reopened'", cancelled.ID).Scan(&audits); err != nil {
		t.Fatal(err)
	}
	if lifecycle != "cancelled" || workflow != "waitlist" || activeReservations != 0 || audits != 0 {
		t.Fatalf("failed reopen lifecycle/workflow/reservations/audit = %s/%s/%d/%d", lifecycle, workflow, activeReservations, audits)
	}
}
