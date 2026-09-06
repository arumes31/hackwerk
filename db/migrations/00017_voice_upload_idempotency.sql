-- +goose Up
ALTER TABLE voice_recordings
    ADD COLUMN upload_key_hash bytea,
    ADD COLUMN manual_retry_count smallint NOT NULL DEFAULT 0 CHECK (manual_retry_count BETWEEN 0 AND 1);

CREATE UNIQUE INDEX voice_recordings_owner_upload_key_idx
    ON voice_recordings (owner_user_id, upload_key_hash)
    WHERE upload_key_hash IS NOT NULL;

INSERT INTO schema_metadata (key, value)
VALUES ('application_schema_version', '17')
ON CONFLICT (key) DO UPDATE SET value=EXCLUDED.value, updated_at=now();

-- +goose Down
DROP INDEX IF EXISTS voice_recordings_owner_upload_key_idx;

ALTER TABLE voice_recordings
    DROP COLUMN IF EXISTS manual_retry_count,
    DROP COLUMN IF EXISTS upload_key_hash;

INSERT INTO schema_metadata (key, value)
VALUES ('application_schema_version', '16')
ON CONFLICT (key) DO UPDATE SET value=EXCLUDED.value, updated_at=now();
