-- name: FindUserByUsername :one
SELECT u.id::text AS id, u.username::text AS username, u.display_name,
       COALESCE(u.email::text, '')::text AS email, u.role, u.password_hash,
       u.must_change_password, u.active, u.version,
       COALESCE(d.id::text, '')::text AS driver_id
FROM users u
LEFT JOIN drivers d ON d.user_id = u.id
WHERE u.username = sqlc.arg(username);

-- name: FindUserByID :one
SELECT u.id::text, u.username::text, u.display_name, COALESCE(u.email::text, '')::text AS email,
       u.role, u.password_hash, u.must_change_password, u.active, u.version,
       COALESCE(d.id::text, '')::text AS driver_id
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
INSERT INTO users (username, display_name, email, role, password_hash, must_change_password)
VALUES (sqlc.arg(username), sqlc.arg(display_name), NULLIF(sqlc.arg(email)::text, '')::citext, sqlc.arg(role), sqlc.arg(password_hash), sqlc.arg(must_change_password))
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
    version = version + 1, updated_at = now()
WHERE id = sqlc.arg(id)::uuid AND version = sqlc.arg(expected_version);

-- name: RevokeUserSessions :exec
UPDATE sessions SET revoked_at = COALESCE(revoked_at, now())
WHERE user_id = sqlc.arg(user_id)::uuid AND revoked_at IS NULL;

-- name: InsertSession :one
INSERT INTO sessions (user_id, token_hash, csrf_token_hash, idle_expires_at, absolute_expires_at)
VALUES (sqlc.arg(user_id)::uuid, sqlc.arg(token_hash), sqlc.arg(csrf_token_hash), sqlc.arg(idle_expires_at), sqlc.arg(absolute_expires_at))
RETURNING id::text;

-- name: FindSession :one
SELECT s.id::text, s.user_id::text, s.csrf_token_hash, s.idle_expires_at,
       s.absolute_expires_at, s.revoked_at, u.username::text, u.display_name,
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
