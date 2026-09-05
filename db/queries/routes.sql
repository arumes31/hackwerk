-- name: ListRouteCandidates :many
SELECT j.id::text AS job_id, j.job_number, j.version AS job_version, w.version AS waitlist_version, j.workflow_status,
       j.job_type, j.transport_mode, j.external_transport_confirmed,
       j.estimated_hack_minutes, j.estimated_transport_minutes, j.volume_m3::text,
       COALESCE(NULLIF(j.region, ''), NULLIF(c.locality, ''), '')::text AS region,
       concat_ws(' ', NULLIF(c.first_name, ''), NULLIF(c.last_name, ''), NULLIF(c.company_name, ''))::text AS customer_name,
       c.locality,
       j.pile_latitude::text AS latitude, j.pile_longitude::text AS longitude,
       j.pile_location_source,
       EXISTS (
           SELECT 1 FROM appointments active_appointment
           WHERE active_appointment.job_id=j.id
             AND active_appointment.lifecycle_status IN ('draft', 'proposal', 'fixed')
       ) AS has_active_appointment
FROM jobs j
JOIN customers c ON c.id=j.customer_id
JOIN waitlist_entries w ON w.job_id=j.id AND w.removed_at IS NULL
WHERE j.archived_at IS NULL AND c.archived_at IS NULL
  AND j.workflow_status IN ('waitlist', 'planning')
  AND j.pile_latitude IS NOT NULL AND j.pile_longitude IS NOT NULL
  AND (
      cardinality(sqlc.arg(job_ids)::uuid[]) = 0
      OR (
          j.id=ANY(sqlc.arg(job_ids)::uuid[])
          AND NOT EXISTS (
              SELECT 1 FROM appointments selected_appointment
              WHERE selected_appointment.job_id=j.id
                AND selected_appointment.lifecycle_status IN ('draft', 'proposal', 'fixed')
          )
      )
  )
ORDER BY j.received_at, j.id
LIMIT 500;

-- name: ListRouteMissingLocations :many
SELECT j.id::text AS job_id, j.job_number,
       concat_ws(' ', NULLIF(c.first_name, ''), NULLIF(c.last_name, ''), NULLIF(c.company_name, ''))::text AS customer_name,
       COALESCE(NULLIF(j.region, ''), NULLIF(c.locality, ''), '')::text AS region
FROM jobs j
JOIN customers c ON c.id=j.customer_id
JOIN waitlist_entries w ON w.job_id=j.id AND w.removed_at IS NULL
WHERE j.archived_at IS NULL AND c.archived_at IS NULL
  AND j.workflow_status IN ('waitlist', 'planning')
  AND (j.pile_latitude IS NULL OR j.pile_longitude IS NULL)
ORDER BY j.received_at, j.id
LIMIT 100;

-- name: ListRouteStopPhones :many
SELECT j.id::text AS job_id, COALESCE(c.phone_raw, '')::text AS customer_phone
FROM jobs j
JOIN customers c ON c.id=j.customer_id
WHERE j.id=ANY(sqlc.arg(job_ids)::uuid[]);

-- name: ListRouteDrivers :many
SELECT id::text, display_name, version
FROM drivers
WHERE active AND can_complete_jobs
ORDER BY lower(display_name), id;

-- name: ListRouteResources :many
SELECT id::text, name, resource_type, exclusive, version
FROM resources
WHERE active AND resource_type IN ('chipper', 'transport_vehicle')
ORDER BY resource_type, lower(name), id;

-- name: InsertRouteDraft :one
INSERT INTO route_drafts (
    actor_user_id, driver_id, chipper_resource_id, transport_resource_id,
    departure_at, start_label, start_latitude, start_longitude, end_label, end_latitude, end_longitude,
    routing_source, distance_meters, duration_seconds, route_geometry
) VALUES (
    sqlc.arg(actor_user_id)::uuid, sqlc.arg(driver_id)::uuid, sqlc.narg(chipper_resource_id)::uuid,
    NULLIF(sqlc.arg(transport_resource_id)::text, '')::uuid,
    sqlc.arg(departure_at)::timestamptz, sqlc.arg(start_label), sqlc.arg(start_latitude)::numeric,
    sqlc.arg(start_longitude)::numeric, sqlc.arg(end_label), sqlc.arg(end_latitude)::numeric,
    sqlc.arg(end_longitude)::numeric, sqlc.arg(routing_source),
    sqlc.arg(distance_meters), sqlc.arg(duration_seconds), sqlc.arg(route_geometry)::jsonb
)
RETURNING id::text, version;

-- name: InsertRouteStop :one
INSERT INTO route_stops (
    route_draft_id, job_id, job_version, waitlist_version, position, travel_distance_meters,
    travel_duration_seconds, planned_starts_at, planned_ends_at
) VALUES (
    sqlc.arg(route_draft_id)::uuid, sqlc.arg(job_id)::uuid, sqlc.arg(job_version), sqlc.arg(waitlist_version), sqlc.arg(position),
    sqlc.arg(travel_distance_meters), sqlc.arg(travel_duration_seconds),
    sqlc.arg(planned_starts_at)::timestamptz, sqlc.arg(planned_ends_at)::timestamptz
)
RETURNING id::text;

-- name: UpdateRouteDraft :execrows
UPDATE route_drafts
SET actor_user_id=sqlc.arg(actor_user_id)::uuid,
    driver_id=sqlc.arg(driver_id)::uuid,
    chipper_resource_id=sqlc.narg(chipper_resource_id)::uuid,
    transport_resource_id=NULLIF(sqlc.arg(transport_resource_id)::text, '')::uuid,
    departure_at=sqlc.arg(departure_at)::timestamptz,
    start_label=sqlc.arg(start_label),
    start_latitude=sqlc.arg(start_latitude)::numeric,
    start_longitude=sqlc.arg(start_longitude)::numeric,
    end_label=sqlc.arg(end_label),
    end_latitude=sqlc.arg(end_latitude)::numeric,
    end_longitude=sqlc.arg(end_longitude)::numeric,
    routing_source=sqlc.arg(routing_source),
    distance_meters=sqlc.arg(distance_meters),
    duration_seconds=sqlc.arg(duration_seconds),
    route_geometry=sqlc.arg(route_geometry)::jsonb,
    version=version+1,
    updated_at=now()
WHERE id=sqlc.arg(id)::uuid AND version=sqlc.arg(expected_version) AND status='draft';

-- name: DeleteRouteStops :exec
DELETE FROM route_stops
WHERE route_draft_id=sqlc.arg(route_draft_id)::uuid;

-- name: GetRouteDraft :one
SELECT rd.id::text, rd.actor_user_id::text, rd.driver_id::text, d.display_name AS driver_name,
       COALESCE(rd.chipper_resource_id::text, '')::text AS rd_chipper_resource_id,
       COALESCE(chipper.name, '')::text AS chipper_name,
       COALESCE(rd.transport_resource_id::text, '')::text AS transport_resource_id,
       COALESCE(transport.name, '')::text AS transport_name,
       rd.departure_at, rd.start_label, rd.start_latitude::text, rd.start_longitude::text,
       rd.end_label, rd.end_latitude::text, rd.end_longitude::text, rd.status, rd.routing_source,
       rd.distance_meters, rd.duration_seconds, rd.route_geometry, rd.assigned_at,
       rd.version, rd.created_at, rd.updated_at
FROM route_drafts rd
JOIN drivers d ON d.id=rd.driver_id
LEFT JOIN resources chipper ON chipper.id=rd.chipper_resource_id
LEFT JOIN resources transport ON transport.id=rd.transport_resource_id
WHERE rd.id=sqlc.arg(id)::uuid;

-- name: ListRouteStops :many
SELECT rs.id::text, rs.route_draft_id::text, rs.job_id::text, rs.job_version, rs.waitlist_version, rs.position,
       rs.travel_distance_meters, rs.travel_duration_seconds,
       rs.planned_starts_at, rs.planned_ends_at,
       COALESCE(rs.appointment_id::text, '')::text AS appointment_id,
       j.job_number, j.job_type, j.transport_mode, j.external_transport_confirmed,
       j.estimated_hack_minutes, j.estimated_transport_minutes, j.volume_m3::text,
       COALESCE(NULLIF(j.region, ''), NULLIF(c.locality, ''), '')::text AS region,
       concat_ws(' ', NULLIF(c.first_name, ''), NULLIF(c.last_name, ''), NULLIF(c.company_name, ''))::text AS customer_name,
       c.locality, COALESCE(j.pile_latitude::text, '')::text AS latitude,
       COALESCE(j.pile_longitude::text, '')::text AS longitude,
       COALESCE(j.pile_location_source, '')::text AS pile_location_source
FROM route_stops rs
JOIN jobs j ON j.id=rs.job_id
JOIN customers c ON c.id=j.customer_id
WHERE rs.route_draft_id=sqlc.arg(route_draft_id)::uuid
ORDER BY rs.position, rs.id;

-- name: LockRouteDraft :one
SELECT rd.id::text, rd.driver_id::text,
       COALESCE(rd.chipper_resource_id::text, '')::text AS rd_chipper_resource_id,
       COALESCE(rd.transport_resource_id::text, '')::text AS transport_resource_id,
       rd.departure_at, rd.duration_seconds, rd.status, rd.version
FROM route_drafts rd
WHERE rd.id=sqlc.arg(id)::uuid
FOR UPDATE;

-- name: LockRouteStopsForAssignment :many
SELECT rs.id::text, rs.job_id::text, rs.job_version, rs.waitlist_version, rs.position,
       rs.travel_duration_seconds, rs.planned_starts_at, rs.planned_ends_at,
       j.version AS current_job_version, j.workflow_status, j.archived_at,
       j.job_type, j.transport_mode, j.external_transport_confirmed,
       COALESCE(j.pile_latitude::text, '')::text AS latitude,
       COALESCE(j.pile_longitude::text, '')::text AS longitude,
       COALESCE(w.id::text, '')::text AS waitlist_id, COALESCE(w.version, 0)::integer AS current_waitlist_version
FROM route_stops rs
JOIN jobs j ON j.id=rs.job_id
JOIN waitlist_entries w ON w.job_id=j.id AND w.removed_at IS NULL
WHERE rs.route_draft_id=sqlc.arg(route_draft_id)::uuid
ORDER BY rs.position, rs.id
FOR UPDATE OF rs, j, w;

-- name: UpdateRouteStopPosition :execrows
UPDATE route_stops
SET position=sqlc.arg(position)
WHERE id=sqlc.arg(id)::uuid AND route_draft_id=sqlc.arg(route_draft_id)::uuid;

-- name: UpdateRouteStopTravel :execrows
UPDATE route_stops
SET travel_distance_meters=sqlc.arg(travel_distance_meters),
    travel_duration_seconds=sqlc.arg(travel_duration_seconds)
WHERE id=sqlc.arg(id)::uuid AND route_draft_id=sqlc.arg(route_draft_id)::uuid;

-- name: UpdateRouteDraftMetrics :execrows
UPDATE route_drafts
SET routing_source=sqlc.arg(routing_source), distance_meters=sqlc.arg(distance_meters),
    duration_seconds=sqlc.arg(duration_seconds), route_geometry=sqlc.arg(route_geometry)::jsonb,
    version=version+1, updated_at=now()
WHERE id=sqlc.arg(id)::uuid AND version=sqlc.arg(expected_version)
  AND status='assigned';

-- name: LinkRouteStopAppointment :execrows
UPDATE route_stops
SET appointment_id=sqlc.arg(appointment_id)::uuid
WHERE id=sqlc.arg(id)::uuid AND route_draft_id=sqlc.arg(route_draft_id)::uuid
  AND appointment_id IS NULL;

-- name: SetRouteDraftAssigned :execrows
UPDATE route_drafts
SET status='assigned', assigned_at=now(), version=version+1, updated_at=now()
WHERE id=sqlc.arg(id)::uuid AND version=sqlc.arg(expected_version) AND status='draft';

-- name: LatestAssignedRouteForDriver :one
SELECT id::text
FROM route_drafts
WHERE driver_id=sqlc.arg(driver_id)::uuid AND status='assigned'
  AND (departure_at AT TIME ZONE 'Europe/Vienna')::date=sqlc.arg(local_date)::date
ORDER BY departure_at DESC, id DESC
LIMIT 1;

-- name: AssignedRouteExistsForDriver :one
SELECT EXISTS (
  SELECT 1
  FROM route_drafts
  WHERE driver_id=sqlc.arg(driver_id)::uuid AND status='assigned'
    AND (departure_at AT TIME ZONE 'Europe/Vienna')::date=sqlc.arg(local_date)::date
);

-- name: ListDraftRouteIDsForDate :many
SELECT id::text
FROM route_drafts
WHERE status='draft'
  AND (departure_at AT TIME ZONE 'Europe/Vienna')::date=sqlc.arg(local_date)::date
ORDER BY departure_at, driver_id, id
LIMIT 50;
