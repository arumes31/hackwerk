-- +goose Up
ALTER TABLE users
    ADD COLUMN salutation text NOT NULL DEFAULT ''
        CHECK (salutation IN ('', 'frau', 'herr', 'divers')),
    ADD COLUMN work_phone_raw text NOT NULL DEFAULT ''
        CHECK (length(work_phone_raw) <= 80 AND work_phone_raw !~ '[\r\n]'),
    ADD COLUMN work_phone_normalized text NOT NULL DEFAULT ''
        CHECK (work_phone_normalized = '' OR work_phone_normalized ~ '^\+[1-9][0-9]{6,14}$'),
    ADD COLUMN email_verified_at timestamptz,
    ADD COLUMN webauthn_user_handle bytea NOT NULL DEFAULT gen_random_bytes(32)
        CHECK (octet_length(webauthn_user_handle) BETWEEN 16 AND 64);

UPDATE users
SET email_verified_at = COALESCE(email_verified_at, created_at)
WHERE email IS NOT NULL;

ALTER TABLE sessions
    ADD COLUMN device_label text NOT NULL DEFAULT 'Unbekanntes Gerät'
        CHECK (btrim(device_label) <> '' AND length(device_label) <= 120 AND device_label !~ '[\r\n]');

CREATE TABLE user_email_verifications (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    email citext NOT NULL CHECK (btrim(email::text) <> '' AND length(email::text) <= 320),
    token_hash bytea NOT NULL UNIQUE CHECK (octet_length(token_hash) = 32),
    token_key_id text NOT NULL CHECK (btrim(token_key_id) <> '' AND length(token_key_id) <= 100),
    token_version integer NOT NULL DEFAULT 1 CHECK (token_version > 0),
    status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'verified', 'cancelled', 'expired')),
    send_count integer NOT NULL DEFAULT 1 CHECK (send_count BETWEEN 1 AND 20),
    last_sent_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL,
    verified_at timestamptz,
    cancelled_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CHECK (expires_at > created_at),
    CHECK ((status = 'verified') = (verified_at IS NOT NULL)),
    CHECK ((status = 'cancelled') = (cancelled_at IS NOT NULL))
);

CREATE UNIQUE INDEX user_email_verifications_one_pending_idx
    ON user_email_verifications (user_id) WHERE status = 'pending';
CREATE INDEX user_email_verifications_expiry_idx
    ON user_email_verifications (expires_at) WHERE status = 'pending';

CREATE TABLE user_totp_credentials (
    user_id uuid PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    name text NOT NULL CHECK (btrim(name) <> '' AND length(name) <= 100 AND name !~ '[\r\n]'),
    secret_key_id text NOT NULL CHECK (btrim(secret_key_id) <> '' AND length(secret_key_id) <= 100),
    secret_ciphertext bytea NOT NULL CHECK (octet_length(secret_ciphertext) >= 29),
    enabled_at timestamptz,
    last_used_step bigint,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE user_webauthn_credentials (
    credential_id bytea PRIMARY KEY CHECK (octet_length(credential_id) BETWEEN 16 AND 1024),
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name text NOT NULL CHECK (btrim(name) <> '' AND length(name) <= 100 AND name !~ '[\r\n]'),
    credential_key_id text NOT NULL CHECK (btrim(credential_key_id) <> '' AND length(credential_key_id) <= 100),
    credential_ciphertext bytea NOT NULL CHECK (octet_length(credential_ciphertext) >= 29),
    created_at timestamptz NOT NULL DEFAULT now(),
    last_used_at timestamptz,
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX user_webauthn_credentials_user_idx
    ON user_webauthn_credentials (user_id, created_at, credential_id);

CREATE TABLE user_recovery_codes (
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    code_hash bytea NOT NULL CHECK (octet_length(code_hash) = 32),
    used_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, code_hash)
);

CREATE INDEX user_recovery_codes_available_idx
    ON user_recovery_codes (user_id) WHERE used_at IS NULL;

CREATE TABLE auth_login_challenges (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash bytea NOT NULL UNIQUE CHECK (octet_length(token_hash) = 32),
    expires_at timestamptz NOT NULL,
    attempt_count integer NOT NULL DEFAULT 0 CHECK (attempt_count BETWEEN 0 AND 10),
    webauthn_session jsonb CHECK (webauthn_session IS NULL OR jsonb_typeof(webauthn_session) = 'object'),
    consumed_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    CHECK (expires_at > created_at)
);

CREATE INDEX auth_login_challenges_active_idx
    ON auth_login_challenges (token_hash, expires_at) WHERE consumed_at IS NULL;

CREATE TABLE user_webauthn_registration_challenges (
    session_id uuid PRIMARY KEY REFERENCES sessions(id) ON DELETE CASCADE,
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    session_data jsonb NOT NULL CHECK (jsonb_typeof(session_data) = 'object'),
    expires_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    CHECK (expires_at > created_at)
);

CREATE INDEX user_webauthn_registration_expiry_idx
    ON user_webauthn_registration_challenges (expires_at);

INSERT INTO schema_metadata (key, value)
VALUES ('application_schema_version', '19')
ON CONFLICT (key) DO UPDATE SET value=EXCLUDED.value, updated_at=now();

-- +goose Down
DROP TABLE IF EXISTS user_webauthn_registration_challenges;
DROP TABLE IF EXISTS auth_login_challenges;
DROP TABLE IF EXISTS user_recovery_codes;
DROP TABLE IF EXISTS user_webauthn_credentials;
DROP TABLE IF EXISTS user_totp_credentials;
DROP TABLE IF EXISTS user_email_verifications;

ALTER TABLE sessions DROP COLUMN device_label;
ALTER TABLE users
    DROP COLUMN webauthn_user_handle,
    DROP COLUMN email_verified_at,
    DROP COLUMN work_phone_normalized,
    DROP COLUMN work_phone_raw,
    DROP COLUMN salutation;

INSERT INTO schema_metadata (key, value)
VALUES ('application_schema_version', '18')
ON CONFLICT (key) DO UPDATE SET value=EXCLUDED.value, updated_at=now();
