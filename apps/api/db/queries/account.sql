-- P6 user centre (API-DD §4.2 PATCH /me/profile).

-- name: UpdateUserNickname :exec
UPDATE users SET nickname = @nickname, updated_at = now() WHERE id = @id;

-- name: UpsertProfileBio :exec
INSERT INTO user_profiles (user_id, bio) VALUES (@user_id, sqlc.narg(bio))
ON CONFLICT (user_id) DO UPDATE SET bio = EXCLUDED.bio, updated_at = now();
