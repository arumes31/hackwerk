-- +goose Up
-- A pending TOTP enrollment is bound to the session that re-entered the
-- password. Existing pending enrollments have no such session and therefore
-- must be restarted before they can be confirmed.
ALTER TABLE user_totp_credentials
    ADD COLUMN enrollment_session_id uuid REFERENCES sessions(id) ON DELETE SET NULL;

INSERT INTO schema_metadata (key, value)
VALUES ('application_schema_version', '23')
ON CONFLICT (key) DO UPDATE SET value=EXCLUDED.value, updated_at=now();

-- +goose Down
ALTER TABLE user_totp_credentials
    DROP COLUMN IF EXISTS enrollment_session_id;

INSERT INTO schema_metadata (key, value)
VALUES ('application_schema_version', '22')
ON CONFLICT (key) DO UPDATE SET value=EXCLUDED.value, updated_at=now();
