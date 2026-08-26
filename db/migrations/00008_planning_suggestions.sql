-- +goose Up
CREATE TABLE planning_runs (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    job_id uuid NOT NULL REFERENCES jobs(id),
    actor_user_id uuid NOT NULL REFERENCES users(id),
    job_version integer NOT NULL CHECK (job_version > 0),
    waitlist_version integer NOT NULL CHECK (waitlist_version > 0),
    search_from timestamptz NOT NULL,
    search_to timestamptz NOT NULL,
    input_fingerprint bytea NOT NULL CHECK (octet_length(input_fingerprint) = 32),
    config_snapshot jsonb NOT NULL CHECK (jsonb_typeof(config_snapshot) = 'object'),
    created_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL,
    CHECK (search_to > search_from),
    CHECK (expires_at > created_at)
);

CREATE INDEX planning_runs_job_created_idx ON planning_runs (job_id, created_at DESC);
CREATE INDEX planning_runs_expiry_idx ON planning_runs (expires_at);

CREATE TABLE planning_suggestions (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    run_id uuid NOT NULL REFERENCES planning_runs(id) ON DELETE CASCADE,
    rank smallint NOT NULL CHECK (rank BETWEEN 1 AND 3),
    starts_at timestamptz NOT NULL,
    ends_at timestamptz NOT NULL,
    driver_id uuid NOT NULL REFERENCES drivers(id),
    resource_ids uuid[] NOT NULL CHECK (cardinality(resource_ids) > 0),
    resource_purposes text[] NOT NULL CHECK (cardinality(resource_ids) = cardinality(resource_purposes)),
    score numeric(5,2) NOT NULL CHECK (score BETWEEN 0 AND 100),
    components jsonb NOT NULL CHECK (jsonb_typeof(components) = 'object'),
    reasons text[] NOT NULL,
    warnings text[] NOT NULL,
    routing_source text NOT NULL CHECK (routing_source IN ('osrm', 'haversine', 'unavailable')),
    distance_meters integer CHECK (distance_meters >= 0),
    duration_seconds integer CHECK (duration_seconds >= 0),
    status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'adopted', 'stale', 'discarded')),
    adopted_appointment_id uuid REFERENCES appointments(id),
    adopted_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (run_id, rank),
    CHECK (ends_at > starts_at),
    CHECK ((status = 'adopted') = (adopted_appointment_id IS NOT NULL AND adopted_at IS NOT NULL))
);

CREATE INDEX planning_suggestions_run_idx ON planning_suggestions (run_id, rank);

-- +goose Down
DROP TABLE IF EXISTS planning_suggestions;
DROP TABLE IF EXISTS planning_runs;
