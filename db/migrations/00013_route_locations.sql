-- +goose Up
CREATE TABLE route_locations (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    label text NOT NULL CHECK (btrim(label) <> '' AND length(label) <= 120),
    address text NOT NULL CHECK (btrim(address) <> '' AND length(address) <= 500),
    latitude numeric(9,6) NOT NULL CHECK (latitude BETWEEN -90 AND 90),
    longitude numeric(9,6) NOT NULL CHECK (longitude BETWEEN -180 AND 180),
    active boolean NOT NULL DEFAULT true,
    default_start boolean NOT NULL DEFAULT false,
    default_end boolean NOT NULL DEFAULT false,
    version integer NOT NULL DEFAULT 1 CHECK (version > 0),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CHECK (latitude <> 0 OR longitude <> 0),
    CHECK (active OR (NOT default_start AND NOT default_end))
);

CREATE UNIQUE INDEX route_locations_one_default_start_idx
    ON route_locations (default_start) WHERE active AND default_start;
CREATE UNIQUE INDEX route_locations_one_default_end_idx
    ON route_locations (default_end) WHERE active AND default_end;
CREATE INDEX route_locations_active_label_idx
    ON route_locations (active DESC, lower(label), id);

ALTER TABLE route_drafts
    ADD COLUMN start_label text NOT NULL DEFAULT '',
    ADD COLUMN end_label text NOT NULL DEFAULT '';

INSERT INTO schema_metadata (key, value)
VALUES ('application_schema_version', '13')
ON CONFLICT (key) DO UPDATE SET value=EXCLUDED.value, updated_at=now();

-- +goose Down
INSERT INTO schema_metadata (key, value)
VALUES ('application_schema_version', '12')
ON CONFLICT (key) DO UPDATE SET value=EXCLUDED.value, updated_at=now();

DROP TABLE IF EXISTS route_locations;

ALTER TABLE route_drafts
    DROP COLUMN IF EXISTS end_label,
    DROP COLUMN IF EXISTS start_label;
