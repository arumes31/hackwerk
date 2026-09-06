-- +goose Up
CREATE TABLE transport_partners (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    partner_type text NOT NULL CHECK (partner_type IN ('person', 'company')),
    name text NOT NULL CHECK (btrim(name) <> '' AND length(name) <= 200),
    phone text CHECK (phone IS NULL OR (length(phone) <= 64 AND phone !~ '[\r\n]')),
    address text CHECK (address IS NULL OR length(address) <= 500),
    internal_note text CHECK (internal_note IS NULL OR length(internal_note) <= 1000),
    active boolean NOT NULL DEFAULT true,
    version integer NOT NULL DEFAULT 1 CHECK (version > 0),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX transport_partners_active_name_idx
    ON transport_partners (lower(name)) WHERE active;

ALTER TABLE jobs
    ADD COLUMN transport_partner_id uuid REFERENCES transport_partners(id) ON DELETE RESTRICT,
    ADD CONSTRAINT jobs_transport_partner_type_check
        CHECK (job_type <> 'chipping_only' OR transport_partner_id IS NULL);

CREATE INDEX jobs_transport_partner_idx ON jobs (transport_partner_id) WHERE transport_partner_id IS NOT NULL;

INSERT INTO schema_metadata (key, value)
VALUES ('application_schema_version', '21')
ON CONFLICT (key) DO UPDATE SET value=EXCLUDED.value, updated_at=now();

-- +goose Down
DROP INDEX IF EXISTS jobs_transport_partner_idx;
ALTER TABLE jobs
    DROP CONSTRAINT IF EXISTS jobs_transport_partner_type_check,
    DROP COLUMN transport_partner_id;
DROP TABLE IF EXISTS transport_partners;

INSERT INTO schema_metadata (key, value)
VALUES ('application_schema_version', '20')
ON CONFLICT (key) DO UPDATE SET value=EXCLUDED.value, updated_at=now();
