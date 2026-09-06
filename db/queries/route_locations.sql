-- name: ListRouteLocations :many
SELECT id::text, label, address, latitude::text, longitude::text, active, default_start, default_end,
       version, created_at, updated_at
FROM route_locations
ORDER BY active DESC, lower(label), id;

-- name: ListActiveRouteLocations :many
SELECT id::text, label, address, latitude::text, longitude::text, active, default_start, default_end,
       version, created_at, updated_at
FROM route_locations
WHERE active
ORDER BY lower(label), id;

-- name: GetDefaultRouteStartLocation :one
SELECT id::text, label, address, latitude::text, longitude::text, active, default_start, default_end,
       version, created_at, updated_at
FROM route_locations
WHERE active AND default_start;

-- name: GetActiveRouteLocation :one
SELECT id::text, label, address, latitude::text, longitude::text, active, default_start, default_end,
       version, created_at, updated_at
FROM route_locations
WHERE id=sqlc.arg(id)::uuid AND active;

-- name: LockRouteLocationDefaults :exec
SELECT pg_advisory_xact_lock(318945712);

-- name: ClearRouteLocationStartDefaultForCreate :exec
UPDATE route_locations
SET default_start=false, version=version+1, updated_at=now()
WHERE active AND default_start;

-- name: ClearRouteLocationEndDefaultForCreate :exec
UPDATE route_locations
SET default_end=false, version=version+1, updated_at=now()
WHERE active AND default_end;

-- name: ClearRouteLocationStartDefaultForUpdate :exec
UPDATE route_locations
SET default_start=false, version=version+1, updated_at=now()
WHERE active AND default_start AND id <> sqlc.arg(id)::uuid;

-- name: ClearRouteLocationEndDefaultForUpdate :exec
UPDATE route_locations
SET default_end=false, version=version+1, updated_at=now()
WHERE active AND default_end AND id <> sqlc.arg(id)::uuid;

-- name: InsertRouteLocation :one
INSERT INTO route_locations (label, address, latitude, longitude, default_start, default_end)
VALUES (sqlc.arg(label), sqlc.arg(address), sqlc.arg(latitude)::numeric, sqlc.arg(longitude)::numeric,
        sqlc.arg(default_start), sqlc.arg(default_end))
RETURNING id::text, label, address, latitude::text, longitude::text, active, default_start, default_end,
          version, created_at, updated_at;

-- name: GetRouteLocationForUpdate :one
SELECT active, version
FROM route_locations
WHERE id=sqlc.arg(id)::uuid
FOR UPDATE;

-- name: UpdateRouteLocation :one
UPDATE route_locations
SET label=sqlc.arg(label), address=sqlc.arg(address), latitude=sqlc.arg(latitude)::numeric,
    longitude=sqlc.arg(longitude)::numeric, default_start=sqlc.arg(default_start),
    default_end=sqlc.arg(default_end), version=version+1, updated_at=now()
WHERE id=sqlc.arg(id)::uuid AND version=sqlc.arg(expected_version) AND active
RETURNING id::text, label, address, latitude::text, longitude::text, active, default_start, default_end,
          version, created_at, updated_at;

-- name: DeactivateRouteLocation :execrows
UPDATE route_locations
SET active=false, default_start=false, default_end=false, version=version+1, updated_at=now()
WHERE id=sqlc.arg(id)::uuid AND version=sqlc.arg(expected_version) AND active;
