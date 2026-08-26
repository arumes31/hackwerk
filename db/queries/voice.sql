-- name: InsertVoiceDraft :one
INSERT INTO voice_drafts (owner_user_id, status, expires_at)
VALUES (sqlc.arg(owner_user_id)::uuid, 'transcribing', sqlc.arg(expires_at))
RETURNING id::text, version;

-- name: CompleteVoiceDraft :execrows
UPDATE voice_drafts SET
    status = 'needs_review', transcript = sqlc.arg(transcript),
    extracted_fields = sqlc.arg(extracted_fields), warnings = sqlc.arg(warnings),
    overall_confidence = sqlc.arg(overall_confidence)::numeric,
    provider_name = sqlc.arg(provider_name), provider_version = sqlc.arg(provider_version),
    parser_version = sqlc.arg(parser_version), failure_code = '',
    version = version + 1, updated_at = now()
WHERE id = sqlc.arg(id)::uuid AND owner_user_id = sqlc.arg(owner_user_id)::uuid
  AND status = 'transcribing' AND expires_at > now();

-- name: FailVoiceDraft :execrows
UPDATE voice_drafts SET status = 'failed', failure_code = sqlc.arg(failure_code),
    retry_count = LEAST(retry_count + 1, 3), version = version + 1, updated_at = now()
WHERE id = sqlc.arg(id)::uuid AND owner_user_id = sqlc.arg(owner_user_id)::uuid
  AND status = 'transcribing' AND expires_at > now();

-- name: GetVoiceDraftForOwner :one
SELECT id::text, owner_user_id::text, status, COALESCE(transcript, '')::text AS transcript,
       extracted_fields, warnings, COALESCE(overall_confidence::text, '')::text AS overall_confidence,
       provider_name, provider_version, parser_version, failure_code, retry_count,
       COALESCE(committed_customer_id::text, '')::text AS committed_customer_id,
       COALESCE(committed_job_id::text, '')::text AS committed_job_id,
       COALESCE(committed_waitlist_id::text, '')::text AS committed_waitlist_id,
       committed_at, expires_at, version, created_at, updated_at
FROM voice_drafts
WHERE id = sqlc.arg(id)::uuid AND owner_user_id = sqlc.arg(owner_user_id)::uuid;

-- name: LockVoiceDraftForOwner :one
SELECT id::text, status, expires_at, version,
       COALESCE(committed_customer_id::text, '')::text AS committed_customer_id,
       COALESCE(committed_job_id::text, '')::text AS committed_job_id,
       COALESCE(committed_waitlist_id::text, '')::text AS committed_waitlist_id
FROM voice_drafts
WHERE id = sqlc.arg(id)::uuid AND owner_user_id = sqlc.arg(owner_user_id)::uuid
FOR UPDATE;

-- name: CommitVoiceDraft :execrows
UPDATE voice_drafts SET status = 'committed', committed_customer_id = sqlc.arg(customer_id)::uuid,
    committed_job_id = sqlc.arg(job_id)::uuid, committed_waitlist_id = sqlc.arg(waitlist_id)::uuid,
    transcript = NULL, extracted_fields = '{}'::jsonb, warnings = '{}', failure_code = '',
    committed_at = now(), version = version + 1, updated_at = now()
WHERE id = sqlc.arg(id)::uuid AND owner_user_id = sqlc.arg(owner_user_id)::uuid
  AND status = 'needs_review' AND version = sqlc.arg(expected_version) AND expires_at > now();

-- name: ExpireVoiceDraft :execrows
UPDATE voice_drafts SET status = 'expired', transcript = NULL, extracted_fields = '{}'::jsonb,
    warnings = '{}', failure_code = '', version = version + 1, updated_at = now()
WHERE id = sqlc.arg(id)::uuid AND owner_user_id = sqlc.arg(owner_user_id)::uuid
  AND status IN ('needs_review', 'failed', 'transcribing', 'recorded');

-- name: CleanupExpiredVoiceDrafts :execrows
UPDATE voice_drafts SET status = 'expired', transcript = NULL, extracted_fields = '{}'::jsonb,
    warnings = '{}', failure_code = '', version = version + 1, updated_at = now()
WHERE expires_at <= now() AND status IN ('needs_review', 'failed', 'transcribing', 'recorded');
