-- name: CreateCalendarFeed :one
INSERT INTO calendar_feeds (owner_user_id, token_hash, name, feed_scope, detail_level, resource_types, expires_at)
VALUES (sqlc.arg(owner_user_id)::uuid, sqlc.arg(token_hash), sqlc.arg(name), sqlc.arg(feed_scope), sqlc.arg(detail_level), sqlc.arg(resource_types)::text[], sqlc.narg(expires_at))
RETURNING id::text, name, feed_scope, detail_level, resource_types, token_version, active, expires_at, last_used_at, revoked_at, version, created_at, updated_at;

-- name: ListCalendarFeeds :many
SELECT id::text, name, feed_scope, detail_level, resource_types, token_version, active, expires_at, last_used_at, revoked_at, version, created_at, updated_at
FROM calendar_feeds
WHERE owner_user_id=sqlc.arg(owner_user_id)::uuid
ORDER BY created_at DESC, id;

-- name: RotateCalendarFeed :one
UPDATE calendar_feeds
SET token_hash=sqlc.arg(token_hash), token_version=token_version+1, last_used_at=NULL, version=version+1, updated_at=now()
WHERE id=sqlc.arg(id)::uuid AND owner_user_id=sqlc.arg(owner_user_id)::uuid AND version=sqlc.arg(expected_version) AND active
RETURNING id::text, name, feed_scope, detail_level, resource_types, token_version, active, expires_at, last_used_at, revoked_at, version, created_at, updated_at;

-- name: RevokeCalendarFeed :one
UPDATE calendar_feeds
SET active=false, revoked_at=now(), version=version+1, updated_at=now()
WHERE id=sqlc.arg(id)::uuid AND owner_user_id=sqlc.arg(owner_user_id)::uuid AND version=sqlc.arg(expected_version) AND active
RETURNING id::text, name, feed_scope, detail_level, resource_types, token_version, active, expires_at, last_used_at, revoked_at, version, created_at, updated_at;

-- name: GetCalendarFeedByTokenHash :one
SELECT f.id::text, f.owner_user_id::text, f.name, f.feed_scope, f.detail_level, f.resource_types,
       f.token_version, f.expires_at, f.updated_at, u.active AS owner_active
FROM calendar_feeds f
JOIN users u ON u.id=f.owner_user_id
WHERE f.token_hash=sqlc.arg(token_hash) AND f.active AND f.revoked_at IS NULL;

-- name: TouchCalendarFeed :exec
UPDATE calendar_feeds SET last_used_at=sqlc.arg(used_at)
WHERE id=sqlc.arg(id)::uuid AND active AND (last_used_at IS NULL OR last_used_at < sqlc.arg(used_at) - interval '5 minutes');

-- name: ListCalendarFeedEvents :many
SELECT a.id::text, a.starts_at, a.ends_at, a.lifecycle_status, a.version AS appointment_version,
       a.created_at AS appointment_created_at, a.updated_at AS appointment_updated_at,
       j.job_number, j.job_type, j.volume_m3::text, j.version AS job_version, j.updated_at AS job_updated_at,
       c.first_name, c.last_name, COALESCE(c.company_name, '')::text AS company_name,
       c.street, c.postal_code, c.locality, c.country_code,
       COALESCE(j.pile_latitude::text, '')::text AS latitude, COALESCE(j.pile_longitude::text, '')::text AS longitude,
       c.version AS customer_version, c.updated_at AS customer_updated_at
FROM appointments a
JOIN jobs j ON j.id=a.job_id
JOIN customers c ON c.id=j.customer_id
WHERE a.starts_at < sqlc.arg(range_end)::timestamptz AND a.ends_at > sqlc.arg(range_start)::timestamptz
  AND a.lifecycle_status IN ('fixed','cancelled','completed')
  AND (sqlc.arg(feed_scope)::text='all' OR EXISTS (
      SELECT 1 FROM appointment_drivers ad JOIN drivers d ON d.id=ad.driver_id
      WHERE ad.appointment_id=a.id AND d.user_id=sqlc.arg(owner_user_id)::uuid
  ))
  AND (cardinality(sqlc.arg(resource_types)::text[])=0 OR EXISTS (
      SELECT 1 FROM appointment_resources ar JOIN resources r ON r.id=ar.resource_id
      WHERE ar.appointment_id=a.id AND r.resource_type=ANY(sqlc.arg(resource_types)::text[])
  ))
ORDER BY a.starts_at, a.id
LIMIT 2000;
