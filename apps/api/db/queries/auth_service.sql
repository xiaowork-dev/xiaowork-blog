-- Queries used by the P2 authentication service (no schema change).

-- name: GetUserByID :one
SELECT * FROM users WHERE id = @id;

-- Session lookup that still returns expired/revoked rows so the API can tell
-- SESSION_EXPIRED from AUTH_REQUIRED; validity is decided by the database clock.
-- name: GetSessionWithUser :one
SELECT s.id AS session_id,
       (s.revoked_at IS NULL AND s.expires_at > now())::boolean AS is_valid,
       u.id AS user_id, u.email, u.nickname, u.role, u.status, u.email_verified_at, u.created_at AS user_created_at,
       p.avatar_media_id, p.bio
FROM auth_sessions s
JOIN users u ON u.id = s.user_id
LEFT JOIN user_profiles p ON p.user_id = u.id
WHERE s.token_digest = @token_digest;

-- Throttled activity timestamp (DD §4.3 "允许节流写入").
-- name: TouchSession :exec
UPDATE auth_sessions SET last_seen_at = now()
WHERE id = @id AND (last_seen_at IS NULL OR last_seen_at < now() - interval '5 minutes');

-- name: RevokeSession :execrows
UPDATE auth_sessions SET revoked_at = now() WHERE id = @id AND revoked_at IS NULL;

-- name: RevokeOtherSessions :execrows
UPDATE auth_sessions SET revoked_at = now()
WHERE user_id = @user_id AND id <> @keep_session_id AND revoked_at IS NULL;

-- name: UpdatePasswordHash :exec
UPDATE users SET password_hash = @password_hash, password_changed_at = now(), updated_at = now() WHERE id = @id;

-- name: UpdateUserEmail :exec
UPDATE users SET email = @email, email_normalized = @email_normalized, updated_at = now() WHERE id = @id;

-- name: AdminExists :one
SELECT EXISTS (SELECT 1 FROM users WHERE role = 'ADMIN')::boolean AS exists;

-- name: CreateVerifiedAdmin :one
INSERT INTO users (id, email, email_normalized, password_hash, nickname, role, email_verified_at)
VALUES (@id, @email, @email_normalized, @password_hash, @nickname, 'ADMIN', now())
RETURNING *;

-- Read-only view of a fixed window, used to block before an attempt is counted.
-- name: PeekRateWindow :one
SELECT coalesce((SELECT request_count FROM auth_rate_windows
                 WHERE action_key = @action_key AND subject_digest = @subject_digest
                   AND window_started_at = @window_started_at), 0)::integer AS request_count;

-- name: DeleteExpiredRateWindows :execrows
DELETE FROM auth_rate_windows WHERE expires_at < now();

-- Finished or expired one-time tokens are kept for a week for troubleshooting, then removed (DD §11.2).
-- name: DeleteStaleAuthTokens :execrows
DELETE FROM auth_tokens WHERE expires_at < now() - interval '7 days';

-- name: DeleteStaleSessions :execrows
DELETE FROM auth_sessions WHERE expires_at < now() - interval '7 days' OR revoked_at < now() - interval '7 days';
