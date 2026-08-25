-- name: DatabaseTime :one
SELECT now()::timestamptz;

-- name: SchemaApplication :one
SELECT value
FROM schema_metadata
WHERE key = 'application';

