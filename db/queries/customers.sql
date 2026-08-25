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
    preferred_start_date, preferred_end_date, preference_text, urgency, region, source
) VALUES (
    sqlc.arg(job_number), sqlc.arg(customer_id)::uuid, sqlc.arg(job_type), sqlc.arg(volume_m3)::numeric,
    sqlc.arg(estimated_hack_minutes), sqlc.arg(estimated_transport_minutes), sqlc.arg(transport_trip_count),
    sqlc.arg(transport_mode), sqlc.arg(external_transport_confirmed),
    NULLIF(sqlc.arg(preferred_start_date)::text, '')::date, NULLIF(sqlc.arg(preferred_end_date)::text, '')::date,
    NULLIF(sqlc.arg(preference_text)::text, ''), sqlc.arg(urgency), NULLIF(sqlc.arg(region)::text, ''), sqlc.arg(source)
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
       c.locality, c.region, COALESCE(c.phone_raw, '')::text AS phone_raw,
       COALESCE(c.email::text, '')::text AS email, c.version,
       count(j.id)::int AS job_count
FROM customers c
LEFT JOIN jobs j ON j.customer_id = c.id AND j.archived_at IS NULL
WHERE c.archived_at IS NULL
  AND (
      sqlc.arg(search)::text = '' OR
      concat_ws(' ', c.first_name, c.last_name, c.company_name, c.locality) ILIKE '%' || sqlc.arg(search)::text || '%' OR
      (sqlc.arg(search_phone)::text <> '' AND c.phone_normalized = sqlc.arg(search_phone)::text) OR
      EXISTS (SELECT 1 FROM jobs sj WHERE sj.customer_id = c.id AND sj.job_number ILIKE '%' || sqlc.arg(search)::text || '%')
  )
GROUP BY c.id
ORDER BY lower(c.last_name), lower(c.first_name), c.id
LIMIT sqlc.arg(page_size) OFFSET sqlc.arg(page_offset);

-- name: CountCustomers :one
SELECT count(*) FROM customers c
WHERE c.archived_at IS NULL
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

-- name: ListCustomerJobs :many
SELECT id::text, job_number, job_type, volume_m3::text, estimated_hack_minutes,
       estimated_transport_minutes, transport_trip_count, transport_mode,
       external_transport_confirmed, COALESCE(to_char(preferred_start_date, 'YYYY-MM-DD'), '')::text AS preferred_start_date,
       COALESCE(to_char(preferred_end_date, 'YYYY-MM-DD'), '')::text AS preferred_end_date,
       COALESCE(preference_text, '')::text AS preference_text, urgency, COALESCE(region, '')::text AS region,
       source, workflow_status, received_at, archived_at, version
FROM jobs WHERE customer_id = sqlc.arg(customer_id)::uuid
ORDER BY received_at DESC, id DESC;

-- name: GetJob :one
SELECT id::text, customer_id::text, job_number, job_type, volume_m3::text,
       estimated_hack_minutes, estimated_transport_minutes, transport_trip_count,
       transport_mode, external_transport_confirmed,
       COALESCE(to_char(preferred_start_date, 'YYYY-MM-DD'), '')::text AS preferred_start_date,
       COALESCE(to_char(preferred_end_date, 'YYYY-MM-DD'), '')::text AS preferred_end_date,
       COALESCE(preference_text, '')::text AS preference_text, urgency,
       COALESCE(region, '')::text AS region, source, workflow_status, received_at,
       archived_at, version
FROM jobs WHERE id = sqlc.arg(id)::uuid;

-- name: ListJobNotes :many
SELECT n.id::text, n.job_id::text, n.author_user_id::text, u.display_name AS author_name,
       n.body, COALESCE(n.correction_of_id::text, '')::text AS correction_of_id, n.created_at
FROM job_notes n JOIN users u ON u.id = n.author_user_id
WHERE n.job_id = sqlc.arg(job_id)::uuid
ORDER BY n.created_at, n.id;

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
    preference_text = NULLIF(sqlc.arg(preference_text)::text, ''), urgency = sqlc.arg(urgency),
    region = NULLIF(sqlc.arg(region)::text, ''), source = sqlc.arg(source),
    version = version + 1, updated_at = now()
WHERE id = sqlc.arg(id)::uuid AND version = sqlc.arg(expected_version)
  AND archived_at IS NULL AND workflow_status IN ('waitlist', 'planning');

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
SELECT w.id::text AS waitlist_id, w.job_id::text, w.entered_at, w.manual_priority, w.version AS waitlist_version,
       j.job_number, j.job_type, j.volume_m3::text, j.estimated_hack_minutes, j.transport_mode,
       COALESCE(to_char(j.preferred_start_date, 'YYYY-MM-DD'), '')::text AS preferred_start_date,
       COALESCE(to_char(j.preferred_end_date, 'YYYY-MM-DD'), '')::text AS preferred_end_date,
       COALESCE(j.preference_text, '')::text AS preference_text, j.urgency,
       COALESCE(w.region_snapshot, '')::text AS region,
       c.id::text AS customer_id, c.first_name, c.last_name, COALESCE(c.company_name, '')::text AS company_name, c.locality,
       COALESCE((SELECT n.body FROM job_notes n WHERE n.job_id = j.id ORDER BY n.created_at DESC LIMIT 1), '')::text AS note_excerpt,
       GREATEST(0, floor(EXTRACT(EPOCH FROM (now() - w.entered_at)) / 86400))::integer AS age_days
FROM waitlist_entries w
JOIN jobs j ON j.id = w.job_id
JOIN customers c ON c.id = j.customer_id
WHERE w.removed_at IS NULL AND j.archived_at IS NULL AND c.archived_at IS NULL
  AND (sqlc.arg(search)::text = '' OR concat_ws(' ', c.first_name, c.last_name, c.company_name, c.locality, j.job_number) ILIKE '%' || sqlc.arg(search)::text || '%')
  AND (sqlc.arg(job_type_filter)::text = '' OR j.job_type = sqlc.arg(job_type_filter)::text)
  AND (sqlc.arg(region_filter)::text = '' OR w.region_snapshot = sqlc.arg(region_filter)::text)
  AND (sqlc.arg(urgency_filter)::text = '' OR j.urgency = sqlc.arg(urgency_filter)::text)
  AND (sqlc.arg(month_filter)::text = '' OR to_char(j.preferred_start_date, 'YYYY-MM') = sqlc.arg(month_filter)::text)
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
  AND (sqlc.arg(month_filter)::text = '' OR to_char(j.preferred_start_date, 'YYYY-MM') = sqlc.arg(month_filter)::text);

-- name: UpdateWaitlistPriority :execrows
UPDATE waitlist_entries SET manual_priority = sqlc.arg(priority), version = version + 1
WHERE id = sqlc.arg(id)::uuid AND version = sqlc.arg(expected_version) AND removed_at IS NULL;

-- name: RemoveWaitlistEntry :execrows
UPDATE waitlist_entries SET removed_at = now(), removed_reason = sqlc.arg(reason), version = version + 1
WHERE id = sqlc.arg(id)::uuid AND version = sqlc.arg(expected_version) AND removed_at IS NULL;
