-- +goose Up
ALTER TABLE route_drafts
    ALTER COLUMN chipper_resource_id DROP NOT NULL;

INSERT INTO schema_metadata (key, value)
VALUES ('application_schema_version', '22')
ON CONFLICT (key) DO UPDATE SET value=EXCLUDED.value, updated_at=now();

-- +goose Down
-- This intentionally fails when routes without a chipper exist. A rollback must
-- resolve those drafts explicitly instead of silently assigning a resource.
ALTER TABLE route_drafts
    ALTER COLUMN chipper_resource_id SET NOT NULL;

INSERT INTO schema_metadata (key, value)
VALUES ('application_schema_version', '21')
ON CONFLICT (key) DO UPDATE SET value=EXCLUDED.value, updated_at=now();
