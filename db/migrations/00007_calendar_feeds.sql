-- +goose Up
CREATE TABLE calendar_feeds (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    owner_user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash bytea NOT NULL UNIQUE CHECK (octet_length(token_hash) = 32),
    name text NOT NULL CHECK (btrim(name) <> '' AND length(name) <= 100),
    feed_scope text NOT NULL DEFAULT 'all' CHECK (feed_scope IN ('all', 'own')),
    detail_level text NOT NULL DEFAULT 'internal' CHECK (detail_level IN ('internal', 'minimal')),
    resource_types text[] NOT NULL DEFAULT '{}'::text[]
        CHECK (resource_types <@ ARRAY['chipper','transport_vehicle','trailer','other']::text[]),
    token_version integer NOT NULL DEFAULT 1 CHECK (token_version > 0),
    active boolean NOT NULL DEFAULT true,
    expires_at timestamptz,
    last_used_at timestamptz,
    revoked_at timestamptz,
    version integer NOT NULL DEFAULT 1 CHECK (version > 0),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CHECK ((active AND revoked_at IS NULL) OR (NOT active AND revoked_at IS NOT NULL)),
    CHECK (expires_at IS NULL OR expires_at > created_at)
);

CREATE INDEX calendar_feeds_owner_idx ON calendar_feeds (owner_user_id, created_at DESC);
CREATE INDEX calendar_feeds_active_hash_idx ON calendar_feeds (token_hash) WHERE active;

-- +goose Down
DROP TABLE IF EXISTS calendar_feeds;
