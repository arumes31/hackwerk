-- +goose Up
CREATE TABLE recent_records (
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    customer_id uuid REFERENCES customers(id) ON DELETE CASCADE,
    job_id uuid REFERENCES jobs(id) ON DELETE CASCADE,
    viewed_at timestamptz NOT NULL DEFAULT now(),
    CHECK (num_nonnulls(customer_id, job_id) = 1)
);

CREATE UNIQUE INDEX recent_records_user_customer_uidx
    ON recent_records (user_id, customer_id) WHERE customer_id IS NOT NULL;
CREATE UNIQUE INDEX recent_records_user_job_uidx
    ON recent_records (user_id, job_id) WHERE job_id IS NOT NULL;
CREATE INDEX recent_records_user_viewed_idx
    ON recent_records (user_id, viewed_at DESC);

CREATE TABLE waitlist_filter_favorites (
    id uuid PRIMARY KEY,
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name text NOT NULL CHECK (char_length(btrim(name)) BETWEEN 1 AND 60),
    job_type text NOT NULL DEFAULT '' CHECK (job_type IN ('', 'chipping_only', 'chipping_with_transport')),
    region text NOT NULL DEFAULT '' CHECK (char_length(region) <= 100),
    urgency text NOT NULL DEFAULT '' CHECK (urgency IN ('', 'low', 'normal', 'high', 'urgent')),
    preferred_month text NOT NULL DEFAULT '' CHECK (preferred_month = '' OR preferred_month ~ '^[0-9]{4}-(0[1-9]|1[0-2])$'),
    workflow text NOT NULL DEFAULT '' CHECK (workflow IN ('', 'unplanned', 'proposal', 'scheduled')),
    missing_location boolean NOT NULL DEFAULT false,
    duration_issue boolean NOT NULL DEFAULT false,
    sort_key text NOT NULL DEFAULT 'entered' CHECK (sort_key IN ('entered', 'preferred', 'urgency', 'volume', 'region', 'customer', 'workflow', 'updated')),
    sort_direction text NOT NULL DEFAULT 'asc' CHECK (sort_direction IN ('asc', 'desc')),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX waitlist_filter_favorites_user_name_uidx
    ON waitlist_filter_favorites (user_id, lower(name));
CREATE INDEX waitlist_filter_favorites_user_updated_idx
    ON waitlist_filter_favorites (user_id, updated_at DESC, id);

ALTER TABLE notifications
    ADD COLUMN reviewed_at timestamptz,
    ADD COLUMN reviewed_by_user_id uuid REFERENCES users(id),
    ADD CONSTRAINT notifications_review_check CHECK (
        (reviewed_at IS NULL) = (reviewed_by_user_id IS NULL)
    );

CREATE INDEX notifications_open_review_idx
    ON notifications (reviewed_at, updated_at DESC, id)
    WHERE status IN ('retry_wait', 'failed');

ALTER TABLE job_notes
    ADD COLUMN idempotency_key text
        CHECK (idempotency_key IS NULL OR char_length(idempotency_key) BETWEEN 1 AND 200);

CREATE UNIQUE INDEX job_notes_idempotency_uidx
    ON job_notes (job_id, author_user_id, idempotency_key)
    WHERE idempotency_key IS NOT NULL;

INSERT INTO schema_metadata (key, value)
VALUES ('application_schema_version', '12')
ON CONFLICT (key) DO UPDATE SET value=EXCLUDED.value, updated_at=now();

-- +goose Down
INSERT INTO schema_metadata (key, value)
VALUES ('application_schema_version', '11')
ON CONFLICT (key) DO UPDATE SET value=EXCLUDED.value, updated_at=now();

DROP INDEX IF EXISTS job_notes_idempotency_uidx;
ALTER TABLE job_notes DROP COLUMN IF EXISTS idempotency_key;
DROP INDEX IF EXISTS notifications_open_review_idx;
ALTER TABLE notifications
    DROP CONSTRAINT IF EXISTS notifications_review_check,
    DROP COLUMN IF EXISTS reviewed_by_user_id,
    DROP COLUMN IF EXISTS reviewed_at;
DROP TABLE IF EXISTS waitlist_filter_favorites;
DROP TABLE IF EXISTS recent_records;
