-- +goose Up
ALTER TABLE outbox_events DROP CONSTRAINT outbox_events_status_check;
UPDATE outbox_events SET status = CASE status
    WHEN 'processing' THEN 'retry_wait'
    WHEN 'failed' THEN 'dead'
    ELSE status
END;
ALTER TABLE outbox_events
    ADD COLUMN payload_version integer NOT NULL DEFAULT 1 CHECK (payload_version > 0),
    ADD COLUMN max_attempts integer NOT NULL DEFAULT 6 CHECK (max_attempts BETWEEN 1 AND 50),
    ADD COLUMN claimed_by text,
    ADD COLUMN lease_until timestamptz,
    ADD COLUMN updated_at timestamptz NOT NULL DEFAULT now(),
    ADD CONSTRAINT outbox_events_status_check CHECK (status IN ('queued', 'claimed', 'retry_wait', 'processed', 'dead')),
    ADD CONSTRAINT outbox_events_claim_check CHECK (
        (status = 'claimed' AND claimed_by IS NOT NULL AND lease_until IS NOT NULL)
        OR (status <> 'claimed' AND claimed_by IS NULL AND lease_until IS NULL)
    ),
    ADD CONSTRAINT outbox_events_error_code_length CHECK (last_error_code IS NULL OR length(last_error_code) <= 200);

DROP INDEX outbox_events_pending_idx;
CREATE INDEX outbox_events_pending_idx
    ON outbox_events (available_at, created_at, id)
    WHERE status IN ('queued', 'retry_wait', 'claimed');

ALTER TABLE appointments
    ADD COLUMN notification_override_reason text,
    ADD CONSTRAINT appointments_notification_override_reason_check CHECK (
        notification_override_reason IS NULL OR
        (btrim(notification_override_reason) <> '' AND length(notification_override_reason) <= 1000)
    );

CREATE TABLE confirmation_requests (
    id uuid PRIMARY KEY,
    appointment_id uuid NOT NULL REFERENCES appointments(id),
    token_hash bytea NOT NULL UNIQUE CHECK (octet_length(token_hash) = 32),
    form_nonce_hash bytea NOT NULL CHECK (octet_length(form_nonce_hash) = 32),
    token_key_id text NOT NULL CHECK (btrim(token_key_id) <> '' AND length(token_key_id) <= 100),
    token_version integer NOT NULL CHECK (token_version > 0),
    status text NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'revoked')),
    response text CHECK (response IS NULL OR response IN ('confirmed', 'declined', 'callback_requested')),
    responded_at timestamptz,
    expires_at timestamptz NOT NULL,
    revoked_at timestamptz,
    revoke_reason text,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (appointment_id, token_version),
    CHECK ((response IS NULL) = (responded_at IS NULL)),
    CHECK ((status = 'revoked') = (revoked_at IS NOT NULL)),
    CHECK (revoke_reason IS NULL OR (btrim(revoke_reason) <> '' AND length(revoke_reason) <= 500)),
    CHECK (expires_at > created_at)
);

CREATE UNIQUE INDEX confirmation_requests_one_active_idx
    ON confirmation_requests (appointment_id) WHERE status = 'active';
CREATE INDEX confirmation_requests_expiry_idx ON confirmation_requests (expires_at) WHERE status = 'active';

CREATE TABLE notifications (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    appointment_id uuid NOT NULL REFERENCES appointments(id),
    confirmation_request_id uuid NOT NULL REFERENCES confirmation_requests(id),
    channel text NOT NULL CHECK (channel IN ('email', 'sms')),
    recipient_snapshot text NOT NULL CHECK (btrim(recipient_snapshot) <> '' AND length(recipient_snapshot) <= 320),
    template_version integer NOT NULL DEFAULT 1 CHECK (template_version > 0),
    parameters jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(parameters) = 'object'),
    status text NOT NULL DEFAULT 'queued' CHECK (status IN ('queued', 'sending', 'retry_wait', 'sent', 'failed')),
    provider_id text,
    attempt_count integer NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
    max_attempts integer NOT NULL DEFAULT 6 CHECK (max_attempts BETWEEN 1 AND 50),
    available_at timestamptz NOT NULL DEFAULT now(),
    last_error_code text,
    sent_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (confirmation_request_id, channel),
    CHECK ((status = 'sent') = (sent_at IS NOT NULL)),
    CHECK (provider_id IS NULL OR length(provider_id) <= 500),
    CHECK (last_error_code IS NULL OR length(last_error_code) <= 200)
);

CREATE INDEX notifications_appointment_idx ON notifications (appointment_id, created_at DESC);
CREATE INDEX notifications_failed_idx ON notifications (updated_at DESC, id) WHERE status IN ('retry_wait', 'failed');

-- +goose Down
DROP TABLE IF EXISTS notifications;
DROP TABLE IF EXISTS confirmation_requests;
ALTER TABLE appointments DROP COLUMN IF EXISTS notification_override_reason;
DROP INDEX IF EXISTS outbox_events_pending_idx;
ALTER TABLE outbox_events
    DROP CONSTRAINT IF EXISTS outbox_events_error_code_length,
    DROP CONSTRAINT IF EXISTS outbox_events_claim_check,
    DROP CONSTRAINT IF EXISTS outbox_events_status_check;
UPDATE outbox_events SET status = CASE status
    WHEN 'claimed' THEN 'retry_wait'
    WHEN 'dead' THEN 'failed'
    ELSE status
END;
ALTER TABLE outbox_events
    DROP COLUMN IF EXISTS updated_at,
    DROP COLUMN IF EXISTS lease_until,
    DROP COLUMN IF EXISTS claimed_by,
    DROP COLUMN IF EXISTS max_attempts,
    DROP COLUMN IF EXISTS payload_version,
    ADD CONSTRAINT outbox_events_status_check CHECK (status IN ('queued', 'processing', 'retry_wait', 'processed', 'failed'));
CREATE INDEX outbox_events_pending_idx
    ON outbox_events (available_at, created_at, id)
    WHERE status IN ('queued', 'retry_wait');
