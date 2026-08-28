-- name: GetPlanningInput :one
SELECT j.id::text AS job_id, j.job_number, j.version AS job_version, j.workflow_status,
       j.job_type, j.estimated_hack_minutes, j.estimated_transport_minutes,
       j.transport_mode, j.external_transport_confirmed, j.urgency, j.received_at,
       COALESCE(j.preferred_start_date::text, '')::text AS preferred_start_date,
       COALESCE(j.preferred_end_date::text, '')::text AS preferred_end_date,
       COALESCE(j.region, '')::text AS region,
       w.id::text AS waitlist_id, w.version AS waitlist_version, w.entered_at,
       c.version AS customer_version,
       COALESCE(j.pile_latitude::text, '')::text AS latitude,
       COALESCE(j.pile_longitude::text, '')::text AS longitude
FROM jobs j
JOIN waitlist_entries w ON w.job_id=j.id AND w.removed_at IS NULL
JOIN customers c ON c.id=j.customer_id
WHERE j.id=sqlc.arg(job_id)::uuid AND j.archived_at IS NULL;

-- name: CurrentPlanningInputFingerprint :one
WITH payload AS (
    SELECT jsonb_build_object(
        'target', (
            SELECT jsonb_build_array(
                j.version, w.version, c.version, j.workflow_status, j.job_type,
                j.estimated_hack_minutes, j.estimated_transport_minutes, j.transport_mode,
                j.external_transport_confirmed, j.urgency, j.preferred_start_date,
                j.preferred_end_date, j.region, j.pile_latitude, j.pile_longitude
            )
            FROM jobs j
            JOIN waitlist_entries w ON w.job_id=j.id AND w.removed_at IS NULL
            JOIN customers c ON c.id=j.customer_id
            WHERE j.id=sqlc.arg(job_id)::uuid AND j.archived_at IS NULL
        ),
        'drivers', (
            SELECT COALESCE(jsonb_agg(jsonb_build_array(id, version, can_complete_jobs) ORDER BY id), '[]'::jsonb)
            FROM drivers WHERE active
        ),
        'availability_rules', (
            SELECT COALESCE(jsonb_agg(jsonb_build_array(
                ar.id, ar.driver_id, ar.version, ar.iso_weekday, ar.local_start, ar.local_end,
                ar.valid_from, ar.valid_until, ar.status
            ) ORDER BY ar.id), '[]'::jsonb)
            FROM availability_rules ar JOIN drivers d ON d.id=ar.driver_id AND d.active
        ),
        'availability_exceptions', (
            SELECT COALESCE(jsonb_agg(jsonb_build_array(
                ae.id, ae.driver_id, ae.version, ae.exception_type, ae.all_day,
                ae.local_date, ae.starts_at, ae.ends_at
            ) ORDER BY ae.id), '[]'::jsonb)
            FROM availability_exceptions ae JOIN drivers d ON d.id=ae.driver_id AND d.active
        ),
        'resources', (
            SELECT COALESCE(jsonb_agg(jsonb_build_array(id, version, resource_type, exclusive) ORDER BY id), '[]'::jsonb)
            FROM resources WHERE active
        ),
        'reservations', (
            SELECT COALESCE(jsonb_agg(jsonb_build_object(
                'appointment', jsonb_build_array(
                    a.id, a.version,
                    a.starts_at - make_interval(mins => a.buffer_before_minutes),
                    a.ends_at + make_interval(mins => a.buffer_after_minutes),
                    j.version, j.pile_latitude, j.pile_longitude
                ),
                'drivers', ARRAY(SELECT ad.driver_id FROM appointment_drivers ad WHERE ad.appointment_id=a.id AND ad.active ORDER BY ad.driver_id),
                'resources', ARRAY(SELECT ar.resource_id FROM appointment_resources ar WHERE ar.appointment_id=a.id AND ar.active ORDER BY ar.resource_id)
            ) ORDER BY a.id), '[]'::jsonb)
            FROM appointments a
            JOIN jobs j ON j.id=a.job_id
            JOIN customers c ON c.id=j.customer_id
            WHERE a.lifecycle_status IN ('proposal','fixed')
              AND a.starts_at - make_interval(mins => a.buffer_before_minutes) < sqlc.arg(search_to)::timestamptz
              AND a.ends_at + make_interval(mins => a.buffer_after_minutes) > sqlc.arg(search_from)::timestamptz
        )
    ) AS value
)
SELECT digest(convert_to(value::text, 'UTF8'), 'sha256')::bytea FROM payload;

-- name: ListPlanningReservations :many
SELECT a.id::text AS appointment_id, a.version,
       (a.starts_at - make_interval(mins => a.buffer_before_minutes))::timestamptz AS starts_at,
       (a.ends_at + make_interval(mins => a.buffer_after_minutes))::timestamptz AS ends_at,
       COALESCE(array_agg(DISTINCT ad.driver_id::text) FILTER (WHERE ad.driver_id IS NOT NULL), ARRAY[]::text[])::text[] AS driver_ids,
       COALESCE(array_agg(DISTINCT ar.resource_id::text) FILTER (WHERE ar.resource_id IS NOT NULL), ARRAY[]::text[])::text[] AS resource_ids,
       COALESCE(j.pile_latitude::text, '')::text AS latitude,
       COALESCE(j.pile_longitude::text, '')::text AS longitude
FROM appointments a
JOIN jobs j ON j.id=a.job_id
JOIN customers c ON c.id=j.customer_id
LEFT JOIN appointment_drivers ad ON ad.appointment_id=a.id AND ad.active
LEFT JOIN appointment_resources ar ON ar.appointment_id=a.id AND ar.active
WHERE a.lifecycle_status IN ('proposal','fixed')
  AND a.starts_at - make_interval(mins => a.buffer_before_minutes) < sqlc.arg(search_to)::timestamptz
  AND a.ends_at + make_interval(mins => a.buffer_after_minutes) > sqlc.arg(search_from)::timestamptz
GROUP BY a.id, j.id
ORDER BY a.starts_at, a.id;

-- name: InsertPlanningRun :one
INSERT INTO planning_runs (job_id, actor_user_id, job_version, waitlist_version, search_from, search_to,
                           input_fingerprint, config_snapshot, expires_at)
VALUES (sqlc.arg(job_id)::uuid, sqlc.arg(actor_user_id)::uuid, sqlc.arg(job_version), sqlc.arg(waitlist_version),
        sqlc.arg(search_from)::timestamptz, sqlc.arg(search_to)::timestamptz,
        sqlc.arg(input_fingerprint)::bytea, sqlc.arg(config_snapshot)::jsonb, sqlc.arg(expires_at)::timestamptz)
RETURNING id::text;

-- name: InsertPlanningSuggestion :one
INSERT INTO planning_suggestions (run_id, rank, starts_at, ends_at, driver_id, resource_ids,
                                  resource_purposes, score, components, reasons, warnings,
                                  routing_source, distance_meters, duration_seconds)
VALUES (sqlc.arg(run_id)::uuid, sqlc.arg(rank), sqlc.arg(starts_at)::timestamptz,
        sqlc.arg(ends_at)::timestamptz, sqlc.arg(driver_id)::uuid, sqlc.arg(resource_ids)::uuid[],
        sqlc.arg(resource_purposes)::text[], sqlc.arg(score)::numeric, sqlc.arg(components)::jsonb,
        sqlc.arg(reasons)::text[], sqlc.arg(warnings)::text[], sqlc.arg(routing_source),
        sqlc.narg(distance_meters)::integer, sqlc.narg(duration_seconds)::integer)
RETURNING id::text;

-- name: ListPlanningSuggestions :many
SELECT s.id::text, s.run_id::text, s.rank, s.starts_at, s.ends_at,
       s.driver_id::text, d.display_name AS driver_name,
       s.resource_ids::text[] AS resource_ids, s.resource_purposes,
       ARRAY(SELECT rsrc.name FROM resources rsrc WHERE rsrc.id=ANY(s.resource_ids) ORDER BY lower(rsrc.name), rsrc.id)::text[] AS resource_names,
       s.score::text,
       s.components, s.reasons, s.warnings, s.routing_source,
       s.distance_meters, s.duration_seconds, s.status, r.job_id::text, r.job_version,
       r.waitlist_version, r.created_at, r.expires_at
FROM planning_suggestions s
JOIN planning_runs r ON r.id=s.run_id
JOIN drivers d ON d.id=s.driver_id
WHERE s.run_id=sqlc.arg(run_id)::uuid
ORDER BY s.rank;

-- name: GetPlanningSuggestionForUpdate :one
SELECT s.id::text, s.run_id::text, s.starts_at, s.ends_at, s.driver_id::text,
       s.resource_ids::text[] AS resource_ids, s.resource_purposes, s.status,
       r.job_id::text, r.job_version, r.waitlist_version, r.expires_at,
       r.search_from, r.search_to, r.input_fingerprint,
       j.workflow_status, j.version AS current_job_version, w.version AS current_waitlist_version,
       j.job_type, j.transport_mode, j.external_transport_confirmed,
       CASE
         WHEN EXISTS (
           SELECT 1 FROM availability_exceptions e WHERE e.driver_id=s.driver_id AND e.exception_type='available_override'
             AND ((e.all_day AND e.local_date=(s.starts_at AT TIME ZONE 'Europe/Vienna')::date)
               OR (NOT e.all_day AND e.starts_at<=s.starts_at AND e.ends_at>=s.ends_at))
         ) THEN true
         WHEN EXISTS (
           SELECT 1 FROM availability_exceptions e WHERE e.driver_id=s.driver_id AND e.exception_type<>'available_override'
             AND ((e.all_day AND e.local_date=(s.starts_at AT TIME ZONE 'Europe/Vienna')::date)
               OR (NOT e.all_day AND e.starts_at<s.ends_at AND e.ends_at>s.starts_at))
         ) THEN false
         ELSE EXISTS (
           SELECT 1 FROM availability_rules ar WHERE ar.driver_id=s.driver_id AND ar.status='available'
             AND ar.iso_weekday=EXTRACT(ISODOW FROM s.starts_at AT TIME ZONE 'Europe/Vienna')::smallint
             AND ar.valid_from<=(s.starts_at AT TIME ZONE 'Europe/Vienna')::date
             AND (ar.valid_until IS NULL OR ar.valid_until>=(s.starts_at AT TIME ZONE 'Europe/Vienna')::date)
             AND ar.local_start<=(s.starts_at AT TIME ZONE 'Europe/Vienna')::time
             AND ar.local_end>=(s.ends_at AT TIME ZONE 'Europe/Vienna')::time
         )
       END::boolean AS driver_available
FROM planning_suggestions s
JOIN planning_runs r ON r.id=s.run_id
JOIN jobs j ON j.id=r.job_id
JOIN waitlist_entries w ON w.job_id=j.id AND w.removed_at IS NULL
WHERE s.id=sqlc.arg(id)::uuid
FOR UPDATE OF s, r, j, w;

-- name: LockPlanningDriver :one
SELECT id::text FROM drivers
WHERE id=sqlc.arg(id)::uuid AND active
FOR SHARE;

-- name: LockPlanningResources :many
SELECT id::text FROM resources
WHERE id=ANY(sqlc.arg(ids)::uuid[]) AND active
ORDER BY id
FOR SHARE;

-- name: PlanningDriverAvailable :one
SELECT CASE
    WHEN EXISTS (
      SELECT 1 FROM availability_exceptions e WHERE e.driver_id=sqlc.arg(driver_id)::uuid AND e.exception_type<>'available_override'
        AND ((e.all_day AND e.local_date=(sqlc.arg(starts_at)::timestamptz AT TIME ZONE 'Europe/Vienna')::date)
          OR (NOT e.all_day AND e.starts_at<sqlc.arg(ends_at)::timestamptz AND e.ends_at>sqlc.arg(starts_at)::timestamptz))
    ) THEN false
    WHEN EXISTS (
      SELECT 1 FROM availability_exceptions e WHERE e.driver_id=sqlc.arg(driver_id)::uuid AND e.exception_type='available_override'
        AND ((e.all_day AND e.local_date=(sqlc.arg(starts_at)::timestamptz AT TIME ZONE 'Europe/Vienna')::date)
          OR (NOT e.all_day AND e.starts_at<=sqlc.arg(starts_at)::timestamptz AND e.ends_at>=sqlc.arg(ends_at)::timestamptz))
    ) THEN true
    ELSE EXISTS (
      SELECT 1 FROM availability_rules ar WHERE ar.driver_id=sqlc.arg(driver_id)::uuid AND ar.status='available'
        AND ar.iso_weekday=EXTRACT(ISODOW FROM sqlc.arg(starts_at)::timestamptz AT TIME ZONE 'Europe/Vienna')::smallint
        AND ar.valid_from<=(sqlc.arg(starts_at)::timestamptz AT TIME ZONE 'Europe/Vienna')::date
        AND (ar.valid_until IS NULL OR ar.valid_until>=(sqlc.arg(starts_at)::timestamptz AT TIME ZONE 'Europe/Vienna')::date)
        AND ar.local_start<=(sqlc.arg(starts_at)::timestamptz AT TIME ZONE 'Europe/Vienna')::time
        AND ar.local_end>=(sqlc.arg(ends_at)::timestamptz AT TIME ZONE 'Europe/Vienna')::time
    )
END::boolean;

-- name: InsertAdoptedProposal :one
INSERT INTO appointments (job_id, lifecycle_status, starts_at, ends_at, buffer_before_minutes, buffer_after_minutes)
VALUES (sqlc.arg(job_id)::uuid, 'proposal', sqlc.arg(starts_at)::timestamptz, sqlc.arg(ends_at)::timestamptz,
        sqlc.arg(buffer_before_minutes), sqlc.arg(buffer_after_minutes))
RETURNING id::text;

-- name: MarkPlanningSuggestionAdopted :execrows
UPDATE planning_suggestions SET status='adopted', adopted_appointment_id=sqlc.arg(appointment_id)::uuid,
    adopted_at=now()
WHERE id=sqlc.arg(id)::uuid AND status='pending';

-- name: DiscardOtherPlanningSuggestions :exec
UPDATE planning_suggestions SET status='discarded'
WHERE run_id=sqlc.arg(run_id)::uuid AND id<>sqlc.arg(adopted_id)::uuid AND status='pending';

-- name: ListPlanningClusterEntries :many
SELECT j.id::text AS job_id, COALESCE(NULLIF(j.region, ''), NULLIF(c.locality, ''), '')::text AS region,
       COALESCE(j.pile_latitude::text, '')::text AS latitude,
       COALESCE(j.pile_longitude::text, '')::text AS longitude
FROM waitlist_entries w
JOIN jobs j ON j.id=w.job_id
JOIN customers c ON c.id=j.customer_id
WHERE w.removed_at IS NULL AND j.archived_at IS NULL AND j.workflow_status='waitlist'
ORDER BY j.id
LIMIT 500;
