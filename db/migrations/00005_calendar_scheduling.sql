-- +goose Up
CREATE TABLE appointments (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    job_id uuid NOT NULL REFERENCES jobs(id),
    lifecycle_status text NOT NULL DEFAULT 'draft'
        CHECK (lifecycle_status IN ('draft', 'proposal', 'fixed', 'cancelled', 'completed')),
    confirmation_status text NOT NULL DEFAULT 'not_requested'
        CHECK (confirmation_status IN ('not_requested', 'pending', 'confirmed', 'declined', 'callback_requested')),
    starts_at timestamptz NOT NULL,
    ends_at timestamptz NOT NULL,
    buffer_before_minutes integer NOT NULL DEFAULT 0 CHECK (buffer_before_minutes BETWEEN 0 AND 1440),
    buffer_after_minutes integer NOT NULL DEFAULT 0 CHECK (buffer_after_minutes BETWEEN 0 AND 1440),
    availability_override_reason text,
    fixed_by_user_id uuid REFERENCES users(id),
    fixed_at timestamptz,
    cancelled_by_user_id uuid REFERENCES users(id),
    cancelled_at timestamptz,
    cancellation_reason text,
    completed_by_user_id uuid REFERENCES users(id),
    completed_at timestamptz,
    completion_override_reason text,
    version integer NOT NULL DEFAULT 1 CHECK (version > 0),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CHECK (ends_at > starts_at),
    CHECK (ends_at - starts_at <= interval '7 days'),
    CHECK (availability_override_reason IS NULL OR (btrim(availability_override_reason) <> '' AND length(availability_override_reason) <= 1000)),
    CHECK (lifecycle_status <> 'fixed' OR (fixed_at IS NOT NULL AND fixed_by_user_id IS NOT NULL)),
    CHECK ((lifecycle_status = 'cancelled') = (cancelled_at IS NOT NULL AND cancelled_by_user_id IS NOT NULL)),
    CHECK (cancellation_reason IS NULL OR (btrim(cancellation_reason) <> '' AND length(cancellation_reason) <= 1000)),
    CHECK ((lifecycle_status = 'completed') = (completed_at IS NOT NULL AND completed_by_user_id IS NOT NULL)),
    CHECK (completion_override_reason IS NULL OR (btrim(completion_override_reason) <> '' AND length(completion_override_reason) <= 1000))
);

CREATE UNIQUE INDEX appointments_one_active_job_idx
    ON appointments (job_id)
    WHERE lifecycle_status IN ('draft', 'proposal', 'fixed');
CREATE INDEX appointments_calendar_range_idx ON appointments (starts_at, ends_at, id);

CREATE TABLE appointment_drivers (
    appointment_id uuid NOT NULL REFERENCES appointments(id) ON DELETE CASCADE,
    driver_id uuid NOT NULL REFERENCES drivers(id),
    is_primary boolean NOT NULL DEFAULT false,
    active boolean NOT NULL DEFAULT false,
    reserved_starts_at timestamptz NOT NULL,
    reserved_ends_at timestamptz NOT NULL,
    reserved_range tstzrange GENERATED ALWAYS AS
        (tstzrange(reserved_starts_at, reserved_ends_at, '[)')) STORED,
    PRIMARY KEY (appointment_id, driver_id),
    CHECK (reserved_ends_at > reserved_starts_at),
    EXCLUDE USING gist (driver_id WITH =, reserved_range WITH &&) WHERE (active)
);

CREATE UNIQUE INDEX appointment_drivers_one_primary_idx
    ON appointment_drivers (appointment_id) WHERE is_primary;
CREATE INDEX appointment_drivers_driver_idx ON appointment_drivers (driver_id, appointment_id);

CREATE TABLE appointment_resources (
    appointment_id uuid NOT NULL REFERENCES appointments(id) ON DELETE CASCADE,
    resource_id uuid NOT NULL REFERENCES resources(id),
    purpose text NOT NULL CHECK (purpose IN ('chipping', 'transport', 'trailer', 'other')),
    exclusive boolean NOT NULL,
    active boolean NOT NULL DEFAULT false,
    reserved_starts_at timestamptz NOT NULL,
    reserved_ends_at timestamptz NOT NULL,
    reserved_range tstzrange GENERATED ALWAYS AS
        (tstzrange(reserved_starts_at, reserved_ends_at, '[)')) STORED,
    PRIMARY KEY (appointment_id, resource_id),
    CHECK (reserved_ends_at > reserved_starts_at),
    EXCLUDE USING gist (resource_id WITH =, reserved_range WITH &&) WHERE (active AND exclusive)
);

CREATE INDEX appointment_resources_resource_idx ON appointment_resources (resource_id, appointment_id);

-- Reservation rows deliberately duplicate the buffered range so PostgreSQL can
-- enforce exclusion constraints. This trigger prevents an adapter from writing
-- a range or activity flag inconsistent with its appointment/resource.
-- +goose StatementBegin
CREATE FUNCTION validate_appointment_reservation() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
    expected_start timestamptz;
    expected_end timestamptz;
    expected_active boolean;
    resource_exclusive boolean;
BEGIN
    SELECT starts_at - make_interval(mins => buffer_before_minutes),
           ends_at + make_interval(mins => buffer_after_minutes),
           lifecycle_status IN ('proposal', 'fixed')
      INTO expected_start, expected_end, expected_active
      FROM appointments WHERE id = NEW.appointment_id;
    IF NOT FOUND OR NEW.reserved_starts_at <> expected_start OR NEW.reserved_ends_at <> expected_end OR NEW.active <> expected_active THEN
        RAISE EXCEPTION 'inconsistent appointment reservation' USING ERRCODE = '23514';
    END IF;
    IF TG_TABLE_NAME = 'appointment_resources' THEN
        SELECT exclusive INTO resource_exclusive FROM resources WHERE id = NEW.resource_id;
        IF NOT FOUND OR NEW.exclusive <> resource_exclusive THEN
            RAISE EXCEPTION 'inconsistent resource reservation' USING ERRCODE = '23514';
        END IF;
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER appointment_drivers_validate
BEFORE INSERT OR UPDATE ON appointment_drivers
FOR EACH ROW EXECUTE FUNCTION validate_appointment_reservation();

CREATE TRIGGER appointment_resources_validate
BEFORE INSERT OR UPDATE ON appointment_resources
FOR EACH ROW EXECUTE FUNCTION validate_appointment_reservation();

CREATE TABLE outbox_events (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    event_type text NOT NULL CHECK (btrim(event_type) <> '' AND length(event_type) <= 200),
    aggregate_type text NOT NULL CHECK (btrim(aggregate_type) <> '' AND length(aggregate_type) <= 100),
    aggregate_id uuid NOT NULL,
    payload jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(payload) = 'object'),
    idempotency_key text NOT NULL UNIQUE CHECK (btrim(idempotency_key) <> '' AND length(idempotency_key) <= 300),
    status text NOT NULL DEFAULT 'queued' CHECK (status IN ('queued', 'processing', 'retry_wait', 'processed', 'failed')),
    attempt_count integer NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
    available_at timestamptz NOT NULL DEFAULT now(),
    locked_at timestamptz,
    processed_at timestamptz,
    last_error_code text,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX outbox_events_pending_idx
    ON outbox_events (available_at, created_at, id)
    WHERE status IN ('queued', 'retry_wait');

-- +goose Down
DROP TABLE IF EXISTS outbox_events;
DROP TRIGGER IF EXISTS appointment_resources_validate ON appointment_resources;
DROP TRIGGER IF EXISTS appointment_drivers_validate ON appointment_drivers;
DROP FUNCTION IF EXISTS validate_appointment_reservation();
DROP TABLE IF EXISTS appointment_resources;
DROP TABLE IF EXISTS appointment_drivers;
DROP TABLE IF EXISTS appointments;
