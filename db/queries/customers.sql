-- name: NextJobNumber :one
WITH vienna_year AS (
    SELECT EXTRACT(YEAR FROM now() AT TIME ZONE 'Europe/Vienna')::integer AS value
)
INSERT INTO job_number_counters (year, next_value)
SELECT value, 2 FROM vienna_year
ON CONFLICT (year) DO UPDATE SET next_value = job_number_counters.next_value + 1
RETURNING year, next_value - 1 AS sequence;

-- name: InsertCustomer :one
INSERT INTO customers (
    first_name, last_name, company_name, street, postal_code, locality, region, country_code,
    address_freeform, phone_raw, phone_normalized, email, notification_preference,
    latitude, longitude, location_source, geocoding_status
) VALUES (
    sqlc.arg(first_name), sqlc.arg(last_name), NULLIF(sqlc.arg(company_name)::text, ''),
    sqlc.arg(street), sqlc.arg(postal_code), sqlc.arg(locality), sqlc.arg(region), sqlc.arg(country_code),
    NULLIF(sqlc.arg(address_freeform)::text, ''), NULLIF(sqlc.arg(phone_raw)::text, ''),
    NULLIF(sqlc.arg(phone_normalized)::text, ''), NULLIF(sqlc.arg(email)::text, '')::citext,
    sqlc.arg(notification_preference), NULLIF(sqlc.arg(latitude)::text, '')::numeric,
    NULLIF(sqlc.arg(longitude)::text, '')::numeric,
    CASE WHEN NULLIF(sqlc.arg(latitude)::text, '') IS NULL THEN NULL ELSE 'manual' END,
    CASE WHEN NULLIF(sqlc.arg(latitude)::text, '') IS NULL THEN 'not_requested' ELSE 'resolved' END
) RETURNING id::text;

-- name: InsertJob :one
INSERT INTO jobs (
    job_number, customer_id, job_type, volume_m3, estimated_hack_minutes,
    estimated_transport_minutes, transport_trip_count, transport_mode, external_transport_confirmed,
    preferred_start_date, preferred_end_date, preference_mode, preference_text, urgency, region, source,
    pile_latitude, pile_longitude, pile_location_source, pile_location_updated_at
) VALUES (
    sqlc.arg(job_number), sqlc.arg(customer_id)::uuid, sqlc.arg(job_type), sqlc.arg(volume_m3)::numeric,
    sqlc.arg(estimated_hack_minutes), sqlc.arg(estimated_transport_minutes), sqlc.arg(transport_trip_count),
    sqlc.arg(transport_mode), sqlc.arg(external_transport_confirmed),
    NULLIF(sqlc.arg(preferred_start_date)::text, '')::date, NULLIF(sqlc.arg(preferred_end_date)::text, '')::date,
    sqlc.arg(preference_mode), NULLIF(sqlc.arg(preference_text)::text, ''), sqlc.arg(urgency), NULLIF(sqlc.arg(region)::text, ''), sqlc.arg(source),
    NULLIF(sqlc.arg(pile_latitude)::text, '')::numeric, NULLIF(sqlc.arg(pile_longitude)::text, '')::numeric,
    NULLIF(sqlc.arg(pile_location_source)::text, ''),
    CASE WHEN NULLIF(sqlc.arg(pile_latitude)::text, '') IS NULL THEN NULL ELSE now() END
) RETURNING id::text;

-- name: InsertWaitlistEntry :one
INSERT INTO waitlist_entries (job_id, region_snapshot)
VALUES (sqlc.arg(job_id)::uuid, NULLIF(sqlc.arg(region_snapshot)::text, ''))
RETURNING id::text;

-- name: InsertJobNote :one
INSERT INTO job_notes (job_id, author_user_id, body, correction_of_id)
VALUES (sqlc.arg(job_id)::uuid, sqlc.arg(author_user_id)::uuid, sqlc.arg(body), NULLIF(sqlc.arg(correction_of_id)::text, '')::uuid)
RETURNING id::text;

-- name: ListCustomers :many
SELECT c.id::text, c.first_name, c.last_name, COALESCE(c.company_name, '')::text AS company_name,
       c.street, c.postal_code, c.locality, c.region, c.country_code::text,
       COALESCE(c.address_freeform, '')::text AS address_freeform,
       COALESCE(c.phone_raw, '')::text AS phone_raw, COALESCE(c.email::text, '')::text AS email,
       c.notification_preference, c.version,
       count(j.id) FILTER (WHERE j.archived_at IS NULL AND j.workflow_status IN ('waitlist','planning','scheduled'))::int AS active_job_count,
       count(j.id) FILTER (WHERE j.archived_at IS NOT NULL OR j.workflow_status IN ('completed','cancelled'))::int AS historical_job_count,
       c.archived_at IS NOT NULL AS archived,
       GREATEST(c.updated_at, COALESCE(max(j.updated_at), c.updated_at)) AS last_used_at,
       c.updated_at, COALESCE(c.latitude::text, '')::text AS latitude,
       COALESCE(c.longitude::text, '')::text AS longitude
FROM customers c
LEFT JOIN jobs j ON j.customer_id = c.id
WHERE (sqlc.arg(include_archived)::boolean OR c.archived_at IS NULL)
  AND (
      sqlc.arg(search)::text = '' OR
      concat_ws(' ', c.first_name, c.last_name, c.company_name, c.locality) ILIKE '%' || sqlc.arg(search)::text || '%' OR
      (sqlc.arg(search_phone)::text <> '' AND c.phone_normalized = sqlc.arg(search_phone)::text) OR
      EXISTS (SELECT 1 FROM jobs sj WHERE sj.customer_id = c.id AND sj.job_number ILIKE '%' || sqlc.arg(search)::text || '%')
  )
GROUP BY c.id
ORDER BY
  CASE WHEN sqlc.arg(sort)::text='name' AND sqlc.arg(direction)::text='asc' THEN lower(c.last_name) END ASC,
  CASE WHEN sqlc.arg(sort)::text='name' AND sqlc.arg(direction)::text='desc' THEN lower(c.last_name) END DESC,
  CASE WHEN sqlc.arg(sort)::text='locality' AND sqlc.arg(direction)::text='asc' THEN lower(c.locality) END ASC,
  CASE WHEN sqlc.arg(sort)::text='locality' AND sqlc.arg(direction)::text='desc' THEN lower(c.locality) END DESC,
  CASE WHEN sqlc.arg(sort)::text='jobs' AND sqlc.arg(direction)::text='asc' THEN count(j.id) FILTER (WHERE j.archived_at IS NULL AND j.workflow_status IN ('waitlist','planning','scheduled')) END ASC,
  CASE WHEN sqlc.arg(sort)::text='jobs' AND sqlc.arg(direction)::text='desc' THEN count(j.id) FILTER (WHERE j.archived_at IS NULL AND j.workflow_status IN ('waitlist','planning','scheduled')) END DESC,
  CASE WHEN sqlc.arg(sort)::text='recent' AND sqlc.arg(direction)::text='asc' THEN GREATEST(c.updated_at, COALESCE(max(j.updated_at), c.updated_at)) END ASC,
  CASE WHEN sqlc.arg(sort)::text='recent' AND sqlc.arg(direction)::text='desc' THEN GREATEST(c.updated_at, COALESCE(max(j.updated_at), c.updated_at)) END DESC,
  lower(c.last_name), lower(c.first_name), c.id
LIMIT sqlc.arg(page_size) OFFSET sqlc.arg(page_offset);

-- name: CountCustomers :one
SELECT count(*) FROM customers c
WHERE (sqlc.arg(include_archived)::boolean OR c.archived_at IS NULL)
  AND (
      sqlc.arg(search)::text = '' OR
      concat_ws(' ', c.first_name, c.last_name, c.company_name, c.locality) ILIKE '%' || sqlc.arg(search)::text || '%' OR
      (sqlc.arg(search_phone)::text <> '' AND c.phone_normalized = sqlc.arg(search_phone)::text) OR
      EXISTS (SELECT 1 FROM jobs sj WHERE sj.customer_id = c.id AND sj.job_number ILIKE '%' || sqlc.arg(search)::text || '%')
  );

-- name: GetCustomer :one
SELECT id::text, first_name, last_name, COALESCE(company_name, '')::text AS company_name,
       street, postal_code, locality, region, country_code::text,
       COALESCE(address_freeform, '')::text AS address_freeform,
       COALESCE(phone_raw, '')::text AS phone_raw, COALESCE(phone_normalized, '')::text AS phone_normalized,
       COALESCE(email::text, '')::text AS email, notification_preference,
       COALESCE(latitude::text, '')::text AS latitude, COALESCE(longitude::text, '')::text AS longitude,
       geocoding_status, archived_at, version, created_at, updated_at
FROM customers WHERE id = sqlc.arg(id)::uuid;

-- name: GetActiveCustomer :one
SELECT id::text
FROM customers
WHERE id = sqlc.arg(id)::uuid AND archived_at IS NULL;

-- name: LockActiveCustomer :one
SELECT id::text FROM customers
WHERE id=sqlc.arg(id)::uuid AND archived_at IS NULL
FOR SHARE;

-- name: LockCustomerForArchive :one
SELECT version FROM customers
WHERE id=sqlc.arg(id)::uuid AND archived_at IS NULL
FOR UPDATE;

-- name: CustomerHasActiveWorkflow :one
SELECT EXISTS (
    SELECT 1 FROM jobs
    WHERE customer_id=sqlc.arg(customer_id)::uuid AND archived_at IS NULL
      AND workflow_status IN ('waitlist','planning','scheduled')
)::boolean;

-- name: ListCustomerJobs :many
SELECT id::text, job_number, job_type, volume_m3::text, estimated_hack_minutes,
       estimated_transport_minutes, transport_trip_count, transport_mode,
       external_transport_confirmed, COALESCE(to_char(preferred_start_date, 'YYYY-MM-DD'), '')::text AS preferred_start_date,
       COALESCE(to_char(preferred_end_date, 'YYYY-MM-DD'), '')::text AS preferred_end_date,
       preference_mode, COALESCE(preference_text, '')::text AS preference_text, urgency, COALESCE(region, '')::text AS region,
       source, workflow_status, received_at, archived_at, version,
       COALESCE(pile_latitude::text, '')::text AS pile_latitude,
       COALESCE(pile_longitude::text, '')::text AS pile_longitude,
       COALESCE(pile_location_source, '')::text AS pile_location_source,
       COALESCE((SELECT a.id::text FROM appointments a WHERE a.job_id=jobs.id AND a.lifecycle_status IN ('proposal','fixed') ORDER BY a.starts_at DESC, a.id DESC LIMIT 1), '')::text AS active_appointment_id
FROM jobs WHERE customer_id = sqlc.arg(customer_id)::uuid
ORDER BY received_at DESC, id DESC;

-- name: ListCustomerAppointments :many
SELECT a.id::text, j.job_number, a.lifecycle_status, a.confirmation_status, a.starts_at, a.ends_at
FROM appointments a
JOIN jobs j ON j.id=a.job_id
WHERE j.customer_id=sqlc.arg(customer_id)::uuid
ORDER BY a.starts_at DESC, a.id DESC;

-- name: GetJob :one
SELECT id::text, customer_id::text, job_number, job_type, volume_m3::text,
       estimated_hack_minutes, estimated_transport_minutes, transport_trip_count,
       transport_mode, external_transport_confirmed,
       COALESCE(to_char(preferred_start_date, 'YYYY-MM-DD'), '')::text AS preferred_start_date,
       COALESCE(to_char(preferred_end_date, 'YYYY-MM-DD'), '')::text AS preferred_end_date,
       preference_mode, COALESCE(preference_text, '')::text AS preference_text, urgency,
       COALESCE(region, '')::text AS region, source, workflow_status, received_at,
       archived_at, version,
       COALESCE(pile_latitude::text, '')::text AS pile_latitude,
       COALESCE(pile_longitude::text, '')::text AS pile_longitude,
       COALESCE(pile_location_source, '')::text AS pile_location_source
FROM jobs WHERE id = sqlc.arg(id)::uuid;

-- name: LockJobForArchive :one
SELECT version, workflow_status FROM jobs
WHERE id=sqlc.arg(id)::uuid AND archived_at IS NULL
FOR UPDATE;

-- name: LockJobForUpdate :one
SELECT version, workflow_status, job_type, volume_m3::text
FROM jobs
WHERE id=sqlc.arg(id)::uuid AND archived_at IS NULL
FOR UPDATE;

-- name: LockFixedAppointmentForJobUpdate :one
SELECT id::text, starts_at, ends_at
FROM appointments
WHERE job_id=sqlc.arg(job_id)::uuid AND lifecycle_status='fixed'
FOR UPDATE;

-- name: JobHasActiveAppointment :one
SELECT EXISTS (
    SELECT 1 FROM appointments
    WHERE job_id=sqlc.arg(job_id)::uuid AND lifecycle_status IN ('proposal','fixed')
)::boolean;

-- name: ListJobNotes :many
SELECT n.id::text, n.job_id::text, n.author_user_id::text, u.display_name AS author_name,
       n.body, COALESCE(n.correction_of_id::text, '')::text AS correction_of_id, n.created_at
FROM job_notes n JOIN users u ON u.id = n.author_user_id
WHERE n.job_id = sqlc.arg(job_id)::uuid
ORDER BY n.created_at DESC, n.id DESC;

-- name: InsertJobNoteIdempotent :one
INSERT INTO job_notes (job_id, author_user_id, body, correction_of_id, idempotency_key)
VALUES (sqlc.arg(job_id)::uuid, sqlc.arg(author_user_id)::uuid, sqlc.arg(body),
        NULLIF(sqlc.arg(correction_of_id)::text, '')::uuid, sqlc.arg(idempotency_key))
ON CONFLICT (job_id, author_user_id, idempotency_key) WHERE idempotency_key IS NOT NULL
DO UPDATE SET id=job_notes.id
RETURNING id::text, (xmax=0)::boolean AS inserted;

-- name: UpsertRecentCustomer :execrows
INSERT INTO recent_records (user_id, customer_id, viewed_at)
SELECT sqlc.arg(user_id)::uuid, c.id, now() FROM customers c WHERE c.id=sqlc.arg(customer_id)::uuid
ON CONFLICT (user_id, customer_id) WHERE customer_id IS NOT NULL
DO UPDATE SET viewed_at=excluded.viewed_at;

-- name: UpsertRecentJob :one
INSERT INTO recent_records (user_id, job_id, viewed_at)
SELECT sqlc.arg(user_id)::uuid, j.id, now() FROM jobs j WHERE j.id=sqlc.arg(job_id)::uuid
ON CONFLICT (user_id, job_id) WHERE job_id IS NOT NULL
DO UPDATE SET viewed_at=excluded.viewed_at
RETURNING (SELECT customer_id::text FROM jobs WHERE id=sqlc.arg(job_id)::uuid)::text AS customer_id;

-- name: TrimRecentRecords :exec
DELETE FROM recent_records WHERE ctid IN (
    SELECT ctid FROM recent_records
    WHERE user_id=sqlc.arg(user_id)::uuid
    ORDER BY viewed_at DESC OFFSET 20
);

-- name: ListRecentRecords :many
SELECT COALESCE(r.customer_id::text, '')::text AS customer_id,
       COALESCE(r.job_id::text, '')::text AS job_id,
       CASE WHEN r.job_id IS NULL THEN concat_ws(' ', c.company_name, c.first_name, c.last_name)
            ELSE j.job_number END::text AS label,
       CASE WHEN r.job_id IS NULL THEN c.locality
            ELSE concat_ws(' · ', concat_ws(' ', jc.company_name, jc.first_name, jc.last_name), jc.locality) END::text AS context,
       r.viewed_at
FROM recent_records r
LEFT JOIN customers c ON c.id=r.customer_id
LEFT JOIN jobs j ON j.id=r.job_id
LEFT JOIN customers jc ON jc.id=j.customer_id
WHERE r.user_id=sqlc.arg(user_id)::uuid
ORDER BY r.viewed_at DESC
LIMIT sqlc.arg(result_limit);

-- name: ListWaitlistFilterFavorites :many
SELECT id::text, name, job_type, region, urgency, preferred_month, workflow,
       missing_location, duration_issue, duration_group, overdue, unassigned,
       transport_pending, incomplete, sort_key, sort_direction
FROM waitlist_filter_favorites
WHERE user_id=sqlc.arg(user_id)::uuid
ORDER BY updated_at DESC, id;

-- name: CountWaitlistFilterFavorites :one
SELECT count(*)::int FROM waitlist_filter_favorites WHERE user_id=sqlc.arg(user_id)::uuid;

-- name: WaitlistFilterFavoriteExists :one
SELECT EXISTS (
    SELECT 1 FROM waitlist_filter_favorites
    WHERE user_id=sqlc.arg(user_id)::uuid AND lower(name)=lower(sqlc.arg(name)::text)
)::boolean;

-- name: UpsertWaitlistFilterFavorite :exec
INSERT INTO waitlist_filter_favorites
    (id, user_id, name, job_type, region, urgency, preferred_month, workflow,
     missing_location, duration_issue, duration_group, overdue, unassigned,
     transport_pending, incomplete, sort_key, sort_direction)
VALUES (gen_random_uuid(), sqlc.arg(user_id)::uuid, sqlc.arg(name), sqlc.arg(job_type),
        sqlc.arg(region), sqlc.arg(urgency), sqlc.arg(preferred_month), sqlc.arg(workflow),
        sqlc.arg(missing_location), sqlc.arg(duration_issue), sqlc.arg(duration_group),
        sqlc.arg(overdue), sqlc.arg(unassigned), sqlc.arg(transport_pending), sqlc.arg(incomplete),
        sqlc.arg(sort_key), sqlc.arg(sort_direction))
ON CONFLICT (user_id, lower(name)) DO UPDATE SET
    job_type=excluded.job_type, region=excluded.region, urgency=excluded.urgency,
    preferred_month=excluded.preferred_month, workflow=excluded.workflow,
    missing_location=excluded.missing_location, duration_issue=excluded.duration_issue,
    duration_group=excluded.duration_group, overdue=excluded.overdue,
    unassigned=excluded.unassigned, transport_pending=excluded.transport_pending, incomplete=excluded.incomplete,
    sort_key=excluded.sort_key, sort_direction=excluded.sort_direction, updated_at=now();

-- name: DeleteWaitlistFilterFavorite :execrows
DELETE FROM waitlist_filter_favorites
WHERE id=sqlc.arg(id)::uuid AND user_id=sqlc.arg(user_id)::uuid;

-- name: FindDuplicateCustomers :many
SELECT id::text, first_name, last_name, COALESCE(company_name, '')::text AS company_name, locality
FROM customers
WHERE archived_at IS NULL AND (
    (sqlc.arg(phone_normalized)::text <> '' AND phone_normalized = sqlc.arg(phone_normalized)::text) OR
    (sqlc.arg(email)::text <> '' AND email = sqlc.arg(email)::text::citext) OR
    (
      lower(locality) = lower(sqlc.arg(locality)::text)
      AND (
        (lower(first_name) = lower(sqlc.arg(first_name)::text) AND lower(last_name) = lower(sqlc.arg(last_name)::text))
        OR (
          length(sqlc.arg(last_name)::text) >= 4
          AND left(lower(last_name), 4) = left(lower(sqlc.arg(last_name)::text), 4)
          AND left(lower(first_name), 1) = left(lower(sqlc.arg(first_name)::text), 1)
        )
      )
    )
)
ORDER BY updated_at DESC LIMIT 10;

-- name: UpdateCustomer :execrows
UPDATE customers SET
    first_name = sqlc.arg(first_name), last_name = sqlc.arg(last_name),
    company_name = NULLIF(sqlc.arg(company_name)::text, ''), street = sqlc.arg(street),
    postal_code = sqlc.arg(postal_code), locality = sqlc.arg(locality), region = sqlc.arg(region),
    address_freeform = NULLIF(sqlc.arg(address_freeform)::text, ''),
    phone_raw = NULLIF(sqlc.arg(phone_raw)::text, ''), phone_normalized = NULLIF(sqlc.arg(phone_normalized)::text, ''),
    email = NULLIF(sqlc.arg(email)::text, '')::citext, notification_preference = sqlc.arg(notification_preference),
    version = version + 1, updated_at = now()
WHERE id = sqlc.arg(id)::uuid AND version = sqlc.arg(expected_version) AND archived_at IS NULL;

-- name: UpdateJob :execrows
UPDATE jobs SET
    job_type = sqlc.arg(job_type), volume_m3 = sqlc.arg(volume_m3)::numeric,
    estimated_hack_minutes = sqlc.arg(estimated_hack_minutes),
    estimated_transport_minutes = sqlc.arg(estimated_transport_minutes),
    transport_trip_count = sqlc.arg(transport_trip_count), transport_mode = sqlc.arg(transport_mode),
    external_transport_confirmed = sqlc.arg(external_transport_confirmed),
    preferred_start_date = NULLIF(sqlc.arg(preferred_start_date)::text, '')::date,
    preferred_end_date = NULLIF(sqlc.arg(preferred_end_date)::text, '')::date,
    preference_mode = sqlc.arg(preference_mode),
    preference_text = NULLIF(sqlc.arg(preference_text)::text, ''), urgency = sqlc.arg(urgency),
    region = NULLIF(sqlc.arg(region)::text, ''), source = sqlc.arg(source),
    pile_latitude = NULLIF(sqlc.arg(pile_latitude)::text, '')::numeric,
    pile_longitude = NULLIF(sqlc.arg(pile_longitude)::text, '')::numeric,
    pile_location_source = NULLIF(sqlc.arg(pile_location_source)::text, ''),
    pile_location_updated_at = CASE WHEN NULLIF(sqlc.arg(pile_latitude)::text, '') IS NULL THEN NULL ELSE now() END,
    version = version + 1, updated_at = now()
WHERE id = sqlc.arg(id)::uuid AND version = sqlc.arg(expected_version)
  AND archived_at IS NULL AND workflow_status IN ('waitlist', 'planning', 'scheduled');

-- name: ArchiveJob :execrows
UPDATE jobs SET archived_at = now(), workflow_status = 'cancelled', version = version + 1, updated_at = now()
WHERE id = sqlc.arg(id)::uuid AND version = sqlc.arg(expected_version)
  AND archived_at IS NULL AND workflow_status IN ('waitlist', 'planning');

-- name: RemoveActiveWaitlistForJob :execrows
UPDATE waitlist_entries SET removed_at = now(), removed_reason = 'cancelled', version = version + 1
WHERE job_id = sqlc.arg(job_id)::uuid AND removed_at IS NULL;

-- name: ArchiveCustomer :execrows
UPDATE customers SET archived_at = now(), version = version + 1, updated_at = now()
WHERE id = sqlc.arg(id)::uuid AND version = sqlc.arg(expected_version) AND archived_at IS NULL;

-- name: ListWaitlist :many
SELECT w.id::text AS waitlist_id, w.job_id::text, w.entered_at, w.manual_priority,
       w.priority_reason, w.version AS waitlist_version,
       j.job_number, j.job_type, j.volume_m3::text, j.estimated_hack_minutes, j.estimated_transport_minutes,
       (j.estimated_hack_minutes+j.estimated_transport_minutes)::integer AS total_minutes,
       j.transport_mode, j.external_transport_confirmed,
       COALESCE(to_char(j.preferred_start_date, 'YYYY-MM-DD'), '')::text AS preferred_start_date,
       COALESCE(to_char(j.preferred_end_date, 'YYYY-MM-DD'), '')::text AS preferred_end_date,
       j.preference_mode, COALESCE(j.preference_text, '')::text AS preference_text, j.urgency,
       COALESCE(w.region_snapshot, '')::text AS region,
       c.id::text AS customer_id, c.first_name, c.last_name, COALESCE(c.company_name, '')::text AS company_name, c.locality,
       COALESCE((SELECT n.body FROM job_notes n WHERE n.job_id = j.id ORDER BY n.created_at DESC, n.id DESC LIMIT 1), '')::text AS note_excerpt,
       GREATEST(0, floor(EXTRACT(EPOCH FROM (now() - w.entered_at)) / 86400))::integer AS age_days,
       CASE WHEN EXISTS (SELECT 1 FROM appointments a WHERE a.job_id=j.id AND a.lifecycle_status='fixed') THEN 'scheduled'
            WHEN EXISTS (SELECT 1 FROM appointments a WHERE a.job_id=j.id AND a.lifecycle_status='proposal') THEN 'proposal'
            ELSE 'unplanned' END::text AS workflow_status,
       j.updated_at, j.pile_latitude IS NOT NULL AND j.pile_longitude IS NOT NULL AS has_pile_location,
       (COALESCE(j.pile_location_source::text, '') <> '')::boolean AS has_pile_source,
       (j.preferred_end_date IS NOT NULL AND j.preferred_end_date < (now() AT TIME ZONE 'Europe/Vienna')::date)::boolean AS overdue,
       EXISTS (SELECT 1 FROM appointments a WHERE a.job_id=j.id AND a.lifecycle_status IN ('proposal','fixed'))::boolean AS has_active_appointment,
       EXISTS (SELECT 1 FROM appointments a JOIN appointment_drivers ad ON ad.appointment_id=a.id WHERE a.job_id=j.id AND a.lifecycle_status IN ('proposal','fixed'))::boolean AS has_internal_assignment,
       CASE c.notification_preference
         WHEN 'email' THEN c.email IS NOT NULL
         WHEN 'sms' THEN c.phone_normalized IS NOT NULL
         WHEN 'both' THEN c.email IS NOT NULL OR c.phone_normalized IS NOT NULL
         ELSE false
       END::boolean AS has_contact
FROM waitlist_entries w
JOIN jobs j ON j.id = w.job_id
JOIN customers c ON c.id = j.customer_id
WHERE w.removed_at IS NULL AND j.archived_at IS NULL AND c.archived_at IS NULL
  AND (sqlc.arg(search)::text = '' OR concat_ws(' ', c.first_name, c.last_name, c.company_name, c.locality, j.job_number) ILIKE '%' || sqlc.arg(search)::text || '%')
  AND (sqlc.arg(job_type_filter)::text = '' OR j.job_type = sqlc.arg(job_type_filter)::text)
  AND (sqlc.arg(region_filter)::text = '' OR w.region_snapshot = sqlc.arg(region_filter)::text)
  AND (sqlc.arg(urgency_filter)::text = '' OR j.urgency = sqlc.arg(urgency_filter)::text)
  AND (sqlc.arg(month_filter)::text = '' OR to_char(j.preferred_start_date, 'YYYY-MM') = sqlc.arg(month_filter)::text)
  AND (sqlc.arg(workflow_filter)::text='' OR sqlc.arg(workflow_filter)::text=CASE
       WHEN EXISTS (SELECT 1 FROM appointments a WHERE a.job_id=j.id AND a.lifecycle_status='fixed') THEN 'scheduled'
       WHEN EXISTS (SELECT 1 FROM appointments a WHERE a.job_id=j.id AND a.lifecycle_status='proposal') THEN 'proposal'
       ELSE 'unplanned' END)
  AND (NOT sqlc.arg(missing_location)::boolean OR j.pile_latitude IS NULL OR j.pile_longitude IS NULL)
  AND (NOT sqlc.arg(duration_issue)::boolean OR j.estimated_hack_minutes+j.estimated_transport_minutes<sqlc.arg(duration_review_min)::integer OR j.estimated_hack_minutes+j.estimated_transport_minutes>sqlc.arg(duration_review_max)::integer)
  AND (NOT sqlc.arg(overdue)::boolean OR (j.preferred_end_date IS NOT NULL AND j.preferred_end_date < (now() AT TIME ZONE 'Europe/Vienna')::date))
  AND (NOT sqlc.arg(unassigned)::boolean OR NOT EXISTS (SELECT 1 FROM appointments a JOIN appointment_drivers ad ON ad.appointment_id=a.id WHERE a.job_id=j.id AND a.lifecycle_status IN ('proposal','fixed')))
  AND (NOT sqlc.arg(transport_pending)::boolean OR (j.job_type='chipping_with_transport' AND (j.transport_mode='undecided' OR (j.transport_mode='external' AND NOT j.external_transport_confirmed))))
  AND (NOT sqlc.arg(incomplete)::boolean OR
       j.pile_latitude IS NULL OR j.pile_longitude IS NULL OR COALESCE(j.pile_location_source, '') = '' OR
       COALESCE(w.region_snapshot, '') = '' OR
       j.estimated_hack_minutes+j.estimated_transport_minutes<sqlc.arg(duration_review_min)::integer OR
       j.estimated_hack_minutes+j.estimated_transport_minutes>sqlc.arg(duration_review_max)::integer OR
       (j.preference_mode='window' AND (j.preferred_start_date IS NULL OR j.preferred_end_date IS NULL)) OR
       (j.job_type='chipping_with_transport' AND (j.transport_mode='undecided' OR (j.transport_mode='external' AND NOT j.external_transport_confirmed))) OR
       NOT CASE c.notification_preference
         WHEN 'email' THEN c.email IS NOT NULL
         WHEN 'sms' THEN c.phone_normalized IS NOT NULL
         WHEN 'both' THEN c.email IS NOT NULL OR c.phone_normalized IS NOT NULL
         ELSE false
       END)
  AND (sqlc.arg(duration_group)::text='' OR sqlc.arg(duration_group)::text=CASE WHEN j.estimated_hack_minutes+j.estimated_transport_minutes<=120 THEN 'short' WHEN j.estimated_hack_minutes+j.estimated_transport_minutes<=360 THEN 'medium' ELSE 'long' END)
ORDER BY
  CASE WHEN sqlc.arg(sort)::text = 'entered' AND sqlc.arg(direction)::text = 'asc' THEN w.entered_at END ASC,
  CASE WHEN sqlc.arg(sort)::text = 'entered' AND sqlc.arg(direction)::text = 'desc' THEN w.entered_at END DESC,
  CASE WHEN sqlc.arg(sort)::text = 'preferred' AND sqlc.arg(direction)::text = 'asc' THEN j.preferred_start_date END ASC NULLS LAST,
  CASE WHEN sqlc.arg(sort)::text = 'preferred' AND sqlc.arg(direction)::text = 'desc' THEN j.preferred_start_date END DESC NULLS LAST,
  CASE WHEN sqlc.arg(sort)::text = 'urgency' AND sqlc.arg(direction)::text = 'asc' THEN array_position(ARRAY['low','normal','high','urgent'], j.urgency) END ASC,
  CASE WHEN sqlc.arg(sort)::text = 'urgency' AND sqlc.arg(direction)::text = 'desc' THEN array_position(ARRAY['low','normal','high','urgent'], j.urgency) END DESC,
  CASE WHEN sqlc.arg(sort)::text = 'volume' AND sqlc.arg(direction)::text = 'asc' THEN j.volume_m3 END ASC,
  CASE WHEN sqlc.arg(sort)::text = 'volume' AND sqlc.arg(direction)::text = 'desc' THEN j.volume_m3 END DESC,
  CASE WHEN sqlc.arg(sort)::text = 'region' AND sqlc.arg(direction)::text = 'asc' THEN lower(w.region_snapshot) END ASC,
  CASE WHEN sqlc.arg(sort)::text = 'region' AND sqlc.arg(direction)::text = 'desc' THEN lower(w.region_snapshot) END DESC,
  CASE WHEN sqlc.arg(sort)::text = 'customer' AND sqlc.arg(direction)::text = 'asc' THEN lower(concat_ws(' ', c.company_name, c.last_name, c.first_name)) END ASC,
  CASE WHEN sqlc.arg(sort)::text = 'customer' AND sqlc.arg(direction)::text = 'desc' THEN lower(concat_ws(' ', c.company_name, c.last_name, c.first_name)) END DESC,
  CASE WHEN sqlc.arg(sort)::text = 'workflow' AND sqlc.arg(direction)::text = 'asc' THEN CASE
       WHEN EXISTS (SELECT 1 FROM appointments a WHERE a.job_id=j.id AND a.lifecycle_status='fixed') THEN 'scheduled'
       WHEN EXISTS (SELECT 1 FROM appointments a WHERE a.job_id=j.id AND a.lifecycle_status='proposal') THEN 'proposal'
       ELSE 'unplanned' END END ASC,
  CASE WHEN sqlc.arg(sort)::text = 'workflow' AND sqlc.arg(direction)::text = 'desc' THEN CASE
       WHEN EXISTS (SELECT 1 FROM appointments a WHERE a.job_id=j.id AND a.lifecycle_status='fixed') THEN 'scheduled'
       WHEN EXISTS (SELECT 1 FROM appointments a WHERE a.job_id=j.id AND a.lifecycle_status='proposal') THEN 'proposal'
       ELSE 'unplanned' END END DESC,
  CASE WHEN sqlc.arg(sort)::text = 'updated' AND sqlc.arg(direction)::text = 'asc' THEN j.updated_at END ASC,
  CASE WHEN sqlc.arg(sort)::text = 'updated' AND sqlc.arg(direction)::text = 'desc' THEN j.updated_at END DESC,
  CASE WHEN sqlc.arg(sort)::text = 'duration' AND sqlc.arg(direction)::text = 'asc' THEN j.estimated_hack_minutes+j.estimated_transport_minutes END ASC,
  CASE WHEN sqlc.arg(sort)::text = 'duration' AND sqlc.arg(direction)::text = 'desc' THEN j.estimated_hack_minutes+j.estimated_transport_minutes END DESC,
  w.manual_priority DESC, w.entered_at, w.id
LIMIT sqlc.arg(page_size) OFFSET sqlc.arg(page_offset);

-- name: CountWaitlist :one
SELECT count(*) FROM waitlist_entries w
JOIN jobs j ON j.id = w.job_id JOIN customers c ON c.id = j.customer_id
WHERE w.removed_at IS NULL AND j.archived_at IS NULL AND c.archived_at IS NULL
  AND (sqlc.arg(search)::text = '' OR concat_ws(' ', c.first_name, c.last_name, c.company_name, c.locality, j.job_number) ILIKE '%' || sqlc.arg(search)::text || '%')
  AND (sqlc.arg(job_type_filter)::text = '' OR j.job_type = sqlc.arg(job_type_filter)::text)
  AND (sqlc.arg(region_filter)::text = '' OR w.region_snapshot = sqlc.arg(region_filter)::text)
  AND (sqlc.arg(urgency_filter)::text = '' OR j.urgency = sqlc.arg(urgency_filter)::text)
  AND (sqlc.arg(month_filter)::text = '' OR to_char(j.preferred_start_date, 'YYYY-MM') = sqlc.arg(month_filter)::text)
  AND (sqlc.arg(workflow_filter)::text='' OR sqlc.arg(workflow_filter)::text=CASE
       WHEN EXISTS (SELECT 1 FROM appointments a WHERE a.job_id=j.id AND a.lifecycle_status='fixed') THEN 'scheduled'
       WHEN EXISTS (SELECT 1 FROM appointments a WHERE a.job_id=j.id AND a.lifecycle_status='proposal') THEN 'proposal'
       ELSE 'unplanned' END)
  AND (NOT sqlc.arg(missing_location)::boolean OR j.pile_latitude IS NULL OR j.pile_longitude IS NULL)
  AND (NOT sqlc.arg(duration_issue)::boolean OR j.estimated_hack_minutes+j.estimated_transport_minutes<sqlc.arg(duration_review_min)::integer OR j.estimated_hack_minutes+j.estimated_transport_minutes>sqlc.arg(duration_review_max)::integer)
  AND (NOT sqlc.arg(overdue)::boolean OR (j.preferred_end_date IS NOT NULL AND j.preferred_end_date < (now() AT TIME ZONE 'Europe/Vienna')::date))
  AND (NOT sqlc.arg(unassigned)::boolean OR NOT EXISTS (SELECT 1 FROM appointments a JOIN appointment_drivers ad ON ad.appointment_id=a.id WHERE a.job_id=j.id AND a.lifecycle_status IN ('proposal','fixed')))
  AND (NOT sqlc.arg(transport_pending)::boolean OR (j.job_type='chipping_with_transport' AND (j.transport_mode='undecided' OR (j.transport_mode='external' AND NOT j.external_transport_confirmed))))
  AND (NOT sqlc.arg(incomplete)::boolean OR
       j.pile_latitude IS NULL OR j.pile_longitude IS NULL OR COALESCE(j.pile_location_source, '') = '' OR
       COALESCE(w.region_snapshot, '') = '' OR
       j.estimated_hack_minutes+j.estimated_transport_minutes<sqlc.arg(duration_review_min)::integer OR
       j.estimated_hack_minutes+j.estimated_transport_minutes>sqlc.arg(duration_review_max)::integer OR
       (j.preference_mode='window' AND (j.preferred_start_date IS NULL OR j.preferred_end_date IS NULL)) OR
       (j.job_type='chipping_with_transport' AND (j.transport_mode='undecided' OR (j.transport_mode='external' AND NOT j.external_transport_confirmed))) OR
       NOT CASE c.notification_preference
         WHEN 'email' THEN c.email IS NOT NULL
         WHEN 'sms' THEN c.phone_normalized IS NOT NULL
         WHEN 'both' THEN c.email IS NOT NULL OR c.phone_normalized IS NOT NULL
         ELSE false
       END)
  AND (sqlc.arg(duration_group)::text='' OR sqlc.arg(duration_group)::text=CASE WHEN j.estimated_hack_minutes+j.estimated_transport_minutes<=120 THEN 'short' WHEN j.estimated_hack_minutes+j.estimated_transport_minutes<=360 THEN 'medium' ELSE 'long' END);

-- name: UpdateWaitlistPriority :execrows
UPDATE waitlist_entries SET manual_priority = sqlc.arg(priority), priority_reason = sqlc.arg(reason), version = version + 1
WHERE id = sqlc.arg(id)::uuid AND version = sqlc.arg(expected_version) AND removed_at IS NULL;

-- name: CountActiveWaitlist :one
SELECT count(*) FROM waitlist_entries w
JOIN jobs j ON j.id=w.job_id
JOIN customers c ON c.id=j.customer_id
WHERE w.removed_at IS NULL AND j.archived_at IS NULL AND c.archived_at IS NULL;

-- name: SearchWorkspace :many
WITH matches AS (
    SELECT 'customer'::text AS kind, c.id::text AS id, ''::text AS parent_id,
           concat_ws(' ', NULLIF(c.first_name,''), NULLIF(c.last_name,''), NULLIF(c.company_name,''))::text AS title,
           concat_ws(' · ', NULLIF(c.locality,''), NULLIF(c.region,''))::text AS subtitle,
           c.updated_at AS ranked_at
    FROM customers c
    WHERE c.archived_at IS NULL
      AND concat_ws(' ', c.first_name, c.last_name, c.company_name, c.locality, c.region) ILIKE '%' || sqlc.arg(search)::text || '%'
    ORDER BY c.updated_at DESC, c.id
    LIMIT 8
), job_matches AS (
    SELECT 'job'::text AS kind, j.id::text AS id, c.id::text AS parent_id,
           j.job_number::text AS title,
           concat_ws(' · ', concat_ws(' ', NULLIF(c.first_name,''), NULLIF(c.last_name,''), NULLIF(c.company_name,'')), NULLIF(c.locality,''))::text AS subtitle,
           j.updated_at AS ranked_at
    FROM jobs j JOIN customers c ON c.id=j.customer_id
    WHERE j.archived_at IS NULL AND c.archived_at IS NULL
      AND concat_ws(' ', j.job_number, c.first_name, c.last_name, c.company_name, c.locality) ILIKE '%' || sqlc.arg(search)::text || '%'
    ORDER BY j.updated_at DESC, j.id
    LIMIT 8
), appointment_matches AS (
    SELECT 'appointment'::text AS kind, a.id::text AS id, c.id::text AS parent_id,
           concat_ws(' · ', j.job_number, concat_ws(' ', NULLIF(c.first_name,''), NULLIF(c.last_name,''), NULLIF(c.company_name,'')))::text AS title,
           to_char(a.starts_at AT TIME ZONE 'Europe/Vienna', 'DD.MM.YYYY HH24:MI')::text AS subtitle,
           a.updated_at AS ranked_at
    FROM appointments a JOIN jobs j ON j.id=a.job_id JOIN customers c ON c.id=j.customer_id
    WHERE a.lifecycle_status IN ('draft','proposal','fixed') AND j.archived_at IS NULL AND c.archived_at IS NULL
      AND concat_ws(' ', j.job_number, c.first_name, c.last_name, c.company_name, c.locality) ILIKE '%' || sqlc.arg(search)::text || '%'
    ORDER BY a.updated_at DESC, a.id
    LIMIT 8
)
SELECT kind, id, parent_id, title, subtitle
FROM (SELECT * FROM matches UNION ALL SELECT * FROM job_matches UNION ALL SELECT * FROM appointment_matches) all_matches
ORDER BY ranked_at DESC, kind, id
LIMIT 24;

-- name: RemoveWaitlistEntry :execrows
UPDATE waitlist_entries SET removed_at = now(), removed_reason = sqlc.arg(reason), version = version + 1
WHERE id = sqlc.arg(id)::uuid AND version = sqlc.arg(expected_version) AND removed_at IS NULL;
