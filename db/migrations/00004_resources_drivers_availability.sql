-- +goose Up
CREATE TABLE resources (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    resource_type text NOT NULL CHECK (resource_type IN ('chipper', 'transport_vehicle', 'trailer', 'other')),
    name text NOT NULL CHECK (btrim(name) <> '' AND length(name) <= 200),
    exclusive boolean NOT NULL DEFAULT true,
    active boolean NOT NULL DEFAULT true,
    capacity_metadata jsonb NOT NULL DEFAULT '{}'::jsonb,
    internal_note text,
    version integer NOT NULL DEFAULT 1 CHECK (version > 0),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CHECK (jsonb_typeof(capacity_metadata) = 'object'),
    CHECK ((capacity_metadata - ARRAY['volume_m3', 'payload_kg', 'seats']::text[]) = '{}'::jsonb),
    CHECK (NOT capacity_metadata ? 'volume_m3' OR jsonb_path_exists(capacity_metadata, '$.volume_m3 ? (@ > 0 && @ <= 1000000)')),
    CHECK (NOT capacity_metadata ? 'payload_kg' OR jsonb_path_exists(capacity_metadata, '$.payload_kg ? (@ > 0 && @ <= 10000000)')),
    CHECK (NOT capacity_metadata ? 'seats' OR jsonb_path_exists(capacity_metadata, '$.seats ? (@ > 0 && @ <= 1000)')),
    CHECK (internal_note IS NULL OR length(internal_note) <= 4000)
);

CREATE UNIQUE INDEX resources_active_name_idx ON resources (lower(name)) WHERE active;
CREATE INDEX resources_type_active_idx ON resources (resource_type, active, lower(name));

ALTER TABLE drivers
    ADD CONSTRAINT drivers_display_name_length CHECK (length(display_name) <= 200),
    ADD CONSTRAINT drivers_phone_length CHECK (phone IS NULL OR length(phone) <= 64),
    ADD CONSTRAINT drivers_internal_note_length CHECK (internal_note IS NULL OR length(internal_note) <= 4000);

CREATE INDEX drivers_active_name_idx ON drivers (active, lower(display_name));

CREATE TABLE availability_rules (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    driver_id uuid NOT NULL REFERENCES drivers(id),
    iso_weekday smallint NOT NULL CHECK (iso_weekday BETWEEN 1 AND 7),
    local_start time NOT NULL,
    local_end time NOT NULL,
    valid_from date NOT NULL,
    valid_until date,
    status text NOT NULL CHECK (status IN ('available', 'limited')),
    internal_note text,
    version integer NOT NULL DEFAULT 1 CHECK (version > 0),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CHECK (local_end > local_start),
    CHECK (valid_until IS NULL OR valid_until >= valid_from),
    CHECK (internal_note IS NULL OR length(internal_note) <= 1000),
    EXCLUDE USING gist (
        driver_id WITH =,
        iso_weekday WITH =,
        (daterange(valid_from, COALESCE(valid_until, 'infinity'::date), '[]')) WITH &&,
        (int4range(
            (EXTRACT(HOUR FROM local_start)::integer * 60) + EXTRACT(MINUTE FROM local_start)::integer,
            (EXTRACT(HOUR FROM local_end)::integer * 60) + EXTRACT(MINUTE FROM local_end)::integer,
            '[)'
        )) WITH &&
    )
);

CREATE INDEX availability_rules_driver_idx ON availability_rules (driver_id, iso_weekday, valid_from);

CREATE TABLE availability_exceptions (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    driver_id uuid NOT NULL REFERENCES drivers(id),
    exception_type text NOT NULL CHECK (exception_type IN ('vacation', 'sick', 'unavailable', 'available_override', 'other')),
    all_day boolean NOT NULL,
    local_date date,
    starts_at timestamptz,
    ends_at timestamptz,
    internal_note text,
    version integer NOT NULL DEFAULT 1 CHECK (version > 0),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CHECK (
        (all_day AND local_date IS NOT NULL AND starts_at IS NULL AND ends_at IS NULL)
        OR
        (NOT all_day AND local_date IS NULL AND starts_at IS NOT NULL AND ends_at IS NOT NULL AND ends_at > starts_at)
    ),
    CHECK (internal_note IS NULL OR length(internal_note) <= 1000)
);

CREATE INDEX availability_exceptions_driver_day_idx ON availability_exceptions (driver_id, local_date) WHERE all_day;
CREATE INDEX availability_exceptions_driver_range_idx ON availability_exceptions (driver_id, starts_at, ends_at) WHERE NOT all_day;

-- +goose Down
DROP TABLE IF EXISTS availability_exceptions;
DROP TABLE IF EXISTS availability_rules;
DROP INDEX IF EXISTS drivers_active_name_idx;
ALTER TABLE drivers
    DROP CONSTRAINT IF EXISTS drivers_internal_note_length,
    DROP CONSTRAINT IF EXISTS drivers_phone_length,
    DROP CONSTRAINT IF EXISTS drivers_display_name_length;
DROP TABLE IF EXISTS resources;
