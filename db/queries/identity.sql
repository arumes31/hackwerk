-- name: FindUserByUsername :one
SELECT u.id::text AS id, u.username::text AS username, u.display_name,
       COALESCE(u.email::text, '')::text AS email, u.role, u.password_hash,
       u.must_change_password, u.active, u.version,
       COALESCE(d.id::text, '')::text AS driver_id,
       u.salutation, u.work_phone_raw, u.work_phone_normalized,
       u.email_verified_at, u.webauthn_user_handle,
       EXISTS (SELECT 1 FROM user_totp_credentials t WHERE t.user_id=u.id AND t.enabled_at IS NOT NULL) AS totp_enabled,
       EXISTS (SELECT 1 FROM user_webauthn_credentials w WHERE w.user_id=u.id) AS passkey_enabled
FROM users u
LEFT JOIN drivers d ON d.user_id = u.id
WHERE u.username = sqlc.arg(username);

-- name: FindUserByID :one
SELECT u.id::text, u.username::text, u.display_name, COALESCE(u.email::text, '')::text AS email,
       u.role, u.password_hash, u.must_change_password, u.active, u.version,
       COALESCE(d.id::text, '')::text AS driver_id,
       u.salutation, u.work_phone_raw, u.work_phone_normalized,
       u.email_verified_at, u.webauthn_user_handle,
       EXISTS (SELECT 1 FROM user_totp_credentials t WHERE t.user_id=u.id AND t.enabled_at IS NOT NULL) AS totp_enabled,
       EXISTS (SELECT 1 FROM user_webauthn_credentials w WHERE w.user_id=u.id) AS passkey_enabled
FROM users u
LEFT JOIN drivers d ON d.user_id = u.id
WHERE u.id = sqlc.arg(id)::uuid;

-- name: ListUsers :many
SELECT u.id::text, u.username::text, u.display_name, COALESCE(u.email::text, '')::text AS email,
       u.role, u.must_change_password, u.active, u.last_login_at, u.version,
       COALESCE(d.id::text, '')::text AS driver_id
FROM users u
LEFT JOIN drivers d ON d.user_id = u.id
ORDER BY lower(u.username::text);

-- name: InsertUser :one
INSERT INTO users (username, display_name, email, email_verified_at, role, password_hash, must_change_password)
VALUES (sqlc.arg(username), sqlc.arg(display_name), NULLIF(sqlc.arg(email)::text, '')::citext,
        CASE WHEN sqlc.arg(email)::text = '' THEN NULL ELSE now() END,
        sqlc.arg(role), sqlc.arg(password_hash), sqlc.arg(must_change_password))
RETURNING id::text;

-- name: InsertDriver :one
INSERT INTO drivers (user_id, display_name, email)
VALUES (NULLIF(sqlc.arg(user_id)::text, '')::uuid, sqlc.arg(display_name), NULLIF(sqlc.arg(email)::text, '')::citext)
RETURNING id::text;

-- name: UpdatePassword :execrows
UPDATE users
SET password_hash = sqlc.arg(password_hash), must_change_password = sqlc.arg(must_change_password),
    version = version + 1, updated_at = now()
WHERE id = sqlc.arg(id)::uuid AND version = sqlc.arg(expected_version);

-- name: MarkLogin :exec
UPDATE users SET last_login_at = now(), updated_at = now() WHERE id = sqlc.arg(id)::uuid;

-- name: CountActiveAdmins :one
SELECT count(*) FROM users WHERE active AND role = 'admin';

-- name: UpdateUserAccess :execrows
UPDATE users
SET role = sqlc.arg(role), active = sqlc.arg(active), version = version + 1, updated_at = now()
WHERE id = sqlc.arg(id)::uuid AND version = sqlc.arg(expected_version);

-- name: UpdateUserDetails :execrows
UPDATE users
SET username = sqlc.arg(username), display_name = sqlc.arg(display_name),
    email = NULLIF(sqlc.arg(email)::text, '')::citext,
    email_verified_at = CASE WHEN sqlc.arg(email)::text = '' THEN NULL ELSE now() END,
    version = version + 1, updated_at = now()
WHERE id = sqlc.arg(id)::uuid AND version = sqlc.arg(expected_version);

-- name: RevokeUserSessions :exec
UPDATE sessions SET revoked_at = COALESCE(revoked_at, now())
WHERE user_id = sqlc.arg(user_id)::uuid AND revoked_at IS NULL;

-- name: InsertSession :one
INSERT INTO sessions (user_id, token_hash, csrf_token_hash, idle_expires_at, absolute_expires_at, device_label)
VALUES (sqlc.arg(user_id)::uuid, sqlc.arg(token_hash), sqlc.arg(csrf_token_hash), sqlc.arg(idle_expires_at), sqlc.arg(absolute_expires_at), sqlc.arg(device_label))
RETURNING id::text;

-- name: FindSession :one
SELECT s.id::text, s.user_id::text, s.csrf_token_hash, s.idle_expires_at,
       s.absolute_expires_at, s.revoked_at, s.created_at, s.last_used_at, s.device_label,
       u.username::text, u.display_name,
       u.role, u.must_change_password, u.active, COALESCE(d.id::text, '')::text AS driver_id, u.version
FROM sessions s
JOIN users u ON u.id = s.user_id
LEFT JOIN drivers d ON d.user_id = u.id
WHERE s.token_hash = sqlc.arg(token_hash);

-- name: TouchSession :exec
UPDATE sessions
SET last_used_at = now(), idle_expires_at = LEAST(sqlc.arg(idle_expires_at), absolute_expires_at)
WHERE id = sqlc.arg(id)::uuid AND revoked_at IS NULL;

-- name: RevokeSession :exec
UPDATE sessions SET revoked_at = COALESCE(revoked_at, now()) WHERE token_hash = sqlc.arg(token_hash);

-- name: DeleteExpiredSessions :execrows
DELETE FROM sessions WHERE revoked_at IS NOT NULL OR idle_expires_at <= now() OR absolute_expires_at <= now();

-- name: FindRateLimit :one
SELECT window_started_at, failure_count FROM auth_rate_limits WHERE key_hash = sqlc.arg(key_hash);

-- name: RecordLoginFailure :exec
INSERT INTO auth_rate_limits (key_hash, window_started_at, failure_count)
VALUES (sqlc.arg(key_hash), now(), 1)
ON CONFLICT (key_hash) DO UPDATE
SET failure_count = CASE WHEN auth_rate_limits.window_started_at < now() - interval '1 minute' THEN 1 ELSE auth_rate_limits.failure_count + 1 END,
    window_started_at = CASE WHEN auth_rate_limits.window_started_at < now() - interval '1 minute' THEN now() ELSE auth_rate_limits.window_started_at END,
    updated_at = now();

-- name: ClearLoginFailures :exec
DELETE FROM auth_rate_limits WHERE key_hash = sqlc.arg(key_hash);

-- name: InsertAuditEvent :exec
INSERT INTO audit_events (actor_type, actor_user_id, action, object_type, object_id, request_id, metadata)
VALUES (sqlc.arg(actor_type), NULLIF(sqlc.arg(actor_user_id)::text, '')::uuid, sqlc.arg(action), sqlc.arg(object_type), NULLIF(sqlc.arg(object_id)::text, ''), NULLIF(sqlc.arg(request_id)::text, ''), sqlc.arg(metadata));

-- name: UpdateOwnProfile :execrows
UPDATE users
SET display_name=sqlc.arg(display_name), salutation=sqlc.arg(salutation),
    work_phone_raw=sqlc.arg(work_phone_raw), work_phone_normalized=sqlc.arg(work_phone_normalized),
    version=version+1, updated_at=now()
WHERE id=sqlc.arg(id)::uuid AND version=sqlc.arg(expected_version) AND active;

-- name: GetOwnSecurityProfile :one
SELECT u.id::text, u.username::text, u.display_name, COALESCE(u.email::text, '')::text AS email,
       u.role, u.active, u.version, COALESCE(d.id::text, '')::text AS driver_id,
       u.salutation, u.work_phone_raw, u.work_phone_normalized, u.email_verified_at,
       COALESCE(ev.id::text, '')::text AS pending_email_id,
       COALESCE(ev.email::text, '')::text AS pending_email,
       ev.last_sent_at AS pending_email_last_sent_at, ev.expires_at AS pending_email_expires_at,
       COALESCE((SELECT o.status FROM outbox_events o
                 WHERE o.aggregate_type='user_email_verification' AND o.aggregate_id=ev.id
                 ORDER BY o.created_at DESC LIMIT 1), '')::text AS pending_email_delivery_status,
       COALESCE(t.name, '')::text AS totp_name, t.enabled_at AS totp_enabled_at,
       (SELECT count(*)::integer FROM user_webauthn_credentials w WHERE w.user_id=u.id) AS passkey_count,
       (SELECT count(*)::integer FROM user_recovery_codes r WHERE r.user_id=u.id AND r.used_at IS NULL) AS recovery_code_count
FROM users u
LEFT JOIN drivers d ON d.user_id=u.id
LEFT JOIN user_email_verifications ev ON ev.user_id=u.id AND ev.status='pending'
LEFT JOIN user_totp_credentials t ON t.user_id=u.id
WHERE u.id=sqlc.arg(id)::uuid;

-- name: ListUserSessions :many
SELECT id::text, device_label, created_at, last_used_at, idle_expires_at, absolute_expires_at
FROM sessions
WHERE user_id=sqlc.arg(user_id)::uuid AND revoked_at IS NULL
  AND idle_expires_at > sqlc.arg(now_utc)::timestamptz
  AND absolute_expires_at > sqlc.arg(now_utc)::timestamptz
ORDER BY last_used_at DESC, id;

-- name: RevokeOwnedSessionByID :execrows
UPDATE sessions SET revoked_at=COALESCE(revoked_at, now())
WHERE id=sqlc.arg(id)::uuid AND user_id=sqlc.arg(user_id)::uuid AND revoked_at IS NULL;

-- name: NewIdentityObjectID :one
SELECT gen_random_uuid()::text;

-- name: CancelPendingEmailVerification :execrows
UPDATE user_email_verifications
SET status='cancelled', cancelled_at=now(), updated_at=now()
WHERE user_id=sqlc.arg(user_id)::uuid AND status='pending';

-- name: InsertEmailVerification :exec
INSERT INTO user_email_verifications (
    id, user_id, email, token_hash, token_key_id, token_version, expires_at
) VALUES (
    sqlc.arg(id)::uuid, sqlc.arg(user_id)::uuid, sqlc.arg(email)::citext,
    sqlc.arg(token_hash), sqlc.arg(token_key_id), sqlc.arg(token_version), sqlc.arg(expires_at)::timestamptz
);

-- name: InsertEmailVerificationOutbox :exec
INSERT INTO outbox_events (
    event_type, aggregate_type, aggregate_id, payload, payload_version, idempotency_key, max_attempts
) VALUES (
    'identity.email_verification_requested', 'user_email_verification', sqlc.arg(verification_id)::uuid,
    jsonb_build_object('verification_id', sqlc.arg(verification_id)::text), 1,
    'identity.email_verification_requested:' || sqlc.arg(verification_id)::text || ':' || sqlc.arg(token_version)::text,
    sqlc.arg(max_attempts)
)
ON CONFLICT (idempotency_key) DO NOTHING;

-- name: GetPendingEmailVerification :one
SELECT id::text, user_id::text, email::text, token_key_id, token_version, send_count, last_sent_at, expires_at
FROM user_email_verifications
WHERE user_id=sqlc.arg(user_id)::uuid AND status='pending';

-- name: UpdateEmailVerificationForResend :execrows
UPDATE user_email_verifications
SET token_hash=sqlc.arg(token_hash), token_key_id=sqlc.arg(token_key_id),
    token_version=sqlc.arg(token_version), send_count=send_count+1,
    last_sent_at=now(), expires_at=sqlc.arg(expires_at)::timestamptz, updated_at=now()
WHERE id=sqlc.arg(id)::uuid AND user_id=sqlc.arg(user_id)::uuid AND status='pending'
  AND send_count < 20 AND last_sent_at <= sqlc.arg(resend_before)::timestamptz;

-- name: GetEmailVerificationForUpdate :one
SELECT id::text, user_id::text, email::text, status, expires_at
FROM user_email_verifications
WHERE token_hash=sqlc.arg(token_hash)
FOR UPDATE;

-- name: MarkEmailVerificationExpired :exec
UPDATE user_email_verifications SET status='expired', updated_at=now()
WHERE id=sqlc.arg(id)::uuid AND status='pending';

-- name: ApplyVerifiedEmail :exec
UPDATE users
SET email=sqlc.arg(email)::citext, email_verified_at=now(), version=version+1, updated_at=now()
WHERE id=sqlc.arg(user_id)::uuid;

-- name: MarkEmailVerificationVerified :execrows
UPDATE user_email_verifications
SET status='verified', verified_at=now(), updated_at=now()
WHERE id=sqlc.arg(id)::uuid AND status='pending';

-- name: ClaimIdentityEmailOutbox :many
WITH candidates AS (
    SELECT id FROM outbox_events
    WHERE event_type='identity.email_verification_requested'
      AND ((status IN ('queued','retry_wait') AND available_at <= sqlc.arg(now_utc)::timestamptz)
           OR (status='claimed' AND lease_until <= sqlc.arg(now_utc)::timestamptz))
    ORDER BY available_at, created_at, id
    FOR UPDATE SKIP LOCKED
    LIMIT sqlc.arg(batch_size)
)
UPDATE outbox_events o
SET status='claimed', claimed_by=sqlc.arg(worker_id), lease_until=sqlc.arg(lease_until)::timestamptz,
    locked_at=sqlc.arg(now_utc)::timestamptz, attempt_count=o.attempt_count+1, updated_at=sqlc.arg(now_utc)::timestamptz
FROM candidates c
WHERE o.id=c.id
RETURNING o.id::text, o.aggregate_id::text AS verification_id, o.idempotency_key, o.attempt_count, o.max_attempts;

-- name: GetIdentityEmailDelivery :one
SELECT ev.id::text, ev.user_id::text, ev.email::text, ev.token_key_id, ev.token_version,
       ev.status, ev.expires_at, u.display_name
FROM user_email_verifications ev
JOIN users u ON u.id=ev.user_id AND u.active
WHERE ev.id=sqlc.arg(id)::uuid;

-- name: UpsertTOTPEnrollment :execrows
INSERT INTO user_totp_credentials (user_id, name, secret_key_id, secret_ciphertext)
VALUES (sqlc.arg(user_id)::uuid, sqlc.arg(name), sqlc.arg(secret_key_id), sqlc.arg(secret_ciphertext))
ON CONFLICT (user_id) DO UPDATE
SET name=EXCLUDED.name, secret_key_id=EXCLUDED.secret_key_id,
    secret_ciphertext=EXCLUDED.secret_ciphertext, enabled_at=NULL, last_used_step=NULL, updated_at=now()
WHERE user_totp_credentials.enabled_at IS NULL;

-- name: GetTOTPCredential :one
SELECT name, secret_key_id, secret_ciphertext, enabled_at, last_used_step
FROM user_totp_credentials WHERE user_id=sqlc.arg(user_id)::uuid;

-- name: EnableTOTPCredential :execrows
UPDATE user_totp_credentials SET enabled_at=now(), updated_at=now()
WHERE user_id=sqlc.arg(user_id)::uuid AND enabled_at IS NULL;

-- name: RecordTOTPStep :execrows
UPDATE user_totp_credentials SET last_used_step=sqlc.arg(step), updated_at=now()
WHERE user_id=sqlc.arg(user_id)::uuid AND enabled_at IS NOT NULL
  AND (last_used_step IS NULL OR last_used_step < sqlc.arg(step));

-- name: RenameTOTPCredential :execrows
UPDATE user_totp_credentials SET name=sqlc.arg(name), updated_at=now()
WHERE user_id=sqlc.arg(user_id)::uuid AND enabled_at IS NOT NULL;

-- name: DeleteTOTPCredential :execrows
DELETE FROM user_totp_credentials WHERE user_id=sqlc.arg(user_id)::uuid;

-- name: CountEnabledSecurityFactors :one
SELECT ((EXISTS (
    SELECT 1 FROM user_totp_credentials t
    WHERE t.user_id=sqlc.arg(user_id)::uuid AND t.enabled_at IS NOT NULL
))::integer + (
    SELECT count(*)::integer FROM user_webauthn_credentials w
    WHERE w.user_id=sqlc.arg(user_id)::uuid
))::integer;

-- name: ListWebAuthnCredentials :many
SELECT credential_id, name, credential_key_id, credential_ciphertext, created_at, last_used_at
FROM user_webauthn_credentials
WHERE user_id=sqlc.arg(user_id)::uuid
ORDER BY created_at, credential_id;

-- name: UpsertWebAuthnRegistrationChallenge :exec
INSERT INTO user_webauthn_registration_challenges (session_id, user_id, session_data, expires_at)
VALUES (sqlc.arg(session_id)::uuid, sqlc.arg(user_id)::uuid, sqlc.arg(session_data)::jsonb, sqlc.arg(expires_at)::timestamptz)
ON CONFLICT (session_id) DO UPDATE
SET user_id=EXCLUDED.user_id, session_data=EXCLUDED.session_data, expires_at=EXCLUDED.expires_at, created_at=now();

-- name: GetWebAuthnRegistrationChallenge :one
SELECT session_data, expires_at FROM user_webauthn_registration_challenges
WHERE session_id=sqlc.arg(session_id)::uuid AND user_id=sqlc.arg(user_id)::uuid;

-- name: DeleteWebAuthnRegistrationChallenge :execrows
DELETE FROM user_webauthn_registration_challenges
WHERE session_id=sqlc.arg(session_id)::uuid AND user_id=sqlc.arg(user_id)::uuid;

-- name: InsertWebAuthnCredential :exec
INSERT INTO user_webauthn_credentials (credential_id, user_id, name, credential_key_id, credential_ciphertext)
VALUES (sqlc.arg(credential_id), sqlc.arg(user_id)::uuid, sqlc.arg(name), sqlc.arg(credential_key_id), sqlc.arg(credential_ciphertext));

-- name: UpdateWebAuthnCredential :execrows
UPDATE user_webauthn_credentials
SET credential_key_id=sqlc.arg(credential_key_id), credential_ciphertext=sqlc.arg(credential_ciphertext),
    last_used_at=now(), updated_at=now()
WHERE credential_id=sqlc.arg(credential_id) AND user_id=sqlc.arg(user_id)::uuid;

-- name: RenameWebAuthnCredential :execrows
UPDATE user_webauthn_credentials SET name=sqlc.arg(name), updated_at=now()
WHERE credential_id=sqlc.arg(credential_id) AND user_id=sqlc.arg(user_id)::uuid;

-- name: DeleteWebAuthnCredential :execrows
DELETE FROM user_webauthn_credentials
WHERE credential_id=sqlc.arg(credential_id) AND user_id=sqlc.arg(user_id)::uuid;

-- name: DeleteWebAuthnCredentialsForUser :exec
DELETE FROM user_webauthn_credentials WHERE user_id=sqlc.arg(user_id)::uuid;

-- name: InsertRecoveryCode :exec
INSERT INTO user_recovery_codes (user_id, code_hash)
VALUES (sqlc.arg(user_id)::uuid, sqlc.arg(code_hash));

-- name: DeleteRecoveryCodes :exec
DELETE FROM user_recovery_codes WHERE user_id=sqlc.arg(user_id)::uuid;

-- name: CountRecoveryCodes :one
SELECT count(*)::integer FROM user_recovery_codes
WHERE user_id=sqlc.arg(user_id)::uuid AND used_at IS NULL;

-- name: ConsumeRecoveryCode :execrows
UPDATE user_recovery_codes SET used_at=now()
WHERE user_id=sqlc.arg(user_id)::uuid AND code_hash=sqlc.arg(code_hash) AND used_at IS NULL;

-- name: InsertLoginChallenge :exec
INSERT INTO auth_login_challenges (user_id, token_hash, expires_at)
VALUES (sqlc.arg(user_id)::uuid, sqlc.arg(token_hash), sqlc.arg(expires_at)::timestamptz);

-- name: GetLoginChallenge :one
SELECT c.id::text, c.user_id::text, c.expires_at, c.attempt_count, c.webauthn_session,
       u.username::text, u.display_name, COALESCE(u.email::text, '')::text AS email,
       u.role, u.password_hash, u.must_change_password, u.active, u.version,
       COALESCE(d.id::text, '')::text AS driver_id, u.webauthn_user_handle,
       EXISTS (SELECT 1 FROM user_totp_credentials t WHERE t.user_id=u.id AND t.enabled_at IS NOT NULL) AS totp_enabled,
       EXISTS (SELECT 1 FROM user_webauthn_credentials w WHERE w.user_id=u.id) AS passkey_enabled
FROM auth_login_challenges c
JOIN users u ON u.id=c.user_id
LEFT JOIN drivers d ON d.user_id=u.id
WHERE c.token_hash=sqlc.arg(token_hash) AND c.consumed_at IS NULL;

-- name: SetLoginWebAuthnSession :execrows
UPDATE auth_login_challenges SET webauthn_session=sqlc.arg(session_data)::jsonb
WHERE token_hash=sqlc.arg(token_hash) AND consumed_at IS NULL;

-- name: RecordLoginChallengeFailure :execrows
UPDATE auth_login_challenges SET attempt_count=attempt_count+1
WHERE token_hash=sqlc.arg(token_hash) AND consumed_at IS NULL AND attempt_count < 10;

-- name: ConsumeLoginChallenge :execrows
UPDATE auth_login_challenges SET consumed_at=now()
WHERE token_hash=sqlc.arg(token_hash) AND consumed_at IS NULL
  AND expires_at > sqlc.arg(now_utc)::timestamptz AND attempt_count < 10;

-- name: DeleteLoginChallengesForUser :exec
DELETE FROM auth_login_challenges WHERE user_id=sqlc.arg(user_id)::uuid;

-- name: DeleteWebAuthnRegistrationChallengesForUser :exec
DELETE FROM user_webauthn_registration_challenges WHERE user_id=sqlc.arg(user_id)::uuid;

-- name: ForceOwnSecurityRecovery :execrows
UPDATE users SET must_change_password=true, version=version+1, updated_at=now()
WHERE id=sqlc.arg(id)::uuid AND version=sqlc.arg(expected_version);
