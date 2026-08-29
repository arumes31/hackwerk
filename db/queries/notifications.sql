-- name: NewConfirmationRequestID :one
SELECT gen_random_uuid()::text;

-- name: GetNotificationPlanningData :one
SELECT a.id::text AS appointment_id, a.starts_at, a.ends_at, a.version,
       j.job_type, j.volume_m3::text,
       concat_ws(' ', NULLIF(c.first_name, ''), NULLIF(c.last_name, ''), NULLIF(c.company_name, ''))::text AS customer_name,
       COALESCE(c.email::text, '')::text AS email,
       COALESCE(c.phone_normalized, c.phone_raw, '')::text AS phone,
       c.notification_preference
FROM appointments a
JOIN jobs j ON j.id = a.job_id
JOIN customers c ON c.id = j.customer_id
WHERE a.id = sqlc.arg(appointment_id)::uuid;

-- name: NextConfirmationTokenVersion :one
SELECT COALESCE(max(token_version), 0)::integer + 1
FROM confirmation_requests
WHERE appointment_id = sqlc.arg(appointment_id)::uuid;

-- name: RevokeActiveConfirmationRequests :exec
UPDATE confirmation_requests
SET status='revoked', revoked_at=now(), revoke_reason=sqlc.arg(reason), updated_at=now()
WHERE appointment_id=sqlc.arg(appointment_id)::uuid AND status='active';

-- name: SetAppointmentNotificationOverride :exec
UPDATE appointments
SET notification_override_reason=NULLIF(sqlc.arg(reason)::text, ''),
    confirmation_status=sqlc.arg(confirmation_status), updated_at=now()
WHERE id=sqlc.arg(appointment_id)::uuid;

-- name: InsertConfirmationRequest :exec
INSERT INTO confirmation_requests (
    id, appointment_id, token_hash, form_nonce_hash, token_key_id, token_version, expires_at
) VALUES (
    sqlc.arg(id)::uuid, sqlc.arg(appointment_id)::uuid, sqlc.arg(token_hash)::bytea,
    sqlc.arg(form_nonce_hash)::bytea, sqlc.arg(token_key_id), sqlc.arg(token_version), sqlc.arg(expires_at)::timestamptz
);

-- name: InsertNotification :one
INSERT INTO notifications (
    appointment_id, confirmation_request_id, channel, recipient_snapshot, template_version, parameters, max_attempts
) VALUES (
    sqlc.arg(appointment_id)::uuid, sqlc.arg(confirmation_request_id)::uuid, sqlc.arg(channel),
    sqlc.arg(recipient_snapshot), sqlc.arg(template_version), sqlc.arg(parameters)::jsonb, sqlc.arg(max_attempts)
)
RETURNING id::text;

-- name: InsertNotificationOutboxEvent :exec
INSERT INTO outbox_events (
    event_type, aggregate_type, aggregate_id, payload, payload_version, idempotency_key, max_attempts
) VALUES (
    'notification.requested', 'notification', sqlc.arg(notification_id)::uuid,
    jsonb_build_object('notification_id', sqlc.arg(notification_id)::text), 1,
    'notification.requested:' || sqlc.arg(notification_id)::text, sqlc.arg(max_attempts)
)
ON CONFLICT (idempotency_key) DO NOTHING;

-- name: GetConfirmationByTokenHash :one
SELECT cr.id::text, cr.appointment_id::text, cr.token_hash, cr.form_nonce_hash, cr.token_key_id,
       cr.token_version, cr.status, COALESCE(cr.response, '')::text AS response, cr.expires_at,
       a.lifecycle_status, a.confirmation_status, a.starts_at, a.ends_at, a.version,
       j.job_number, j.job_type, j.volume_m3::text,
       concat_ws(' ', NULLIF(c.first_name, ''), NULLIF(c.last_name, ''), NULLIF(c.company_name, ''))::text AS customer_name,
       c.locality
FROM confirmation_requests cr
JOIN appointments a ON a.id=cr.appointment_id
JOIN jobs j ON j.id=a.job_id
JOIN customers c ON c.id=j.customer_id
WHERE cr.token_hash=sqlc.arg(token_hash)::bytea;

-- name: GetConfirmationForUpdate :one
SELECT cr.id::text, cr.appointment_id::text, cr.token_hash, cr.form_nonce_hash,
       cr.status, COALESCE(cr.response, '')::text AS response, cr.expires_at,
       a.lifecycle_status, a.confirmation_status, a.version
FROM confirmation_requests cr
JOIN appointments a ON a.id=cr.appointment_id
WHERE cr.token_hash=sqlc.arg(token_hash)::bytea
FOR UPDATE OF cr;

-- name: GetConfirmationAppointmentID :one
SELECT appointment_id::text FROM confirmation_requests
WHERE token_hash=sqlc.arg(token_hash)::bytea;

-- name: LockAppointmentForConfirmation :one
SELECT id::text FROM appointments
WHERE id=sqlc.arg(id)::uuid
FOR UPDATE;

-- name: SetConfirmationResponse :execrows
UPDATE confirmation_requests
SET response=sqlc.arg(response), response_note=NULLIF(sqlc.arg(response_note)::text, ''), responded_at=now(), updated_at=now()
WHERE id=sqlc.arg(id)::uuid AND status='active'
  AND (response IS NULL OR (response='callback_requested' AND sqlc.arg(response)::text IN ('confirmed','declined')));

-- name: SetAppointmentConfirmation :exec
UPDATE appointments
SET confirmation_status=sqlc.arg(confirmation_status), version=version+1, updated_at=now()
WHERE id=sqlc.arg(appointment_id)::uuid AND lifecycle_status='fixed';

-- name: GetActiveConfirmationForUpdate :one
SELECT cr.id::text, COALESCE(cr.response, '')::text AS response, cr.expires_at,
       a.lifecycle_status, a.confirmation_status, a.version
FROM confirmation_requests cr
JOIN appointments a ON a.id=cr.appointment_id
WHERE cr.appointment_id=sqlc.arg(appointment_id)::uuid AND cr.status='active'
FOR UPDATE OF cr;

-- name: ResetConfirmationResponse :execrows
UPDATE confirmation_requests
SET response=NULL, responded_at=NULL, updated_at=now()
WHERE id=sqlc.arg(id)::uuid AND status='active' AND response IS NOT NULL;

-- name: InsertConfirmationRespondedEvent :exec
INSERT INTO outbox_events (event_type, aggregate_type, aggregate_id, payload, payload_version, idempotency_key, status, processed_at)
VALUES ('confirmation.responded', 'appointment', sqlc.arg(appointment_id)::uuid,
        jsonb_build_object('appointment_id', sqlc.arg(appointment_id)::text, 'response', sqlc.arg(response)::text), 1,
        'confirmation.responded:' || sqlc.arg(confirmation_request_id)::text || ':' || sqlc.arg(response)::text,
        'processed', now())
ON CONFLICT (idempotency_key) DO NOTHING;

-- name: ClaimNotificationOutbox :many
WITH candidates AS (
    SELECT id
    FROM outbox_events
    WHERE event_type='notification.requested'
      AND ((status IN ('queued','retry_wait') AND available_at <= sqlc.arg(now_utc)::timestamptz)
           OR (status='claimed' AND lease_until <= sqlc.arg(now_utc)::timestamptz))
    ORDER BY available_at, created_at, id
    FOR UPDATE SKIP LOCKED
    LIMIT sqlc.arg(batch_size)
)
UPDATE outbox_events o
SET status='claimed', claimed_by=sqlc.arg(worker_id), lease_until=sqlc.arg(lease_until)::timestamptz,
    locked_at=sqlc.arg(now_utc)::timestamptz, attempt_count=o.attempt_count+1, updated_at=sqlc.arg(now_utc)::timestamptz
FROM candidates c
WHERE o.id=c.id
RETURNING o.id::text, o.aggregate_id::text AS notification_id, o.idempotency_key, o.attempt_count, o.max_attempts;

-- name: GetNotificationDelivery :one
SELECT n.id::text, n.appointment_id::text, n.confirmation_request_id::text,
       n.channel, n.recipient_snapshot, n.template_version, n.status,
       cr.token_key_id, cr.token_version, cr.status AS confirmation_request_status, cr.expires_at,
       a.lifecycle_status,
       COALESCE(NULLIF(n.parameters->>'starts_at', '')::timestamptz, a.starts_at) AS starts_at,
       COALESCE(NULLIF(n.parameters->>'ends_at', '')::timestamptz, a.ends_at) AS ends_at,
       COALESCE(NULLIF(n.parameters->>'job_type', ''), j.job_type)::text AS job_type,
       COALESCE(NULLIF(n.parameters->>'volume_m3', ''), j.volume_m3::text)::text AS volume_m3,
       COALESCE(NULLIF(n.parameters->>'customer_name', ''), concat_ws(' ', NULLIF(c.first_name, ''), NULLIF(c.last_name, ''), NULLIF(c.company_name, '')))::text AS customer_name
FROM notifications n
JOIN confirmation_requests cr ON cr.id=n.confirmation_request_id
JOIN appointments a ON a.id=n.appointment_id
JOIN jobs j ON j.id=a.job_id
JOIN customers c ON c.id=j.customer_id
WHERE n.id=sqlc.arg(id)::uuid;

-- name: RenewNotificationLease :execrows
UPDATE outbox_events
SET lease_until=sqlc.arg(lease_until)::timestamptz, updated_at=sqlc.arg(now_utc)::timestamptz
WHERE id=sqlc.arg(id)::uuid AND status='claimed' AND claimed_by=sqlc.arg(worker_id)
  AND lease_until > sqlc.arg(now_utc)::timestamptz;

-- name: MarkNotificationSending :execrows
UPDATE notifications SET status='sending', attempt_count=attempt_count+1, updated_at=now()
WHERE id=sqlc.arg(id)::uuid AND status IN ('queued','retry_wait');

-- name: MarkNotificationSent :exec
UPDATE notifications SET status='sent', provider_id=NULLIF(sqlc.arg(provider_id)::text,''), sent_at=now(), last_error_code=NULL, updated_at=now()
WHERE id=sqlc.arg(id)::uuid;

-- name: MarkOutboxProcessed :execrows
UPDATE outbox_events SET status='processed', processed_at=now(), claimed_by=NULL, lease_until=NULL, last_error_code=NULL, updated_at=now()
WHERE id=sqlc.arg(id)::uuid AND status='claimed' AND claimed_by=sqlc.arg(worker_id);

-- name: MarkNotificationRetry :exec
UPDATE notifications SET status='retry_wait', available_at=sqlc.arg(available_at)::timestamptz, last_error_code=sqlc.arg(error_code), updated_at=now()
WHERE id=sqlc.arg(id)::uuid;

-- name: MarkOutboxRetry :execrows
UPDATE outbox_events SET status='retry_wait', available_at=sqlc.arg(available_at)::timestamptz,
    claimed_by=NULL, lease_until=NULL, last_error_code=sqlc.arg(error_code), updated_at=now()
WHERE id=sqlc.arg(id)::uuid AND status='claimed' AND claimed_by=sqlc.arg(worker_id);

-- name: MarkNotificationFailed :exec
UPDATE notifications SET status='failed', last_error_code=sqlc.arg(error_code), updated_at=now()
WHERE id=sqlc.arg(id)::uuid;

-- name: MarkOutboxDead :execrows
UPDATE outbox_events SET status='dead', claimed_by=NULL, lease_until=NULL, last_error_code=sqlc.arg(error_code), updated_at=now()
WHERE id=sqlc.arg(id)::uuid AND status='claimed' AND claimed_by=sqlc.arg(worker_id);

-- name: ListAppointmentNotifications :many
SELECT n.id::text, n.channel, n.status, n.recipient_snapshot, n.attempt_count, n.max_attempts,
       COALESCE(n.last_error_code, '')::text AS last_error_code,
       COALESCE(n.provider_id, '')::text AS provider_id,
       n.available_at, n.sent_at, n.created_at, n.updated_at,
       cr.status AS confirmation_request_status, COALESCE(cr.response, '')::text AS response, COALESCE(cr.response_note, '')::text AS response_note,
       cr.responded_at, cr.expires_at, n.reviewed_at
FROM notifications n
JOIN confirmation_requests cr ON cr.id=n.confirmation_request_id
WHERE n.appointment_id=sqlc.arg(appointment_id)::uuid
ORDER BY n.created_at DESC, n.channel;

-- name: ListFailedNotifications :many
SELECT n.id::text, n.appointment_id::text, n.channel, n.status, n.recipient_snapshot,
       n.attempt_count, n.max_attempts, COALESCE(n.last_error_code, '')::text AS last_error_code,
       COALESCE(n.provider_id, '')::text AS provider_id,
       n.available_at, n.sent_at, n.created_at, n.updated_at,
       cr.status AS confirmation_request_status, COALESCE(cr.response, '')::text AS response,
       cr.responded_at, cr.expires_at, n.reviewed_at
FROM notifications n
JOIN confirmation_requests cr ON cr.id=n.confirmation_request_id AND cr.status='active'
JOIN appointments a ON a.id=n.appointment_id AND a.lifecycle_status='fixed'
WHERE n.status IN ('retry_wait','failed')
  AND (sqlc.arg(status_filter)::text = 'all' OR n.status=sqlc.arg(status_filter)::text)
ORDER BY n.updated_at DESC, n.id
LIMIT sqlc.arg(result_limit);

-- name: ListCallbackRequests :many
SELECT a.id::text AS appointment_id, j.job_number,
       concat_ws(' ', NULLIF(c.first_name, ''), NULLIF(c.last_name, ''), NULLIF(c.company_name, ''))::text AS customer_name,
       c.locality, COALESCE(c.phone_normalized, c.phone_raw, '')::text AS phone,
       cr.responded_at, cr.expires_at
FROM confirmation_requests cr
JOIN appointments a ON a.id=cr.appointment_id AND a.lifecycle_status='fixed'
JOIN jobs j ON j.id=a.job_id
JOIN customers c ON c.id=j.customer_id
WHERE cr.status='active' AND cr.response='callback_requested'
ORDER BY cr.responded_at DESC, cr.id
LIMIT sqlc.arg(result_limit);

-- name: MarkNotificationReviewed :one
UPDATE notifications
SET reviewed_at=sqlc.arg(reviewed_at)::timestamptz,
    reviewed_by_user_id=sqlc.arg(reviewed_by_user_id)::uuid,
    updated_at=now()
WHERE id=sqlc.arg(id)::uuid AND status IN ('retry_wait','failed')
RETURNING appointment_id::text;

-- name: RequeueNotification :execrows
UPDATE notifications SET status='queued', available_at=now(), last_error_code=NULL,
    reviewed_at=NULL, reviewed_by_user_id=NULL, updated_at=now()
WHERE id=sqlc.arg(id)::uuid AND status IN ('retry_wait','failed');

-- name: RequeueNotificationOutbox :execrows
UPDATE outbox_events SET status='queued', available_at=now(), attempt_count=0, last_error_code=NULL,
    claimed_by=NULL, lease_until=NULL, processed_at=NULL, updated_at=now()
WHERE aggregate_type='notification' AND aggregate_id=sqlc.arg(notification_id)::uuid AND status IN ('retry_wait','dead');
