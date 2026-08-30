//go:build integration

package integration_test

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"example.invalid/hackplan/internal/adapters/postgres"
	"example.invalid/hackplan/internal/appointment"
	"example.invalid/hackplan/internal/notification"
)

func TestFixCreatesHashOnlyConfirmationAndIdempotentResponses(t *testing.T) {
	responses := []notification.Response{notification.ResponseConfirmed, notification.ResponseDeclined, notification.ResponseCallback}
	for _, wantedResponse := range responses {
		t.Run(string(wantedResponse), func(t *testing.T) {
			fixture := newCalendarFixture(t)
			start := time.Date(2026, 9, 1, 6, 0, 0, 0, time.UTC)
			proposed := fixture.proposal(t, fixture.job(t, "HW-2026-NOTIFY-"+strings.ToUpper(string(wantedResponse[:3]))), fixture.driver1, fixture.chipper1, start, 3*time.Hour)
			fixed, err := fixture.service.FixAppointment(fixture.ctx, fixture.admin, appointment.FixInput{MutateInput: appointment.MutateInput{
				ID: proposed.ID, ExpectedVersion: proposed.Version, RequestID: "fix-notify",
			}})
			if err != nil {
				t.Fatal(err)
			}
			if fixed.Lifecycle != appointment.LifecycleFixed || fixed.Confirmation != appointment.ConfirmationPending {
				t.Fatalf("fixed = %s/%s", fixed.Lifecycle, fixed.Confirmation)
			}
			var requestID, appointmentID, keyID string
			var version int32
			var tokenHash []byte
			if err := fixture.pool.QueryRow(fixture.ctx, `SELECT id::text, appointment_id::text, token_key_id, token_version, token_hash FROM confirmation_requests WHERE appointment_id=$1 AND status='active'`, fixed.ID).Scan(&requestID, &appointmentID, &keyID, &version, &tokenHash); err != nil {
				t.Fatal(err)
			}
			material, err := notification.DevelopmentKeyRing().Reconstruct(keyID, requestID, appointmentID, version)
			if err != nil || !notification.ConstantTimeEqual(material.Hash, tokenHash) {
				t.Fatalf("stored hash does not match derived material: %v", err)
			}
			var notificationCount, eventCount int
			var databaseText string
			if err := fixture.pool.QueryRow(fixture.ctx, "SELECT count(*) FROM notifications WHERE confirmation_request_id=$1", requestID).Scan(&notificationCount); err != nil {
				t.Fatal(err)
			}
			if err := fixture.pool.QueryRow(fixture.ctx, "SELECT count(*) FROM outbox_events WHERE event_type='notification.requested' AND aggregate_id IN (SELECT id FROM notifications WHERE confirmation_request_id=$1)", requestID).Scan(&eventCount); err != nil {
				t.Fatal(err)
			}
			if err := fixture.pool.QueryRow(fixture.ctx, "SELECT coalesce(string_agg(payload::text, ' '), '') || ' ' || coalesce((SELECT string_agg(metadata::text, ' ') FROM audit_events), '') FROM outbox_events").Scan(&databaseText); err != nil {
				t.Fatal(err)
			}
			if notificationCount != 1 || eventCount != 1 || strings.Contains(databaseText, material.Raw) {
				t.Fatalf("notification/event/raw-token = %d/%d/%t", notificationCount, eventCount, strings.Contains(databaseText, material.Raw))
			}

			confirmationService, err := notification.NewConfirmationService(postgres.NewNotificationStore(fixture.pool), notification.DevelopmentKeyRing(), time.Now)
			if err != nil {
				t.Fatal(err)
			}
			view, err := confirmationService.View(fixture.ctx, material.Raw)
			if err != nil {
				t.Fatal(err)
			}
			first, err := confirmationService.Respond(fixture.ctx, material.Raw, view.FormNonce, wantedResponse, "", "public-first")
			if err != nil {
				t.Fatal(err)
			}
			second, err := confirmationService.Respond(fixture.ctx, material.Raw, view.FormNonce, wantedResponse, "", "public-repeat")
			if err != nil || first.Response != wantedResponse || second.Response != wantedResponse {
				t.Fatalf("idempotent response first=%+v second=%+v err=%v", first, second, err)
			}
			var lifecycle, confirmation string
			if err := fixture.pool.QueryRow(fixture.ctx, "SELECT lifecycle_status, confirmation_status FROM appointments WHERE id=$1", fixed.ID).Scan(&lifecycle, &confirmation); err != nil {
				t.Fatal(err)
			}
			if lifecycle != "fixed" || confirmation != string(wantedResponse) {
				t.Fatalf("customer response changed reservation: %s/%s", lifecycle, confirmation)
			}
			if err := fixture.pool.QueryRow(fixture.ctx, "SELECT count(*) FROM outbox_events WHERE event_type='confirmation.responded' AND aggregate_id=$1", fixed.ID).Scan(&eventCount); err != nil {
				t.Fatal(err)
			}
			if eventCount != 1 {
				t.Fatalf("response side effects = %d", eventCount)
			}
			adminService, _ := notification.NewAdminService(postgres.NewNotificationStore(fixture.pool), time.Now)
			timeline, err := adminService.AppointmentStatuses(fixture.ctx, fixture.admin, fixed.ID)
			if err != nil || len(timeline) != 1 || timeline[0].Response != string(wantedResponse) || timeline[0].CreatedAt.IsZero() || timeline[0].RespondedAt.IsZero() || timeline[0].ExpiresAt.IsZero() || timeline[0].ProviderReference != "" {
				t.Fatalf("notification timeline = %+v, err=%v", timeline, err)
			}
			if wantedResponse == notification.ResponseCallback {
				callbacks, err := adminService.Callbacks(fixture.ctx, fixture.admin, 20)
				if err != nil || len(callbacks) != 1 || callbacks[0].AppointmentID != fixed.ID || callbacks[0].Phone == "" || strings.Contains(callbacks[0].Phone, "+43") {
					t.Fatalf("callback list = %+v, err=%v", callbacks, err)
				}
			}
			var strandedGenericEvents int
			if err := fixture.pool.QueryRow(fixture.ctx, `SELECT count(*) FROM outbox_events
				WHERE event_type <> 'notification.requested' AND status IN ('queued','claimed','retry_wait')`).Scan(&strandedGenericEvents); err != nil {
				t.Fatal(err)
			}
			if strandedGenericEvents != 0 {
				t.Fatalf("generic outbox events left pending = %d", strandedGenericEvents)
			}
		})
	}
}

func TestQueuedNotificationUsesFixTimeJobSnapshot(t *testing.T) {
	fixture := newCalendarFixture(t)
	start := time.Date(2026, 9, 1, 6, 0, 0, 0, time.UTC)
	jobID := fixture.job(t, "HW-2026-NOTIFY-SNAPSHOT")
	proposed := fixture.proposal(t, jobID, fixture.driver1, fixture.chipper1, start, 3*time.Hour)
	fixed, err := fixture.service.FixAppointment(fixture.ctx, fixture.admin, appointment.FixInput{MutateInput: appointment.MutateInput{
		ID: proposed.ID, ExpectedVersion: proposed.Version, RequestID: "fix-snapshot",
	}})
	if err != nil {
		t.Fatal(err)
	}
	var notificationID string
	if err := fixture.pool.QueryRow(fixture.ctx, "SELECT id::text FROM notifications WHERE appointment_id=$1", fixed.ID).Scan(&notificationID); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.pool.Exec(fixture.ctx, "UPDATE jobs SET job_type='chipping_with_transport',volume_m3=99.00 WHERE id=$1", jobID); err != nil {
		t.Fatal(err)
	}
	delivery, err := postgres.NewNotificationWorkerStore(fixture.pool).LoadDelivery(fixture.ctx, notificationID)
	if err != nil {
		t.Fatal(err)
	}
	if delivery.JobType != "chipping_only" || delivery.VolumeM3 != "30.00" || !delivery.StartsAt.Equal(start) || !delivery.EndsAt.Equal(start.Add(3*time.Hour)) {
		t.Fatalf("notification snapshot drifted after job edit: %#v", delivery)
	}
}

func TestMoveRevokesOldTokenAndPlansNewVersion(t *testing.T) {
	fixture := newCalendarFixture(t)
	start := time.Date(2026, 9, 1, 6, 0, 0, 0, time.UTC)
	proposed := fixture.proposal(t, fixture.job(t, "HW-2026-NOTIFY-MOVE"), fixture.driver1, fixture.chipper1, start, 3*time.Hour)
	fixed, err := fixture.service.FixAppointment(fixture.ctx, fixture.admin, appointment.FixInput{MutateInput: appointment.MutateInput{ID: proposed.ID, ExpectedVersion: proposed.Version, RequestID: "fix"}})
	if err != nil {
		t.Fatal(err)
	}
	var oldRequest, keyID string
	var oldVersion int32
	if err := fixture.pool.QueryRow(fixture.ctx, "SELECT id::text, token_key_id, token_version FROM confirmation_requests WHERE appointment_id=$1 AND status='active'", fixed.ID).Scan(&oldRequest, &keyID, &oldVersion); err != nil {
		t.Fatal(err)
	}
	oldMaterial, _ := notification.DevelopmentKeyRing().Reconstruct(keyID, oldRequest, fixed.ID, oldVersion)
	confirmationService, _ := notification.NewConfirmationService(postgres.NewNotificationStore(fixture.pool), notification.DevelopmentKeyRing(), time.Now)
	view, err := confirmationService.View(fixture.ctx, oldMaterial.Raw)
	if err != nil {
		t.Fatal(err)
	}
	confirmed, err := confirmationService.Respond(fixture.ctx, oldMaterial.Raw, view.FormNonce, notification.ResponseDeclined, "Bitte vormittags zurückrufen", "confirm")
	if err != nil {
		t.Fatal(err)
	}
	moved, err := fixture.service.MoveAppointment(fixture.ctx, fixture.admin, appointment.MoveInput{
		MutateInput: appointment.MutateInput{ID: fixed.ID, ExpectedVersion: confirmed.AppointmentVersion, RequestID: "move-fixed"},
		StartsAt:    start.Add(24 * time.Hour), EndsAt: start.Add(27 * time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	if moved.Confirmation != appointment.ConfirmationPending {
		t.Fatalf("moved confirmation = %s", moved.Confirmation)
	}
	if _, err := confirmationService.View(fixture.ctx, oldMaterial.Raw); !errors.Is(err, notification.ErrConfirmationRevoked) {
		t.Fatalf("old token remains usable: %v", err)
	}
	var activeVersion int32
	var revokedCount int
	if err := fixture.pool.QueryRow(fixture.ctx, "SELECT token_version FROM confirmation_requests WHERE appointment_id=$1 AND status='active'", fixed.ID).Scan(&activeVersion); err != nil {
		t.Fatal(err)
	}
	if err := fixture.pool.QueryRow(fixture.ctx, "SELECT count(*) FROM confirmation_requests WHERE appointment_id=$1 AND status='revoked'", fixed.ID).Scan(&revokedCount); err != nil {
		t.Fatal(err)
	}
	if activeVersion != oldVersion+1 || revokedCount != 1 {
		t.Fatalf("token versions active/revoked = %d/%d", activeVersion, revokedCount)
	}
}

func TestFixWithoutReachableChannelRequiresAuditedOverride(t *testing.T) {
	fixture := newCalendarFixture(t)
	jobID := fixture.job(t, "HW-2026-NOTIFY-NONE")
	if _, err := fixture.pool.Exec(fixture.ctx, "UPDATE customers SET email=NULL, notification_preference='email' WHERE id=(SELECT customer_id FROM jobs WHERE id=$1)", jobID); err != nil {
		t.Fatal(err)
	}
	proposed := fixture.proposal(t, jobID, fixture.driver1, fixture.chipper1, time.Date(2026, 9, 1, 6, 0, 0, 0, time.UTC), 3*time.Hour)
	if _, err := fixture.service.FixAppointment(fixture.ctx, fixture.admin, appointment.FixInput{MutateInput: appointment.MutateInput{ID: proposed.ID, ExpectedVersion: proposed.Version, RequestID: "missing"}}); !errors.Is(err, appointment.ErrNotification) {
		t.Fatalf("missing-channel error = %v", err)
	}
	fixed, err := fixture.service.FixAppointment(fixture.ctx, fixture.admin, appointment.FixInput{
		MutateInput:               appointment.MutateInput{ID: proposed.ID, ExpectedVersion: proposed.Version, RequestID: "override"},
		WithoutNotificationReason: "Kunde wünscht ausschließlich telefonische Abstimmung",
	})
	if err != nil {
		t.Fatal(err)
	}
	var requestCount int
	var overrideReason string
	if err := fixture.pool.QueryRow(fixture.ctx, "SELECT notification_override_reason FROM appointments WHERE id=$1", fixed.ID).Scan(&overrideReason); err != nil {
		t.Fatal(err)
	}
	if err := fixture.pool.QueryRow(fixture.ctx, "SELECT count(*) FROM confirmation_requests WHERE appointment_id=$1", fixed.ID).Scan(&requestCount); err != nil {
		t.Fatal(err)
	}
	if fixed.Confirmation != appointment.ConfirmationNotRequested || requestCount != 0 || overrideReason == "" {
		t.Fatalf("override result confirmation/requests/reason = %s/%d/%q", fixed.Confirmation, requestCount, overrideReason)
	}
}

func TestAdminCanResetResponseAndReissueTokenWithReason(t *testing.T) {
	fixture := newCalendarFixture(t)
	start := time.Date(2026, 9, 1, 6, 0, 0, 0, time.UTC)
	proposed := fixture.proposal(t, fixture.job(t, "HW-2026-NOTIFY-ADMIN"), fixture.driver1, fixture.chipper1, start, 3*time.Hour)
	fixed, err := fixture.service.FixAppointment(fixture.ctx, fixture.admin, appointment.FixInput{MutateInput: appointment.MutateInput{ID: proposed.ID, ExpectedVersion: proposed.Version, RequestID: "fix"}})
	if err != nil {
		t.Fatal(err)
	}
	var requestID, keyID string
	var tokenVersion int32
	if err := fixture.pool.QueryRow(fixture.ctx, "SELECT id::text, token_key_id, token_version FROM confirmation_requests WHERE appointment_id=$1 AND status='active'", fixed.ID).Scan(&requestID, &keyID, &tokenVersion); err != nil {
		t.Fatal(err)
	}
	oldMaterial, _ := notification.DevelopmentKeyRing().Reconstruct(keyID, requestID, fixed.ID, tokenVersion)
	store := postgres.NewNotificationStore(fixture.pool)
	confirmationService, _ := notification.NewConfirmationService(store, notification.DevelopmentKeyRing(), time.Now)
	view, err := confirmationService.View(fixture.ctx, oldMaterial.Raw)
	if err != nil {
		t.Fatal(err)
	}
	confirmed, err := confirmationService.Respond(fixture.ctx, oldMaterial.Raw, view.FormNonce, notification.ResponseConfirmed, "", "confirm")
	if err != nil {
		t.Fatal(err)
	}
	adminService, _ := notification.NewAdminService(store, time.Now)
	resetReason := "Rückfrage an audit-canary@example.test"
	if err := adminService.ResetResponse(fixture.ctx, fixture.admin, fixed.ID, confirmed.AppointmentVersion, resetReason, "reset"); err != nil {
		t.Fatal(err)
	}
	var confirmationStatus string
	var version int32
	var responseValue *string
	var responseNote *string
	if err := fixture.pool.QueryRow(fixture.ctx, "SELECT a.confirmation_status, a.version, cr.response, cr.response_note FROM appointments a JOIN confirmation_requests cr ON cr.appointment_id=a.id AND cr.status='active' WHERE a.id=$1", fixed.ID).Scan(&confirmationStatus, &version, &responseValue, &responseNote); err != nil {
		t.Fatal(err)
	}
	if confirmationStatus != "pending" || responseValue != nil || responseNote != nil {
		t.Fatalf("reset status/response/note = %s/%v/%v", confirmationStatus, responseValue, responseNote)
	}
	reissueReason := "Neuer Link für second-canary@example.test"
	var requestsBefore, notificationsBefore, outboxBefore int
	if err := fixture.pool.QueryRow(fixture.ctx, "SELECT count(*) FROM confirmation_requests WHERE appointment_id=$1", fixed.ID).Scan(&requestsBefore); err != nil {
		t.Fatal(err)
	}
	if err := fixture.pool.QueryRow(fixture.ctx, "SELECT count(*) FROM notifications WHERE appointment_id=$1", fixed.ID).Scan(&notificationsBefore); err != nil {
		t.Fatal(err)
	}
	if err := fixture.pool.QueryRow(fixture.ctx, "SELECT count(*) FROM outbox_events WHERE event_type='notification.requested' AND aggregate_id IN (SELECT id FROM notifications WHERE appointment_id=$1)", fixed.ID).Scan(&outboxBefore); err != nil {
		t.Fatal(err)
	}
	if err := adminService.Reissue(fixture.ctx, fixture.admin, fixed.ID, version, reissueReason, "reissue"); err != nil {
		t.Fatal(err)
	}
	if err := adminService.Reissue(fixture.ctx, fixture.admin, fixed.ID, version, reissueReason, "reissue-replay"); !errors.Is(err, notification.ErrAdminActionUnavailable) {
		t.Fatalf("same-version reissue replay error = %v", err)
	}
	if _, err := confirmationService.View(fixture.ctx, oldMaterial.Raw); !errors.Is(err, notification.ErrConfirmationRevoked) {
		t.Fatalf("reissued old token remains valid: %v", err)
	}
	var activeCount, revokedCount, auditCount, currentVersion, requestCount, notificationCount, outboxCount int
	if err := fixture.pool.QueryRow(fixture.ctx, "SELECT count(*) FILTER (WHERE status='active'), count(*) FILTER (WHERE status='revoked') FROM confirmation_requests WHERE appointment_id=$1", fixed.ID).Scan(&activeCount, &revokedCount); err != nil {
		t.Fatal(err)
	}
	if err := fixture.pool.QueryRow(fixture.ctx, "SELECT count(*) FROM audit_events WHERE object_id=$1 AND action IN ('confirmation.response_reset','confirmation.reissued')", fixed.ID).Scan(&auditCount); err != nil {
		t.Fatal(err)
	}
	if err := fixture.pool.QueryRow(fixture.ctx, "SELECT version FROM appointments WHERE id=$1", fixed.ID).Scan(&currentVersion); err != nil {
		t.Fatal(err)
	}
	if err := fixture.pool.QueryRow(fixture.ctx, "SELECT count(*) FROM confirmation_requests WHERE appointment_id=$1", fixed.ID).Scan(&requestCount); err != nil {
		t.Fatal(err)
	}
	if err := fixture.pool.QueryRow(fixture.ctx, "SELECT count(*) FROM notifications WHERE appointment_id=$1", fixed.ID).Scan(&notificationCount); err != nil {
		t.Fatal(err)
	}
	if err := fixture.pool.QueryRow(fixture.ctx, "SELECT count(*) FROM outbox_events WHERE event_type='notification.requested' AND aggregate_id IN (SELECT id FROM notifications WHERE appointment_id=$1)", fixed.ID).Scan(&outboxCount); err != nil {
		t.Fatal(err)
	}
	if activeCount != 1 || revokedCount != 1 || auditCount != 2 || currentVersion != int(version)+1 ||
		requestCount != requestsBefore+1 || notificationCount != notificationsBefore+1 || outboxCount != outboxBefore+1 {
		t.Fatalf("admin lifecycle active/revoked/audit/version/requests/notifications/outbox = %d/%d/%d/%d/%d/%d/%d", activeCount, revokedCount, auditCount, currentVersion, requestCount, notificationCount, outboxCount)
	}
	var leakedReasons int
	if err := fixture.pool.QueryRow(fixture.ctx, `SELECT count(*) FROM audit_events
		WHERE object_id=$1 AND metadata::text LIKE '%canary@example.test%'`, fixed.ID).Scan(&leakedReasons); err != nil {
		t.Fatal(err)
	}
	if leakedReasons != 0 {
		t.Fatalf("admin free-text reason leaked into %d audit records", leakedReasons)
	}
}

func TestParallelWorkerClaimsAndAdminRetry(t *testing.T) {
	fixture := newCalendarFixture(t)
	start := time.Date(2026, 9, 1, 6, 0, 0, 0, time.UTC)
	for index, number := range []string{"HW-2026-CLAIM-1", "HW-2026-CLAIM-2"} {
		proposed := fixture.proposal(t, fixture.job(t, number), []string{fixture.driver1, fixture.driver2}[index], []string{fixture.chipper1, fixture.chipper2}[index], start.Add(time.Duration(index)*time.Hour), 3*time.Hour)
		if _, err := fixture.service.FixAppointment(fixture.ctx, fixture.admin, appointment.FixInput{MutateInput: appointment.MutateInput{ID: proposed.ID, ExpectedVersion: proposed.Version, RequestID: "fix-" + number}}); err != nil {
			t.Fatal(err)
		}
	}
	stores := []*postgres.NotificationWorkerStore{postgres.NewNotificationWorkerStore(fixture.pool), postgres.NewNotificationWorkerStore(fixture.pool)}
	claimed := make(chan []notification.ClaimedEvent, 2)
	var wait sync.WaitGroup
	for index, store := range stores {
		wait.Add(1)
		go func(index int, store *postgres.NotificationWorkerStore) {
			defer wait.Done()
			values, err := store.Claim(fixture.ctx, "worker-"+string(rune('a'+index)), time.Now().UTC(), time.Now().UTC().Add(time.Minute), 10)
			if err != nil {
				t.Errorf("claim: %v", err)
			}
			claimed <- values
		}(index, store)
	}
	wait.Wait()
	close(claimed)
	unique := map[string]notification.ClaimedEvent{}
	for values := range claimed {
		for _, value := range values {
			if _, duplicate := unique[value.OutboxID]; duplicate {
				t.Fatalf("outbox claimed twice: %s", value.OutboxID)
			}
			unique[value.OutboxID] = value
		}
	}
	if len(unique) != 2 {
		t.Fatalf("claimed events = %d, want 2", len(unique))
	}
	for _, value := range unique {
		if err := stores[0].Dead(fixture.ctx, value, claimedWorker(t, fixture, value.OutboxID), "simulated_failure"); err != nil {
			t.Fatal(err)
		}
	}
	adminService, _ := notification.NewAdminService(postgres.NewNotificationStore(fixture.pool), time.Now)
	failed, err := adminService.Failed(fixture.ctx, fixture.admin, notification.FailureAll, 100)
	if err != nil || len(failed) != 2 || strings.Contains(failed[0].Recipient, "@") && !strings.Contains(failed[0].Recipient, "***@") {
		t.Fatalf("failed list = %+v, err=%v", failed, err)
	}
	if err := adminService.Review(fixture.ctx, fixture.admin, failed[0].ID, "admin-review"); err != nil {
		t.Fatal(err)
	}
	var reviewed bool
	var reviewAudit int
	if err := fixture.pool.QueryRow(fixture.ctx, "SELECT reviewed_at IS NOT NULL FROM notifications WHERE id=$1", failed[0].ID).Scan(&reviewed); err != nil {
		t.Fatal(err)
	}
	if err := fixture.pool.QueryRow(fixture.ctx, "SELECT count(*) FROM audit_events WHERE object_id=$1 AND action='notification.reviewed'", failed[0].AppointmentID).Scan(&reviewAudit); err != nil {
		t.Fatal(err)
	}
	if !reviewed || reviewAudit != 1 {
		t.Fatalf("reviewed/audit = %t/%d", reviewed, reviewAudit)
	}
	if err := adminService.Retry(fixture.ctx, fixture.admin, failed[0].ID, "admin-retry"); err != nil {
		t.Fatal(err)
	}
	var state string
	if err := fixture.pool.QueryRow(fixture.ctx, "SELECT status, reviewed_at IS NOT NULL FROM notifications WHERE id=$1", failed[0].ID).Scan(&state, &reviewed); err != nil {
		t.Fatal(err)
	}
	if state != "queued" || reviewed {
		t.Fatalf("retried state/reviewed = %s/%t", state, reviewed)
	}
}

func TestExpiredSendingClaimRequiresReconciliationInsteadOfResend(t *testing.T) {
	fixture := newCalendarFixture(t)
	start := time.Date(2026, 9, 1, 6, 0, 0, 0, time.UTC)
	proposed := fixture.proposal(t, fixture.job(t, "HW-2026-UNCERTAIN"), fixture.driver1, fixture.chipper1, start, 3*time.Hour)
	if _, err := fixture.service.FixAppointment(fixture.ctx, fixture.admin, appointment.FixInput{MutateInput: appointment.MutateInput{
		ID: proposed.ID, ExpectedVersion: proposed.Version, RequestID: "uncertain-fix",
	}}); err != nil {
		t.Fatal(err)
	}
	store := postgres.NewNotificationWorkerStore(fixture.pool)
	now := time.Now().UTC()
	first, err := store.Claim(fixture.ctx, "worker-first", now, now.Add(time.Minute), 1)
	if err != nil || len(first) != 1 {
		t.Fatalf("first claim=%+v err=%v", first, err)
	}
	if err := store.MarkSending(fixture.ctx, first[0], "worker-first", now, now.Add(time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	second, err := store.Claim(fixture.ctx, "worker-second", now.Add(time.Second), now.Add(time.Minute), 1)
	if err != nil || len(second) != 1 {
		t.Fatalf("reclaim=%+v err=%v", second, err)
	}
	if err := store.MarkSending(fixture.ctx, second[0], "worker-second", now.Add(time.Second), now.Add(time.Minute)); !errors.Is(err, notification.ErrDeliveryUncertain) {
		t.Fatalf("reclaimed sending notification error=%v want uncertain", err)
	}
}

func claimedWorker(t *testing.T, fixture calendarFixture, outboxID string) string {
	t.Helper()
	var worker string
	if err := fixture.pool.QueryRow(fixture.ctx, "SELECT claimed_by FROM outbox_events WHERE id=$1", outboxID).Scan(&worker); err != nil {
		t.Fatal(err)
	}
	return worker
}
