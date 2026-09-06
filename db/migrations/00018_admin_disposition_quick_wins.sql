-- +goose Up
ALTER TABLE jobs
    ADD COLUMN preference_mode text NOT NULL DEFAULT 'window'
        CHECK (preference_mode IN ('fixed', 'window', 'flexible'));

ALTER TABLE waitlist_entries
    ADD COLUMN priority_reason text NOT NULL DEFAULT ''
        CHECK (length(priority_reason) <= 240);

ALTER TABLE waitlist_filter_favorites
    ADD COLUMN incomplete boolean NOT NULL DEFAULT false;

UPDATE waitlist_entries
SET priority_reason = 'Bestehende Priorisierung'
WHERE manual_priority <> 0 AND btrim(priority_reason) = '';

ALTER TABLE waitlist_entries
    ADD CONSTRAINT waitlist_priority_reason_required
        CHECK (manual_priority = 0 OR btrim(priority_reason) <> '');

ALTER TABLE jobs
    ADD CONSTRAINT jobs_fixed_preference_exact_date
        CHECK (preference_mode <> 'fixed' OR
               (preferred_start_date IS NOT NULL AND preferred_start_date = preferred_end_date));

INSERT INTO schema_metadata (key, value)
VALUES ('application_schema_version', '18')
ON CONFLICT (key) DO UPDATE SET value=EXCLUDED.value, updated_at=now();

-- +goose Down
ALTER TABLE jobs DROP CONSTRAINT jobs_fixed_preference_exact_date;
ALTER TABLE waitlist_entries DROP CONSTRAINT waitlist_priority_reason_required;
ALTER TABLE waitlist_filter_favorites DROP COLUMN incomplete;
ALTER TABLE waitlist_entries DROP COLUMN priority_reason;
ALTER TABLE jobs DROP COLUMN preference_mode;

INSERT INTO schema_metadata (key, value)
VALUES ('application_schema_version', '17')
ON CONFLICT (key) DO UPDATE SET value=EXCLUDED.value, updated_at=now();
