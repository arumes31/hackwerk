-- +goose Up
CREATE TABLE users (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    username citext NOT NULL UNIQUE,
    display_name text NOT NULL CHECK (btrim(display_name) <> ''),
    email citext,
    role text NOT NULL CHECK (role IN ('admin', 'driver')),
    password_hash text NOT NULL,
    must_change_password boolean NOT NULL DEFAULT true,
    active boolean NOT NULL DEFAULT true,
    last_login_at timestamptz,
    version integer NOT NULL DEFAULT 1 CHECK (version > 0),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CHECK (btrim(username::text) <> '')
);

CREATE TABLE drivers (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id uuid UNIQUE REFERENCES users(id) ON DELETE SET NULL,
    display_name text NOT NULL CHECK (btrim(display_name) <> ''),
    phone text,
    email citext,
    active boolean NOT NULL DEFAULT true,
    can_complete_jobs boolean NOT NULL DEFAULT true,
    internal_note text,
    version integer NOT NULL DEFAULT 1 CHECK (version > 0),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE sessions (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash bytea NOT NULL UNIQUE CHECK (octet_length(token_hash) = 32),
    csrf_token_hash bytea NOT NULL CHECK (octet_length(csrf_token_hash) = 32),
    idle_expires_at timestamptz NOT NULL,
    absolute_expires_at timestamptz NOT NULL,
    last_used_at timestamptz NOT NULL DEFAULT now(),
    revoked_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    CHECK (absolute_expires_at > created_at),
    CHECK (idle_expires_at <= absolute_expires_at)
);

CREATE INDEX sessions_active_user_idx ON sessions (user_id, absolute_expires_at)
    WHERE revoked_at IS NULL;
CREATE INDEX sessions_expiry_idx ON sessions (idle_expires_at, absolute_expires_at)
    WHERE revoked_at IS NULL;

CREATE TABLE auth_rate_limits (
    key_hash bytea PRIMARY KEY CHECK (octet_length(key_hash) = 32),
    window_started_at timestamptz NOT NULL,
    failure_count integer NOT NULL DEFAULT 0 CHECK (failure_count >= 0),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE audit_events (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    actor_type text NOT NULL CHECK (actor_type IN ('user', 'system', 'public')),
    actor_user_id uuid REFERENCES users(id) ON DELETE SET NULL,
    action text NOT NULL CHECK (btrim(action) <> ''),
    object_type text NOT NULL CHECK (btrim(object_type) <> ''),
    object_id text,
    request_id text,
    metadata jsonb NOT NULL DEFAULT '{}'::jsonb,
    occurred_at timestamptz NOT NULL DEFAULT now(),
    CHECK (jsonb_typeof(metadata) = 'object')
);

CREATE INDEX audit_events_object_idx ON audit_events (object_type, object_id, occurred_at DESC);
CREATE INDEX audit_events_actor_idx ON audit_events (actor_user_id, occurred_at DESC);

-- +goose Down
DROP TABLE IF EXISTS audit_events;
DROP TABLE IF EXISTS auth_rate_limits;
DROP TABLE IF EXISTS sessions;
DROP TABLE IF EXISTS drivers;
DROP TABLE IF EXISTS users;
