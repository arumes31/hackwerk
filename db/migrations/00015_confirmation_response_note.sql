-- +goose Up
ALTER TABLE confirmation_requests
    ADD COLUMN response_note text,
    ADD CONSTRAINT confirmation_requests_response_note_check CHECK (
        response_note IS NULL OR (btrim(response_note) <> '' AND length(response_note) <= 500)
    );

-- +goose Down
ALTER TABLE confirmation_requests
    DROP CONSTRAINT IF EXISTS confirmation_requests_response_note_check,
    DROP COLUMN IF EXISTS response_note;
