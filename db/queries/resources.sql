-- name: ListResources :many
SELECT id::text, resource_type, name, exclusive, active, capacity_metadata, COALESCE(internal_note, '')::text AS internal_note,
       version, created_at, updated_at
FROM resources
ORDER BY active DESC, lower(name), id;

-- name: InsertResource :one
INSERT INTO resources (resource_type, name, exclusive, capacity_metadata, internal_note)
VALUES (sqlc.arg(resource_type), sqlc.arg(name), sqlc.arg(exclusive), sqlc.arg(capacity_metadata)::jsonb,
        NULLIF(sqlc.arg(internal_note)::text, ''))
RETURNING id::text;

-- name: UpdateResource :execrows
UPDATE resources SET
    resource_type = sqlc.arg(resource_type), name = sqlc.arg(name), exclusive = sqlc.arg(exclusive),
    capacity_metadata = sqlc.arg(capacity_metadata)::jsonb, internal_note = NULLIF(sqlc.arg(internal_note)::text, ''),
    version = version + 1, updated_at = now()
WHERE id = sqlc.arg(id)::uuid AND version = sqlc.arg(expected_version) AND active;

-- name: DeactivateResource :execrows
UPDATE resources SET active = false, version = version + 1, updated_at = now()
WHERE id = sqlc.arg(id)::uuid AND version = sqlc.arg(expected_version) AND active;
