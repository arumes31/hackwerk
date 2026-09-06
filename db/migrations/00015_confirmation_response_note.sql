-- +goose Up
ALTER TABLE confirmation_requests
    ADD COLUMN response_note text,
    ADD CONSTRAINT confirmation_requests_response_note_check CHECK (
        response_note IS NULL OR (btrim(response_note) <> '' AND length(response_note) <= 500)
    );

INSERT INTO schema_metadata (key, value)
VALUES ('application_schema_version', '15')
ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value;

-- +goose Down
ALTER TABLE confirmation_requests
    DROP CONSTRAINT IF EXISTS confirmation_requests_response_note_check,
    DROP COLUMN IF EXISTS response_note;

INSERT INTO schema_metadata (key, value)
VALUES ('application_schema_version', '14')
ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value;
