-- +goose Up
ALTER TABLE waitlist_filter_favorites
    DROP CONSTRAINT waitlist_filter_favorites_sort_key_check,
    ADD COLUMN duration_group text NOT NULL DEFAULT ''
        CHECK (duration_group IN ('', 'short', 'medium', 'long')),
    ADD COLUMN overdue boolean NOT NULL DEFAULT false,
    ADD COLUMN unassigned boolean NOT NULL DEFAULT false,
    ADD COLUMN transport_pending boolean NOT NULL DEFAULT false,
    ADD CONSTRAINT waitlist_filter_favorites_sort_key_check
        CHECK (sort_key IN ('entered', 'preferred', 'urgency', 'volume', 'region', 'customer', 'workflow', 'updated', 'duration'));

INSERT INTO schema_metadata (key, value)
VALUES ('application_schema_version', '16')
ON CONFLICT (key) DO UPDATE SET value=EXCLUDED.value, updated_at=now();

-- +goose Down
UPDATE waitlist_filter_favorites SET sort_key='entered' WHERE sort_key='duration';

ALTER TABLE waitlist_filter_favorites
    DROP CONSTRAINT waitlist_filter_favorites_sort_key_check,
    ADD CONSTRAINT waitlist_filter_favorites_sort_key_check
        CHECK (sort_key IN ('entered', 'preferred', 'urgency', 'volume', 'region', 'customer', 'workflow', 'updated')),
    DROP COLUMN transport_pending,
    DROP COLUMN unassigned,
    DROP COLUMN overdue,
    DROP COLUMN duration_group;

INSERT INTO schema_metadata (key, value)
VALUES ('application_schema_version', '15')
ON CONFLICT (key) DO UPDATE SET value=EXCLUDED.value, updated_at=now();
