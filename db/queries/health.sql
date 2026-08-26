-- name: DatabaseTime :one
SELECT now()::timestamptz;

-- name: SchemaApplication :one
SELECT value
FROM schema_metadata
WHERE key = 'application';

-- name: LatestAppliedMigration :one
SELECT value::bigint
FROM schema_metadata
WHERE key='application_schema_version';

-- name: UpsertWorkerHeartbeat :exec
INSERT INTO worker_heartbeats (worker_id, started_at, heartbeat_at, status)
VALUES (sqlc.arg(worker_id), sqlc.arg(started_at)::timestamptz, sqlc.arg(heartbeat_at)::timestamptz, sqlc.arg(status))
ON CONFLICT (worker_id) DO UPDATE
SET heartbeat_at=EXCLUDED.heartbeat_at, status=EXCLUDED.status;

-- name: LatestWorkerHeartbeat :one
SELECT COALESCE(max(heartbeat_at), '-infinity'::timestamptz)::timestamptz
FROM worker_heartbeats
WHERE status='running';

-- name: OperationalSnapshot :one
SELECT
    (SELECT count(*) FROM outbox_events WHERE status IN ('queued','claimed','retry_wait'))::bigint AS outbox_pending,
    (SELECT COALESCE(extract(epoch FROM (now() - min(created_at))), 0) FROM outbox_events WHERE status IN ('queued','claimed','retry_wait'))::float8 AS outbox_oldest_seconds,
    (SELECT COALESCE(sum(attempt_count), 0) FROM outbox_events WHERE status IN ('queued','claimed','retry_wait','dead'))::bigint AS outbox_attempts,
    (SELECT count(*) FROM sessions WHERE idle_expires_at > now() AND absolute_expires_at > now())::bigint AS active_sessions,
    (SELECT count(*) FROM planning_runs WHERE created_at > now() - interval '5 minutes')::bigint AS planning_runs_recent,
    (SELECT count(*) FROM planning_suggestions ps JOIN planning_runs pr ON pr.id=ps.run_id WHERE pr.created_at > now() - interval '5 minutes')::bigint AS planning_candidates_recent;

-- name: NotificationMetricCounts :many
SELECT channel, status, count(*)::bigint AS total
FROM notifications
GROUP BY channel, status
ORDER BY channel, status;

-- name: VoiceMetricCounts :many
SELECT status, count(*)::bigint AS total
FROM voice_drafts
GROUP BY status
ORDER BY status;
