-- name: InsertVoiceDraft :one
INSERT INTO voice_drafts (owner_user_id, status, expires_at)
VALUES (sqlc.arg(owner_user_id)::uuid, 'transcribing', sqlc.arg(expires_at))
RETURNING id::text, version;

-- name: InsertVoiceRecording :one
WITH draft AS (
    INSERT INTO voice_drafts (owner_user_id, status, expires_at)
    VALUES (sqlc.arg(owner_user_id)::uuid, 'recorded', sqlc.arg(draft_expires_at)::timestamptz)
    RETURNING id, version
)
INSERT INTO voice_recordings (
    draft_id, owner_user_id, content_type, audio_bytes, byte_size, duration_ms,
    recorded_at, expires_at, available_at, upload_key_hash
)
SELECT id, sqlc.arg(owner_user_id)::uuid, sqlc.arg(content_type), sqlc.arg(audio_bytes),
       sqlc.arg(byte_size), sqlc.arg(duration_ms), sqlc.arg(recorded_at)::timestamptz,
       sqlc.arg(recording_expires_at)::timestamptz, now(), sqlc.arg(upload_key_hash)::bytea
FROM draft
RETURNING draft_id::text;

-- name: LockVoiceUploadKey :exec
SELECT pg_advisory_xact_lock(hashtextextended(sqlc.arg(owner_user_id)::text || encode(sqlc.arg(upload_key_hash)::bytea, 'hex'), 0));

-- name: GetVoiceDraftByUploadKey :one
SELECT draft.id::text, draft.status, draft.version, draft.expires_at
FROM voice_recordings recording
JOIN voice_drafts draft ON draft.id=recording.draft_id
WHERE recording.owner_user_id=sqlc.arg(owner_user_id)::uuid
  AND recording.upload_key_hash=sqlc.arg(upload_key_hash)::bytea;

-- name: ClaimVoiceRecording :one
WITH candidate AS (
    SELECT recording.id
    FROM voice_recordings recording
    JOIN voice_drafts draft ON draft.id = recording.draft_id
    WHERE recording.expires_at > sqlc.arg(now_utc)::timestamptz
      AND draft.expires_at > sqlc.arg(now_utc)::timestamptz
      AND recording.attempt_count < recording.max_attempts
      AND (
          (draft.status = 'recorded' AND recording.available_at <= sqlc.arg(now_utc)::timestamptz AND recording.claimed_by IS NULL)
          OR
          (draft.status = 'transcribing' AND recording.lease_until <= sqlc.arg(now_utc)::timestamptz)
      )
    ORDER BY recording.available_at, recording.created_at, recording.id
    FOR UPDATE OF recording, draft SKIP LOCKED
    LIMIT 1
), claimed AS (
    UPDATE voice_recordings recording
    SET claimed_by = sqlc.arg(worker_id), lease_until = sqlc.arg(lease_until)::timestamptz,
        attempt_count = recording.attempt_count + 1, failure_code = '', updated_at = sqlc.arg(now_utc)::timestamptz
    FROM candidate
    WHERE recording.id = candidate.id
    RETURNING recording.id, recording.draft_id, recording.owner_user_id, recording.content_type,
              recording.audio_bytes, recording.byte_size, recording.duration_ms, recording.recorded_at,
              recording.attempt_count, recording.max_attempts
)
UPDATE voice_drafts draft
SET status = 'transcribing', retry_count = claimed.attempt_count,
    version = version + CASE WHEN draft.status = 'recorded' THEN 1 ELSE 0 END,
    updated_at = sqlc.arg(now_utc)::timestamptz
FROM claimed
WHERE draft.id = claimed.draft_id
RETURNING claimed.id::text AS recording_id, claimed.draft_id::text, claimed.owner_user_id::text,
          claimed.content_type, claimed.audio_bytes, claimed.byte_size, claimed.duration_ms,
          claimed.recorded_at, claimed.attempt_count, claimed.max_attempts;

-- name: CompleteClaimedVoiceRecording :execrows
WITH released AS (
    UPDATE voice_recordings
    SET claimed_by = NULL, lease_until = NULL, failure_code = '', updated_at = sqlc.arg(now_utc)::timestamptz
    WHERE id = sqlc.arg(recording_id)::uuid AND claimed_by = sqlc.arg(worker_id)
      AND lease_until > sqlc.arg(now_utc)::timestamptz
    RETURNING draft_id
)
UPDATE voice_drafts draft SET
    status = 'needs_review', transcript = sqlc.arg(transcript),
    extracted_fields = sqlc.arg(extracted_fields), warnings = sqlc.arg(warnings),
    overall_confidence = sqlc.arg(overall_confidence)::numeric,
    provider_name = sqlc.arg(provider_name), provider_version = sqlc.arg(provider_version),
    parser_version = sqlc.arg(parser_version), failure_code = '',
    version = version + 1, updated_at = sqlc.arg(now_utc)::timestamptz
FROM released
WHERE draft.id = released.draft_id AND draft.status = 'transcribing' AND draft.expires_at > sqlc.arg(now_utc)::timestamptz;

-- name: FailClaimedVoiceRecording :one
WITH released AS (
    UPDATE voice_recordings recording
    SET claimed_by = NULL, lease_until = NULL,
        available_at = CASE WHEN sqlc.arg(retry)::boolean THEN sqlc.arg(available_at)::timestamptz ELSE recording.available_at END,
        attempt_count = CASE WHEN sqlc.arg(retry)::boolean THEN recording.attempt_count ELSE recording.max_attempts END,
        failure_code = sqlc.arg(failure_code), updated_at = sqlc.arg(now_utc)::timestamptz
    WHERE recording.id = sqlc.arg(recording_id)::uuid AND recording.claimed_by = sqlc.arg(worker_id)
      AND recording.lease_until > sqlc.arg(now_utc)::timestamptz
    RETURNING recording.draft_id, recording.attempt_count, recording.max_attempts
)
UPDATE voice_drafts draft
SET status = CASE WHEN sqlc.arg(retry)::boolean AND released.attempt_count < released.max_attempts THEN 'recorded' ELSE 'failed' END,
    failure_code = sqlc.arg(failure_code), retry_count = released.attempt_count,
    version = version + 1, updated_at = sqlc.arg(now_utc)::timestamptz
FROM released
WHERE draft.id = released.draft_id AND draft.status = 'transcribing'
RETURNING draft.status;

-- name: RetryFailedVoiceRecording :one
WITH queued AS (
    UPDATE voice_recordings recording
    SET attempt_count=0, manual_retry_count=manual_retry_count+1,
        claimed_by=NULL, lease_until=NULL, available_at=sqlc.arg(now_utc)::timestamptz,
        failure_code='', updated_at=sqlc.arg(now_utc)::timestamptz
    FROM voice_drafts draft
    WHERE recording.draft_id=draft.id
      AND draft.id=sqlc.arg(draft_id)::uuid
      AND draft.owner_user_id=sqlc.arg(owner_user_id)::uuid
      AND draft.status='failed' AND draft.version=sqlc.arg(expected_version)
      AND draft.expires_at>sqlc.arg(now_utc)::timestamptz
      AND recording.expires_at>sqlc.arg(now_utc)::timestamptz
      AND recording.manual_retry_count<1
    RETURNING recording.draft_id, recording.manual_retry_count
)
UPDATE voice_drafts draft
SET status='recorded', failure_code='', retry_count=0,
    version=version+1, updated_at=sqlc.arg(now_utc)::timestamptz
FROM queued
WHERE draft.id=queued.draft_id
RETURNING draft.id::text, draft.owner_user_id::text, draft.status, draft.retry_count,
          queued.manual_retry_count, draft.version, draft.created_at, draft.updated_at, draft.expires_at;

-- name: ListVoiceRecordingsForAdmin :many
SELECT recording.id::text, recording.draft_id::text, recording.content_type, recording.byte_size,
       recording.duration_ms, recording.recorded_at, recording.expires_at, recording.created_at,
       draft.status, owner.display_name AS owner_display_name
FROM voice_recordings recording
JOIN voice_drafts draft ON draft.id = recording.draft_id
JOIN users owner ON owner.id = recording.owner_user_id
WHERE recording.expires_at > now()
ORDER BY recording.created_at DESC, recording.id DESC
LIMIT sqlc.arg(result_limit)
OFFSET sqlc.arg(result_offset);

-- name: GetVoiceRecordingAudio :one
SELECT id::text, content_type, audio_bytes, byte_size, duration_ms, recorded_at, expires_at
FROM voice_recordings
WHERE id = sqlc.arg(id)::uuid AND expires_at > now();

-- name: CleanupExpiredVoiceRecordings :execrows
WITH expired AS (
    SELECT id
    FROM voice_recordings
    WHERE expires_at <= sqlc.arg(now_utc)::timestamptz
    ORDER BY expires_at, id
    LIMIT 100
    FOR UPDATE SKIP LOCKED
)
DELETE FROM voice_recordings recording
USING expired
WHERE recording.id = expired.id;

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
	   COALESCE((SELECT recording.manual_retry_count FROM voice_recordings recording WHERE recording.draft_id=voice_drafts.id), 0)::int AS manual_retry_count,
       COALESCE(committed_customer_id::text, '')::text AS committed_customer_id,
       COALESCE(committed_job_id::text, '')::text AS committed_job_id,
       COALESCE(committed_waitlist_id::text, '')::text AS committed_waitlist_id,
       COALESCE((SELECT job_number FROM jobs WHERE id = voice_drafts.committed_job_id), '')::text AS committed_job_number,
       committed_at, expires_at, version, created_at, updated_at
FROM voice_drafts
WHERE id = sqlc.arg(id)::uuid AND owner_user_id = sqlc.arg(owner_user_id)::uuid;

-- name: LockVoiceDraftForOwner :one
SELECT id::text, status, expires_at, version,
       COALESCE(committed_customer_id::text, '')::text AS committed_customer_id,
       COALESCE(committed_job_id::text, '')::text AS committed_job_id,
       COALESCE(committed_waitlist_id::text, '')::text AS committed_waitlist_id,
       COALESCE((SELECT job_number FROM jobs WHERE id = voice_drafts.committed_job_id), '')::text AS committed_job_number
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
