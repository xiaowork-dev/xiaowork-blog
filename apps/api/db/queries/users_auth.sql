-- name: CreateUser :one
INSERT INTO users (id, email, email_normalized, password_hash, nickname, role)
VALUES (@id, @email, @email_normalized, @password_hash, @nickname, @role)
RETURNING *;

-- name: CreateUserProfile :exec
INSERT INTO user_profiles (user_id) VALUES (@user_id);

-- name: GetUserByEmailNormalized :one
SELECT * FROM users WHERE email_normalized = @email_normalized;

-- name: MarkEmailVerified :execrows
UPDATE users
SET email_verified_at = now(), updated_at = now()
WHERE id = @id AND email_verified_at IS NULL;

-- name: SetUserStatus :execrows
UPDATE users SET status = @status, updated_at = now() WHERE id = @id;

-- name: SetUserAvatar :exec
UPDATE user_profiles SET avatar_media_id = @avatar_media_id, updated_at = now() WHERE user_id = @user_id;

-- name: CreateSession :exec
INSERT INTO auth_sessions (id, user_id, token_digest, expires_at)
VALUES (@id, @user_id, @token_digest, @expires_at);

-- Authentication re-checks expiry, revocation and the live account status on every request (DD §4.3).
-- name: GetActiveSessionUser :one
SELECT s.id AS session_id, u.id AS user_id, u.role, u.email_verified_at
FROM auth_sessions s
JOIN users u ON u.id = s.user_id
WHERE s.token_digest = @token_digest
  AND s.revoked_at IS NULL
  AND s.expires_at > now()
  AND u.status = 'ACTIVE';

-- name: RevokeUserSessions :execrows
UPDATE auth_sessions SET revoked_at = now() WHERE user_id = @user_id AND revoked_at IS NULL;

-- name: CreateAuthToken :exec
INSERT INTO auth_tokens (id, user_id, purpose, token_digest, target_email, target_email_normalized, expires_at)
VALUES (@id, @user_id, @purpose, @token_digest, @target_email, @target_email_normalized, @expires_at);

-- One-time consumption: the conditional UPDATE row-locks the token, so concurrent consumers
-- re-evaluate the predicate and at most one succeeds (DD T01, DB-03). No row => invalid/expired/used.
-- name: ConsumeAuthToken :one
UPDATE auth_tokens
SET consumed_at = now()
WHERE token_digest = @token_digest
  AND purpose = @purpose
  AND consumed_at IS NULL
  AND revoked_at IS NULL
  AND expires_at > now()
RETURNING id, user_id, target_email, target_email_normalized;

-- name: RevokeUnusedTokens :execrows
UPDATE auth_tokens
SET revoked_at = now()
WHERE user_id = @user_id AND purpose = @purpose AND consumed_at IS NULL AND revoked_at IS NULL;

-- name: HitRateWindow :one
INSERT INTO auth_rate_windows (action_key, subject_digest, window_started_at, request_count, expires_at)
VALUES (@action_key, @subject_digest, @window_started_at, 1, @expires_at)
ON CONFLICT (action_key, subject_digest, window_started_at)
DO UPDATE SET request_count = auth_rate_windows.request_count + 1
RETURNING request_count;
