-- name: GetAppointmentForUpdate :one
SELECT a.id::text, a.job_id::text, a.lifecycle_status, a.confirmation_status,
       a.starts_at, a.ends_at, a.buffer_before_minutes, a.buffer_after_minutes,
       COALESCE(a.availability_override_reason, '')::text AS availability_override_reason,
       a.version, j.workflow_status, j.job_type, j.transport_mode,
       j.external_transport_confirmed, j.estimated_hack_minutes
FROM appointments a
JOIN jobs j ON j.id = a.job_id
WHERE a.id = sqlc.arg(id)::uuid
FOR UPDATE OF a, j;

-- name: GetAppointment :one
SELECT a.id::text, a.job_id::text, j.job_number, a.lifecycle_status, a.confirmation_status,
       a.starts_at, a.ends_at, a.buffer_before_minutes, a.buffer_after_minutes,
       COALESCE(a.availability_override_reason, '')::text AS availability_override_reason,
       a.version, j.workflow_status, j.job_type, j.transport_mode,
       j.external_transport_confirmed, j.estimated_hack_minutes
FROM appointments a
JOIN jobs j ON j.id = a.job_id
WHERE a.id = sqlc.arg(id)::uuid;

-- name: GetPlanningJob :one
SELECT j.id::text, j.job_number, j.workflow_status, j.job_type, j.transport_mode,
       j.external_transport_confirmed, j.estimated_hack_minutes, j.archived_at,
       COALESCE(w.id::text, '')::text AS waitlist_id, COALESCE(w.version, 0)::integer AS waitlist_version
FROM jobs j
LEFT JOIN waitlist_entries w ON w.job_id = j.id AND w.removed_at IS NULL
WHERE j.id = sqlc.arg(id)::uuid
FOR UPDATE OF j;

-- name: InsertAppointmentDraft :one
INSERT INTO appointments (job_id, starts_at, ends_at, buffer_before_minutes, buffer_after_minutes)
VALUES (sqlc.arg(job_id)::uuid, sqlc.arg(starts_at)::timestamptz, sqlc.arg(ends_at)::timestamptz,
        sqlc.arg(buffer_before_minutes), sqlc.arg(buffer_after_minutes))
RETURNING id::text, version;

-- name: UpdateAppointmentTime :execrows
UPDATE appointments SET starts_at = sqlc.arg(starts_at)::timestamptz,
    ends_at = sqlc.arg(ends_at)::timestamptz,
    confirmation_status = CASE WHEN lifecycle_status = 'fixed' THEN 'pending' ELSE confirmation_status END,
    version = version + 1, updated_at = now()
WHERE id = sqlc.arg(id)::uuid AND version = sqlc.arg(expected_version)
  AND lifecycle_status IN ('draft', 'proposal', 'fixed');

-- name: BumpAppointmentVersion :execrows
UPDATE appointments SET version = version + 1, updated_at = now()
WHERE id = sqlc.arg(id)::uuid AND version = sqlc.arg(expected_version)
  AND lifecycle_status IN ('draft', 'proposal', 'fixed');

-- name: SetAppointmentProposal :execrows
UPDATE appointments SET lifecycle_status = 'proposal', version = version + 1, updated_at = now()
WHERE id = sqlc.arg(id)::uuid AND version = sqlc.arg(expected_version) AND lifecycle_status = 'draft';

-- name: SetAppointmentFixed :execrows
UPDATE appointments SET lifecycle_status = 'fixed', confirmation_status = 'pending',
    fixed_by_user_id = sqlc.arg(actor_user_id)::uuid, fixed_at = now(),
    version = version + 1, updated_at = now()
WHERE id = sqlc.arg(id)::uuid AND version = sqlc.arg(expected_version) AND lifecycle_status = 'proposal';

-- name: SetAppointmentCancelled :execrows
UPDATE appointments SET lifecycle_status = 'cancelled',
    cancelled_by_user_id = sqlc.arg(actor_user_id)::uuid, cancelled_at = now(),
    cancellation_reason = NULLIF(sqlc.arg(reason)::text, ''),
    version = version + 1, updated_at = now()
WHERE id = sqlc.arg(id)::uuid AND version = sqlc.arg(expected_version)
  AND lifecycle_status IN ('draft', 'proposal', 'fixed');

-- name: SetAppointmentCompleted :execrows
UPDATE appointments SET lifecycle_status = 'completed',
    completed_by_user_id = sqlc.arg(actor_user_id)::uuid, completed_at = now(),
    completion_override_reason = NULLIF(sqlc.arg(override_reason)::text, ''),
    version = version + 1, updated_at = now()
WHERE id = sqlc.arg(id)::uuid AND version = sqlc.arg(expected_version) AND lifecycle_status = 'fixed';

-- name: SetAppointmentOverrideReason :exec
UPDATE appointments SET availability_override_reason = NULLIF(sqlc.arg(reason)::text, '')
WHERE id = sqlc.arg(id)::uuid;

-- name: DeleteAppointmentAssignments :exec
DELETE FROM appointment_drivers WHERE appointment_id = sqlc.arg(appointment_id)::uuid;

-- name: DeleteAppointmentResourceAssignments :exec
DELETE FROM appointment_resources WHERE appointment_id = sqlc.arg(appointment_id)::uuid;

-- name: InsertAppointmentDriver :execrows
INSERT INTO appointment_drivers (
    appointment_id, driver_id, is_primary, active, reserved_starts_at, reserved_ends_at
)
SELECT a.id, sqlc.arg(driver_id)::uuid, sqlc.arg(is_primary),
       a.lifecycle_status IN ('proposal', 'fixed'),
       a.starts_at - make_interval(mins => a.buffer_before_minutes),
       a.ends_at + make_interval(mins => a.buffer_after_minutes)
FROM appointments a
JOIN drivers d ON d.id = sqlc.arg(driver_id)::uuid AND d.active
WHERE a.id = sqlc.arg(appointment_id)::uuid;

-- name: InsertAppointmentResource :execrows
INSERT INTO appointment_resources (
    appointment_id, resource_id, purpose, exclusive, active, reserved_starts_at, reserved_ends_at
)
SELECT a.id, r.id, sqlc.arg(purpose), r.exclusive,
       a.lifecycle_status IN ('proposal', 'fixed'),
       a.starts_at - make_interval(mins => a.buffer_before_minutes),
       a.ends_at + make_interval(mins => a.buffer_after_minutes)
FROM appointments a
JOIN resources r ON r.id = sqlc.arg(resource_id)::uuid AND r.active
WHERE a.id = sqlc.arg(appointment_id)::uuid;

-- name: RefreshAppointmentReservations :exec
UPDATE appointment_drivers d SET
    active = a.lifecycle_status IN ('proposal', 'fixed'),
    reserved_starts_at = a.starts_at - make_interval(mins => a.buffer_before_minutes),
    reserved_ends_at = a.ends_at + make_interval(mins => a.buffer_after_minutes)
FROM appointments a WHERE a.id = d.appointment_id AND a.id = sqlc.arg(appointment_id)::uuid;

-- name: RefreshAppointmentResourceReservations :exec
UPDATE appointment_resources r SET
    active = a.lifecycle_status IN ('proposal', 'fixed'),
    reserved_starts_at = a.starts_at - make_interval(mins => a.buffer_before_minutes),
    reserved_ends_at = a.ends_at + make_interval(mins => a.buffer_after_minutes)
FROM appointments a WHERE a.id = r.appointment_id AND a.id = sqlc.arg(appointment_id)::uuid;

-- name: SetJobWorkflow :exec
UPDATE jobs SET workflow_status = sqlc.arg(workflow_status), version = version + 1, updated_at = now()
WHERE id = sqlc.arg(job_id)::uuid;

-- name: RemoveWaitlistScheduled :exec
UPDATE waitlist_entries SET removed_at = now(), removed_reason = 'scheduled', version = version + 1
WHERE job_id = sqlc.arg(job_id)::uuid AND removed_at IS NULL;

-- name: RestoreWaitlistAfterCancellation :exec
INSERT INTO waitlist_entries (job_id)
SELECT sqlc.arg(job_id)::uuid
WHERE NOT EXISTS (SELECT 1 FROM waitlist_entries WHERE job_id = sqlc.arg(job_id)::uuid AND removed_at IS NULL);

-- name: InsertOutboxEvent :exec
INSERT INTO outbox_events (event_type, aggregate_type, aggregate_id, payload, idempotency_key)
VALUES (sqlc.arg(event_type), 'appointment', sqlc.arg(aggregate_id)::uuid,
        sqlc.arg(payload)::jsonb, sqlc.arg(idempotency_key))
ON CONFLICT (idempotency_key) DO NOTHING;

-- name: ListAppointmentDrivers :many
SELECT ad.appointment_id::text AS appointment_id, ad.driver_id::text AS driver_id, d.display_name, ad.is_primary
FROM appointment_drivers ad JOIN drivers d ON d.id = ad.driver_id
WHERE ad.appointment_id = ANY(sqlc.arg(appointment_ids)::uuid[])
ORDER BY ad.appointment_id, ad.is_primary DESC, lower(d.display_name);

-- name: ListAppointmentResources :many
SELECT ar.appointment_id::text AS appointment_id, ar.resource_id::text AS resource_id, r.name, r.resource_type, ar.purpose
FROM appointment_resources ar JOIN resources r ON r.id = ar.resource_id
WHERE ar.appointment_id = ANY(sqlc.arg(appointment_ids)::uuid[])
ORDER BY ar.appointment_id, lower(r.name);

-- name: ListCalendarAppointments :many
SELECT a.id::text, a.job_id::text, j.job_number, a.lifecycle_status, a.confirmation_status,
       a.starts_at, a.ends_at, a.buffer_before_minutes, a.buffer_after_minutes, a.version,
       j.job_type, j.volume_m3::text, c.id::text AS customer_id,
       concat_ws(' ', NULLIF(c.first_name, ''), NULLIF(c.last_name, ''), NULLIF(c.company_name, ''))::text AS customer_name,
       c.locality, c.street, c.postal_code,
       COALESCE(c.latitude::text, '')::text AS latitude,
       COALESCE(c.longitude::text, '')::text AS longitude
FROM appointments a
JOIN jobs j ON j.id = a.job_id
JOIN customers c ON c.id = j.customer_id
WHERE a.starts_at < sqlc.arg(to_utc)::timestamptz AND a.ends_at > sqlc.arg(from_utc)::timestamptz
ORDER BY a.starts_at, a.id;

-- name: ListActiveDriversForPlanning :many
SELECT id::text, display_name, can_complete_jobs FROM drivers WHERE active ORDER BY lower(display_name), id;

-- name: ListActiveResourcesForPlanning :many
SELECT id::text, name, resource_type, exclusive FROM resources WHERE active ORDER BY resource_type, lower(name), id;

-- name: DriverCanCompleteAppointment :one
SELECT EXISTS (
    SELECT 1 FROM appointment_drivers ad
    JOIN drivers d ON d.id = ad.driver_id
    WHERE ad.appointment_id = sqlc.arg(appointment_id)::uuid
      AND ad.driver_id = sqlc.arg(driver_id)::uuid
      AND d.active AND d.can_complete_jobs
)::boolean;

-- name: AppointmentAssignmentsReady :one
SELECT (
    EXISTS (
        SELECT 1 FROM appointment_drivers ad JOIN drivers d ON d.id=ad.driver_id
        WHERE ad.appointment_id=sqlc.arg(appointment_id)::uuid AND ad.is_primary AND d.active
    )
    AND NOT EXISTS (
        SELECT 1 FROM appointment_drivers ad JOIN drivers d ON d.id=ad.driver_id
        WHERE ad.appointment_id=sqlc.arg(appointment_id)::uuid AND NOT d.active
    )
    AND EXISTS (
        SELECT 1 FROM appointment_resources ar JOIN resources r ON r.id=ar.resource_id
        WHERE ar.appointment_id=sqlc.arg(appointment_id)::uuid AND ar.purpose='chipping'
          AND r.resource_type='chipper' AND r.active
    )
    AND NOT EXISTS (
        SELECT 1 FROM appointment_resources ar JOIN resources r ON r.id=ar.resource_id
        WHERE ar.appointment_id=sqlc.arg(appointment_id)::uuid AND NOT r.active
    )
    AND (
        sqlc.arg(job_type)::text <> 'chipping_with_transport'
        OR (sqlc.arg(transport_mode)::text='external' AND sqlc.arg(external_transport_confirmed)::boolean)
        OR (sqlc.arg(transport_mode)::text='internal' AND EXISTS (
            SELECT 1 FROM appointment_resources ar JOIN resources r ON r.id=ar.resource_id
            WHERE ar.appointment_id=sqlc.arg(appointment_id)::uuid AND ar.purpose='transport'
              AND r.resource_type='transport_vehicle' AND r.active
        ))
    )
)::boolean;

-- name: ListWaitlistForPlanning :many
SELECT w.id::text AS waitlist_id, j.id::text AS job_id, j.job_number,
       j.job_type, j.volume_m3::text, j.estimated_hack_minutes,
       concat_ws(' ', NULLIF(c.first_name, ''), NULLIF(c.last_name, ''), NULLIF(c.company_name, ''))::text AS customer_name,
       c.locality
FROM waitlist_entries w
JOIN jobs j ON j.id = w.job_id
JOIN customers c ON c.id = j.customer_id
WHERE w.removed_at IS NULL AND j.archived_at IS NULL AND j.workflow_status = 'waitlist'
ORDER BY w.manual_priority DESC, w.entered_at, w.id;

-- name: FindAppointmentConflicts :many
SELECT 'driver'::text AS conflict_type, d.driver_id::text AS subject_id, dr.display_name AS subject_name,
       a.id::text AS appointment_id, a.starts_at, a.ends_at
FROM appointment_drivers d
JOIN drivers dr ON dr.id = d.driver_id
JOIN appointments a ON a.id = d.appointment_id
WHERE d.active AND d.driver_id = ANY(sqlc.arg(driver_ids)::uuid[])
  AND d.reserved_range && tstzrange(sqlc.arg(from_utc)::timestamptz, sqlc.arg(to_utc)::timestamptz, '[)')
  AND (sqlc.arg(exclude_appointment_id)::text = '' OR d.appointment_id <> sqlc.arg(exclude_appointment_id)::uuid)
UNION ALL
SELECT 'resource'::text, r.resource_id::text, rs.name,
       a.id::text, a.starts_at, a.ends_at
FROM appointment_resources r
JOIN resources rs ON rs.id = r.resource_id
JOIN appointments a ON a.id = r.appointment_id
WHERE r.active AND r.exclusive AND r.resource_id = ANY(sqlc.arg(resource_ids)::uuid[])
  AND r.reserved_range && tstzrange(sqlc.arg(from_utc)::timestamptz, sqlc.arg(to_utc)::timestamptz, '[)')
  AND (sqlc.arg(exclude_appointment_id)::text = '' OR r.appointment_id <> sqlc.arg(exclude_appointment_id)::uuid)
ORDER BY starts_at, conflict_type, subject_name;
