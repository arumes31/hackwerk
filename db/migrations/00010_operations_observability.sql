-- +goose Up
CREATE TABLE worker_heartbeats (
    worker_id text PRIMARY KEY,
    started_at timestamptz NOT NULL,
    heartbeat_at timestamptz NOT NULL,
    status text NOT NULL CHECK (status IN ('running', 'stopping')),
    CHECK (btrim(worker_id) <> '' AND length(worker_id) <= 128),
    CHECK (heartbeat_at >= started_at)
);

CREATE INDEX worker_heartbeats_latest_idx ON worker_heartbeats (heartbeat_at DESC);

INSERT INTO schema_metadata (key, value)
VALUES ('application_schema_version', '10')
ON CONFLICT (key) DO UPDATE SET value=EXCLUDED.value, updated_at=now();

-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT FROM pg_roles WHERE rolname='hackwerk_app') THEN
        REVOKE UPDATE, DELETE ON audit_events FROM hackwerk_app;
        REVOKE INSERT, UPDATE, DELETE ON schema_metadata FROM hackwerk_app;
        REVOKE INSERT, UPDATE, DELETE ON goose_db_version FROM hackwerk_app;
    END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
DELETE FROM schema_metadata WHERE key='application_schema_version';
DROP TABLE IF EXISTS worker_heartbeats;
