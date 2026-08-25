-- name: ListDriverProfiles :many
SELECT d.id::text, COALESCE(d.user_id::text, '')::text AS user_id, COALESCE(u.username::text, '')::text AS username,
       d.display_name, COALESCE(d.phone, '')::text AS phone, COALESCE(d.email::text, '')::text AS email,
       d.active, d.can_complete_jobs, COALESCE(d.internal_note, '')::text AS internal_note,
       d.version, d.created_at, d.updated_at
FROM drivers d
LEFT JOIN users u ON u.id = d.user_id
ORDER BY d.active DESC, lower(d.display_name), d.id;

-- name: GetDriverProfile :one
SELECT d.id::text, COALESCE(d.user_id::text, '')::text AS user_id, COALESCE(u.username::text, '')::text AS username,
       d.display_name, COALESCE(d.phone, '')::text AS phone, COALESCE(d.email::text, '')::text AS email,
       d.active, d.can_complete_jobs, COALESCE(d.internal_note, '')::text AS internal_note,
       d.version, d.created_at, d.updated_at
FROM drivers d
LEFT JOIN users u ON u.id = d.user_id
WHERE d.id = sqlc.arg(id)::uuid;

-- name: InsertDriverProfile :one
INSERT INTO drivers (user_id, display_name, phone, email, can_complete_jobs, internal_note)
SELECT NULLIF(sqlc.arg(user_id)::text, '')::uuid, sqlc.arg(display_name), NULLIF(sqlc.arg(phone)::text, ''),
       NULLIF(sqlc.arg(email)::text, '')::citext, sqlc.arg(can_complete_jobs), NULLIF(sqlc.arg(internal_note)::text, '')
WHERE sqlc.arg(user_id)::text = '' OR EXISTS (
    SELECT 1 FROM users WHERE id = sqlc.arg(user_id)::uuid AND role = 'driver' AND active
)
RETURNING id::text;

-- name: UpdateDriverProfile :execrows
UPDATE drivers SET
    user_id = NULLIF(sqlc.arg(user_id)::text, '')::uuid,
    display_name = sqlc.arg(display_name), phone = NULLIF(sqlc.arg(phone)::text, ''),
    email = NULLIF(sqlc.arg(email)::text, '')::citext, can_complete_jobs = sqlc.arg(can_complete_jobs),
    internal_note = NULLIF(sqlc.arg(internal_note)::text, ''), version = drivers.version + 1, updated_at = now()
WHERE drivers.id = sqlc.arg(id)::uuid AND drivers.version = sqlc.arg(expected_version) AND drivers.active
  AND (sqlc.arg(user_id)::text = '' OR EXISTS (
      SELECT 1 FROM users WHERE id = sqlc.arg(user_id)::uuid AND role = 'driver' AND active
  ));

-- name: DeactivateDriverProfile :execrows
UPDATE drivers SET active = false, version = version + 1, updated_at = now()
WHERE id = sqlc.arg(id)::uuid AND version = sqlc.arg(expected_version) AND active;

-- name: ListAvailabilityRulesForDriver :many
SELECT id::text, driver_id::text, iso_weekday, to_char(local_start, 'HH24:MI')::text AS local_start,
       to_char(local_end, 'HH24:MI')::text AS local_end, valid_from::text,
       COALESCE(valid_until::text, '')::text AS valid_until, status,
       COALESCE(internal_note, '')::text AS internal_note, version
FROM availability_rules
WHERE driver_id = sqlc.arg(driver_id)::uuid
ORDER BY iso_weekday, local_start, valid_from, id;

-- name: ListAvailabilityRulesInRange :many
SELECT id::text, driver_id::text, iso_weekday, to_char(local_start, 'HH24:MI')::text AS local_start,
       to_char(local_end, 'HH24:MI')::text AS local_end, valid_from::text,
       COALESCE(valid_until::text, '')::text AS valid_until, status,
       COALESCE(internal_note, '')::text AS internal_note, version
FROM availability_rules
WHERE driver_id = sqlc.arg(driver_id)::uuid
  AND valid_from <= sqlc.arg(local_to)::date
  AND (valid_until IS NULL OR valid_until >= sqlc.arg(local_from)::date)
ORDER BY iso_weekday, local_start, valid_from, id;

-- name: InsertAvailabilityRule :one
INSERT INTO availability_rules (
    driver_id, iso_weekday, local_start, local_end, valid_from, valid_until, status, internal_note
) VALUES (
    sqlc.arg(driver_id)::uuid, sqlc.arg(iso_weekday), sqlc.arg(local_start)::time,
    sqlc.arg(local_end)::time, sqlc.arg(valid_from)::date, NULLIF(sqlc.arg(valid_until)::text, '')::date,
    sqlc.arg(status), NULLIF(sqlc.arg(internal_note)::text, '')
)
RETURNING id::text;

-- name: UpdateAvailabilityRule :execrows
UPDATE availability_rules SET
    iso_weekday = sqlc.arg(iso_weekday), local_start = sqlc.arg(local_start)::time,
    local_end = sqlc.arg(local_end)::time, valid_from = sqlc.arg(valid_from)::date,
    valid_until = NULLIF(sqlc.arg(valid_until)::text, '')::date, status = sqlc.arg(status),
    internal_note = NULLIF(sqlc.arg(internal_note)::text, ''), version = version + 1, updated_at = now()
WHERE id = sqlc.arg(id)::uuid AND driver_id = sqlc.arg(driver_id)::uuid AND version = sqlc.arg(expected_version);

-- name: DeleteAvailabilityRule :execrows
DELETE FROM availability_rules
WHERE id = sqlc.arg(id)::uuid AND driver_id = sqlc.arg(driver_id)::uuid AND version = sqlc.arg(expected_version);

-- name: ListAvailabilityExceptionsForDriver :many
SELECT id::text, driver_id::text, exception_type, all_day,
       COALESCE(local_date::text, '')::text AS local_date, starts_at, ends_at,
       COALESCE(internal_note, '')::text AS internal_note, version
FROM availability_exceptions
WHERE driver_id = sqlc.arg(driver_id)::uuid
ORDER BY COALESCE(local_date::timestamp AT TIME ZONE 'Europe/Vienna', starts_at), id;

-- name: ListAvailabilityExceptionsInRange :many
SELECT id::text, driver_id::text, exception_type, all_day,
       COALESCE(local_date::text, '')::text AS local_date, starts_at, ends_at,
       COALESCE(internal_note, '')::text AS internal_note, version
FROM availability_exceptions
WHERE driver_id = sqlc.arg(driver_id)::uuid AND (
    (all_day AND local_date BETWEEN sqlc.arg(local_from)::date AND sqlc.arg(local_to)::date)
    OR
    (NOT all_day AND starts_at < sqlc.arg(to_utc)::timestamptz AND ends_at > sqlc.arg(from_utc)::timestamptz)
)
ORDER BY COALESCE(local_date::timestamp AT TIME ZONE 'Europe/Vienna', starts_at), id;

-- name: InsertAvailabilityException :one
INSERT INTO availability_exceptions (
    driver_id, exception_type, all_day, local_date, starts_at, ends_at, internal_note
) VALUES (
    sqlc.arg(driver_id)::uuid, sqlc.arg(exception_type), sqlc.arg(all_day),
    NULLIF(sqlc.arg(local_date)::text, '')::date, NULLIF(sqlc.arg(starts_at)::text, '')::timestamptz,
    NULLIF(sqlc.arg(ends_at)::text, '')::timestamptz, NULLIF(sqlc.arg(internal_note)::text, '')
)
RETURNING id::text;

-- name: UpdateAvailabilityException :execrows
UPDATE availability_exceptions SET
    exception_type = sqlc.arg(exception_type), all_day = sqlc.arg(all_day),
    local_date = NULLIF(sqlc.arg(local_date)::text, '')::date,
    starts_at = NULLIF(sqlc.arg(starts_at)::text, '')::timestamptz,
    ends_at = NULLIF(sqlc.arg(ends_at)::text, '')::timestamptz,
    internal_note = NULLIF(sqlc.arg(internal_note)::text, ''), version = version + 1, updated_at = now()
WHERE id = sqlc.arg(id)::uuid AND driver_id = sqlc.arg(driver_id)::uuid AND version = sqlc.arg(expected_version);

-- name: DeleteAvailabilityException :execrows
DELETE FROM availability_exceptions
WHERE id = sqlc.arg(id)::uuid AND driver_id = sqlc.arg(driver_id)::uuid AND version = sqlc.arg(expected_version);
