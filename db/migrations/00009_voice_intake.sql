-- +goose Up
CREATE TABLE voice_drafts (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    owner_user_id uuid NOT NULL REFERENCES users(id),
    status text NOT NULL CHECK (status IN ('recorded', 'transcribing', 'needs_review', 'failed', 'committed', 'expired')),
    transcript text,
    extracted_fields jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(extracted_fields) = 'object'),
    warnings text[] NOT NULL DEFAULT '{}',
    overall_confidence numeric(4,3) CHECK (overall_confidence BETWEEN 0 AND 1),
    provider_name text NOT NULL DEFAULT '',
    provider_version text NOT NULL DEFAULT '',
    parser_version text NOT NULL DEFAULT '',
    failure_code text NOT NULL DEFAULT '',
    retry_count smallint NOT NULL DEFAULT 0 CHECK (retry_count BETWEEN 0 AND 3),
    committed_customer_id uuid REFERENCES customers(id),
    committed_job_id uuid REFERENCES jobs(id),
    committed_waitlist_id uuid REFERENCES waitlist_entries(id),
    committed_at timestamptz,
    expires_at timestamptz NOT NULL,
    version integer NOT NULL DEFAULT 1 CHECK (version > 0),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CHECK (expires_at > created_at),
    CHECK (
        (status = 'committed' AND committed_customer_id IS NOT NULL AND committed_job_id IS NOT NULL AND committed_waitlist_id IS NOT NULL AND committed_at IS NOT NULL)
        OR
        (status <> 'committed' AND committed_customer_id IS NULL AND committed_job_id IS NULL AND committed_waitlist_id IS NULL AND committed_at IS NULL)
    )
);

CREATE INDEX voice_drafts_owner_created_idx ON voice_drafts (owner_user_id, created_at DESC);
CREATE INDEX voice_drafts_expiry_idx ON voice_drafts (expires_at) WHERE status NOT IN ('committed', 'expired');

-- +goose Down
DROP TABLE IF EXISTS voice_drafts;
