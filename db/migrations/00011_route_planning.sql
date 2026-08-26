-- +goose Up
ALTER TABLE jobs
    ADD COLUMN pile_latitude numeric(9,6),
    ADD COLUMN pile_longitude numeric(9,6),
    ADD COLUMN pile_location_source text,
    ADD COLUMN pile_location_updated_at timestamptz,
    ADD CONSTRAINT jobs_pile_coordinates_complete CHECK ((pile_latitude IS NULL) = (pile_longitude IS NULL)),
    ADD CONSTRAINT jobs_pile_latitude_range CHECK (pile_latitude IS NULL OR pile_latitude BETWEEN -90 AND 90),
    ADD CONSTRAINT jobs_pile_longitude_range CHECK (pile_longitude IS NULL OR pile_longitude BETWEEN -180 AND 180),
    ADD CONSTRAINT jobs_pile_location_source_valid CHECK (
        pile_location_source IS NULL OR pile_location_source IN ('map_pin', 'customer_address', 'device_location', 'coordinates')
    ),
    ADD CONSTRAINT jobs_pile_location_metadata_complete CHECK (
        (pile_latitude IS NULL AND pile_location_source IS NULL AND pile_location_updated_at IS NULL)
        OR (pile_latitude IS NOT NULL AND pile_location_source IS NOT NULL AND pile_location_updated_at IS NOT NULL)
    );

CREATE INDEX jobs_routable_status_idx
    ON jobs (workflow_status, received_at, id)
    WHERE archived_at IS NULL AND pile_latitude IS NOT NULL;

CREATE TABLE route_drafts (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    actor_user_id uuid NOT NULL REFERENCES users(id),
    driver_id uuid NOT NULL REFERENCES drivers(id),
    chipper_resource_id uuid NOT NULL REFERENCES resources(id),
    transport_resource_id uuid REFERENCES resources(id),
    departure_at timestamptz NOT NULL,
    start_latitude numeric(9,6) NOT NULL,
    start_longitude numeric(9,6) NOT NULL,
    end_latitude numeric(9,6) NOT NULL,
    end_longitude numeric(9,6) NOT NULL,
    status text NOT NULL DEFAULT 'draft' CHECK (status IN ('draft', 'assigned')),
    routing_source text NOT NULL CHECK (routing_source IN ('osrm', 'haversine', 'unavailable')),
    distance_meters integer NOT NULL DEFAULT 0 CHECK (distance_meters >= 0),
    duration_seconds integer NOT NULL DEFAULT 0 CHECK (duration_seconds >= 0),
    route_geometry jsonb NOT NULL DEFAULT '{"type":"LineString","coordinates":[]}'::jsonb
        CHECK (jsonb_typeof(route_geometry) = 'object'),
    assigned_at timestamptz,
    version integer NOT NULL DEFAULT 1 CHECK (version > 0),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CHECK (start_latitude BETWEEN -90 AND 90),
    CHECK (start_longitude BETWEEN -180 AND 180),
    CHECK (end_latitude BETWEEN -90 AND 90),
    CHECK (end_longitude BETWEEN -180 AND 180),
    CHECK (transport_resource_id IS NULL OR transport_resource_id <> chipper_resource_id),
    CHECK ((status = 'assigned') = (assigned_at IS NOT NULL))
);

CREATE INDEX route_drafts_driver_departure_idx ON route_drafts (driver_id, departure_at DESC, id);
CREATE INDEX route_drafts_status_departure_idx ON route_drafts (status, departure_at DESC, id);

CREATE TABLE route_stops (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    route_draft_id uuid NOT NULL REFERENCES route_drafts(id) ON DELETE CASCADE,
    job_id uuid NOT NULL REFERENCES jobs(id),
    job_version integer NOT NULL CHECK (job_version > 0),
    waitlist_version integer NOT NULL CHECK (waitlist_version > 0),
    position integer NOT NULL CHECK (position > 0),
    travel_distance_meters integer NOT NULL DEFAULT 0 CHECK (travel_distance_meters >= 0),
    travel_duration_seconds integer NOT NULL DEFAULT 0 CHECK (travel_duration_seconds >= 0),
    planned_starts_at timestamptz NOT NULL,
    planned_ends_at timestamptz NOT NULL,
    appointment_id uuid UNIQUE REFERENCES appointments(id),
    created_at timestamptz NOT NULL DEFAULT now(),
    CHECK (planned_ends_at > planned_starts_at),
    UNIQUE (route_draft_id, job_id),
    UNIQUE (route_draft_id, position) DEFERRABLE INITIALLY DEFERRED
);

CREATE INDEX route_stops_route_order_idx ON route_stops (route_draft_id, position, id);

INSERT INTO schema_metadata (key, value)
VALUES ('application_schema_version', '11')
ON CONFLICT (key) DO UPDATE SET value=EXCLUDED.value, updated_at=now();

-- +goose Down
INSERT INTO schema_metadata (key, value)
VALUES ('application_schema_version', '10')
ON CONFLICT (key) DO UPDATE SET value=EXCLUDED.value, updated_at=now();

DROP TABLE IF EXISTS route_stops;
DROP TABLE IF EXISTS route_drafts;
DROP INDEX IF EXISTS jobs_routable_status_idx;
ALTER TABLE jobs
    DROP CONSTRAINT IF EXISTS jobs_pile_location_metadata_complete,
    DROP CONSTRAINT IF EXISTS jobs_pile_location_source_valid,
    DROP CONSTRAINT IF EXISTS jobs_pile_longitude_range,
    DROP CONSTRAINT IF EXISTS jobs_pile_latitude_range,
    DROP CONSTRAINT IF EXISTS jobs_pile_coordinates_complete,
    DROP COLUMN IF EXISTS pile_location_updated_at,
    DROP COLUMN IF EXISTS pile_location_source,
    DROP COLUMN IF EXISTS pile_longitude,
    DROP COLUMN IF EXISTS pile_latitude;
