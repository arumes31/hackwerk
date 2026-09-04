-- +goose Up
ALTER TABLE drivers
    ADD COLUMN is_primary boolean NOT NULL DEFAULT false,
    ADD COLUMN availability_policy text NOT NULL DEFAULT 'legacy_rules';

WITH first_active AS (
    SELECT id
    FROM drivers
    WHERE active
    ORDER BY created_at, id
    LIMIT 1
)
UPDATE drivers
SET is_primary = true
WHERE id = (SELECT id FROM first_active);

ALTER TABLE drivers
    ALTER COLUMN availability_policy SET DEFAULT 'explicit_dates',
    ADD CONSTRAINT drivers_availability_policy_check
        CHECK (availability_policy IN ('legacy_rules', 'assumed_available', 'explicit_dates')),
    ADD CONSTRAINT drivers_primary_active_check
        CHECK (NOT is_primary OR active),
    ADD CONSTRAINT drivers_assumed_available_primary_check
        CHECK (availability_policy <> 'assumed_available' OR (is_primary AND active));

CREATE UNIQUE INDEX drivers_one_primary_idx ON drivers (is_primary) WHERE is_primary;

INSERT INTO schema_metadata (key, value)
VALUES ('application_schema_version', '20')
ON CONFLICT (key) DO UPDATE SET value=EXCLUDED.value, updated_at=now();

-- +goose Down
DROP INDEX IF EXISTS drivers_one_primary_idx;
ALTER TABLE drivers
    DROP CONSTRAINT IF EXISTS drivers_assumed_available_primary_check,
    DROP CONSTRAINT IF EXISTS drivers_primary_active_check,
    DROP CONSTRAINT IF EXISTS drivers_availability_policy_check,
    DROP COLUMN availability_policy,
    DROP COLUMN is_primary;

INSERT INTO schema_metadata (key, value)
VALUES ('application_schema_version', '19')
ON CONFLICT (key) DO UPDATE SET value=EXCLUDED.value, updated_at=now();
