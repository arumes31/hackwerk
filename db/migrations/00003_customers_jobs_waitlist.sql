-- +goose Up
CREATE TABLE customers (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    first_name text NOT NULL DEFAULT '',
    last_name text NOT NULL DEFAULT '',
    company_name text,
    street text NOT NULL DEFAULT '',
    postal_code text NOT NULL DEFAULT '',
    locality text NOT NULL DEFAULT '',
    region text NOT NULL DEFAULT '',
    country_code char(2) NOT NULL DEFAULT 'AT',
    address_freeform text,
    phone_raw text,
    phone_normalized text,
    email citext,
    notification_preference text NOT NULL DEFAULT 'none'
        CHECK (notification_preference IN ('email', 'sms', 'both', 'none')),
    latitude numeric(9,6),
    longitude numeric(9,6),
    location_source text CHECK (location_source IN ('manual', 'geocoder')),
    geocoding_status text NOT NULL DEFAULT 'not_requested'
        CHECK (geocoding_status IN ('not_requested', 'pending', 'resolved', 'failed', 'needs_review')),
    archived_at timestamptz,
    version integer NOT NULL DEFAULT 1 CHECK (version > 0),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CHECK (btrim(first_name) <> '' OR btrim(last_name) <> '' OR btrim(COALESCE(company_name, '')) <> ''),
    CHECK ((latitude IS NULL) = (longitude IS NULL)),
    CHECK (latitude IS NULL OR latitude BETWEEN -90 AND 90),
    CHECK (longitude IS NULL OR longitude BETWEEN -180 AND 180)
);

CREATE INDEX customers_active_name_idx ON customers (lower(last_name), lower(first_name)) WHERE archived_at IS NULL;
CREATE INDEX customers_phone_idx ON customers (phone_normalized) WHERE phone_normalized IS NOT NULL;
CREATE INDEX customers_email_idx ON customers (email) WHERE email IS NOT NULL;

CREATE TABLE job_number_counters (
    year integer PRIMARY KEY CHECK (year BETWEEN 2020 AND 9999),
    next_value bigint NOT NULL CHECK (next_value > 0)
);

CREATE TABLE jobs (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    job_number text NOT NULL UNIQUE,
    customer_id uuid NOT NULL REFERENCES customers(id),
    job_type text NOT NULL CHECK (job_type IN ('chipping_only', 'chipping_with_transport')),
    volume_m3 numeric(10,2) NOT NULL CHECK (volume_m3 > 0),
    estimated_hack_minutes integer NOT NULL CHECK (estimated_hack_minutes > 0),
    estimated_transport_minutes integer NOT NULL DEFAULT 0 CHECK (estimated_transport_minutes >= 0),
    transport_trip_count integer NOT NULL DEFAULT 0 CHECK (transport_trip_count >= 0),
    transport_mode text NOT NULL DEFAULT 'none' CHECK (transport_mode IN ('none', 'internal', 'external', 'undecided')),
    external_transport_confirmed boolean NOT NULL DEFAULT false,
    preferred_start_date date,
    preferred_end_date date,
    preference_text text,
    urgency text NOT NULL DEFAULT 'normal' CHECK (urgency IN ('low', 'normal', 'high', 'urgent')),
    region text,
    source text NOT NULL DEFAULT 'phone' CHECK (source IN ('phone', 'voice', 'email', 'in_person', 'other')),
    workflow_status text NOT NULL DEFAULT 'waitlist' CHECK (workflow_status IN ('waitlist', 'planning', 'scheduled', 'completed', 'cancelled')),
    received_at timestamptz NOT NULL DEFAULT now(),
    archived_at timestamptz,
    version integer NOT NULL DEFAULT 1 CHECK (version > 0),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CHECK (preferred_end_date IS NULL OR preferred_start_date IS NULL OR preferred_end_date >= preferred_start_date),
    CHECK (
        (job_type = 'chipping_only' AND estimated_transport_minutes = 0 AND transport_trip_count = 0 AND transport_mode = 'none' AND NOT external_transport_confirmed)
        OR job_type = 'chipping_with_transport'
    ),
    CHECK (NOT external_transport_confirmed OR (job_type = 'chipping_with_transport' AND transport_mode = 'external'))
);

CREATE INDEX jobs_customer_idx ON jobs (customer_id, received_at DESC);
CREATE INDEX jobs_active_status_idx ON jobs (workflow_status, received_at) WHERE archived_at IS NULL;

CREATE TABLE waitlist_entries (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    job_id uuid NOT NULL REFERENCES jobs(id),
    entered_at timestamptz NOT NULL DEFAULT now(),
    manual_priority integer NOT NULL DEFAULT 0 CHECK (manual_priority BETWEEN -100 AND 100),
    position_hint numeric,
    region_snapshot text,
    removed_at timestamptz,
    removed_reason text CHECK (removed_reason IN ('scheduled', 'cancelled', 'duplicate', 'other')),
    version integer NOT NULL DEFAULT 1 CHECK (version > 0),
    CHECK ((removed_at IS NULL) = (removed_reason IS NULL))
);

CREATE UNIQUE INDEX waitlist_one_active_job_idx ON waitlist_entries (job_id) WHERE removed_at IS NULL;
CREATE INDEX waitlist_active_sort_idx ON waitlist_entries (manual_priority DESC, entered_at, id) WHERE removed_at IS NULL;

CREATE TABLE job_notes (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    job_id uuid NOT NULL REFERENCES jobs(id),
    author_user_id uuid NOT NULL REFERENCES users(id),
    body text NOT NULL CHECK (btrim(body) <> '' AND length(body) <= 4000),
    correction_of_id uuid REFERENCES job_notes(id),
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX job_notes_job_idx ON job_notes (job_id, created_at, id);

-- +goose StatementBegin
CREATE FUNCTION prevent_job_note_mutation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'job notes are append-only' USING ERRCODE = '55000';
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER job_notes_no_update BEFORE UPDATE OR DELETE ON job_notes
FOR EACH ROW EXECUTE FUNCTION prevent_job_note_mutation();

-- +goose Down
DROP TRIGGER IF EXISTS job_notes_no_update ON job_notes;
DROP FUNCTION IF EXISTS prevent_job_note_mutation();
DROP TABLE IF EXISTS job_notes;
DROP TABLE IF EXISTS waitlist_entries;
DROP TABLE IF EXISTS jobs;
DROP TABLE IF EXISTS job_number_counters;
DROP TABLE IF EXISTS customers;
