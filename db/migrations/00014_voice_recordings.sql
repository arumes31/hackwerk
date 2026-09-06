-- +goose Up
CREATE TABLE voice_recordings (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    draft_id uuid NOT NULL UNIQUE REFERENCES voice_drafts(id) ON DELETE CASCADE,
    owner_user_id uuid NOT NULL REFERENCES users(id),
    content_type text NOT NULL CHECK (content_type IN ('audio/webm', 'audio/ogg', 'audio/wav')),
    audio_bytes bytea NOT NULL CHECK (octet_length(audio_bytes) BETWEEN 1 AND 15728640),
    byte_size integer NOT NULL CHECK (byte_size BETWEEN 1 AND 15728640 AND byte_size = octet_length(audio_bytes)),
    duration_ms integer NOT NULL CHECK (duration_ms BETWEEN 1 AND 300000),
    recorded_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL,
    available_at timestamptz NOT NULL DEFAULT now(),
    claimed_by text,
    lease_until timestamptz,
    attempt_count smallint NOT NULL DEFAULT 0 CHECK (attempt_count BETWEEN 0 AND 3),
    max_attempts smallint NOT NULL DEFAULT 3 CHECK (max_attempts BETWEEN 1 AND 3),
    failure_code text NOT NULL DEFAULT '' CHECK (length(failure_code) <= 100),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CHECK (expires_at > created_at),
    CHECK ((claimed_by IS NULL AND lease_until IS NULL) OR (claimed_by IS NOT NULL AND lease_until IS NOT NULL))
);

CREATE INDEX voice_recordings_queue_idx
    ON voice_recordings (available_at, created_at, id)
    WHERE attempt_count < max_attempts;
CREATE INDEX voice_recordings_expiry_idx ON voice_recordings (expires_at);
CREATE INDEX voice_recordings_admin_idx ON voice_recordings (created_at DESC, id DESC);

INSERT INTO schema_metadata (key, value)
VALUES ('application_schema_version', '14')
ON CONFLICT (key) DO UPDATE SET value=EXCLUDED.value, updated_at=now();

-- +goose Down
INSERT INTO schema_metadata (key, value)
VALUES ('application_schema_version', '13')
ON CONFLICT (key) DO UPDATE SET value=EXCLUDED.value, updated_at=now();

DROP TABLE IF EXISTS voice_recordings;
