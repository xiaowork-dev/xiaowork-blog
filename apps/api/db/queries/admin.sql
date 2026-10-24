-- ---- P7 admin: overview, users, site settings, about page (API-DD §4.3/§4.4, §5.8; DD §8) ----

-- name: AdminOverviewCounts :one
SELECT
  (SELECT count(*) FROM articles WHERE status <> 'TRASHED')::bigint                  AS article_count,
  (SELECT count(*) FROM articles WHERE status = 'PUBLISHED')::bigint                 AS published_count,
  (SELECT count(*) FROM articles WHERE status = 'DRAFT')::bigint                     AS draft_count,
  (SELECT count(*) FROM articles WHERE status = 'WITHDRAWN')::bigint                 AS withdrawn_count,
  (SELECT count(*) FROM articles WHERE status = 'TRASHED')::bigint                   AS trashed_count,
  (SELECT count(*) FROM articles a JOIN article_drafts d ON d.article_id = a.id
    WHERE a.status IN ('PUBLISHED', 'WITHDRAWN')
      AND a.last_published_draft_revision IS DISTINCT FROM d.revision)::bigint       AS changed_count,
  (SELECT count(*) FROM users)::bigint                                               AS user_count,
  (SELECT count(*) FROM users WHERE email_verified_at IS NULL)::bigint               AS unverified_user_count,
  (SELECT count(*) FROM users WHERE status = 'DISABLED')::bigint                     AS disabled_user_count,
  (SELECT count(*) FROM users WHERE created_at > now() - interval '7 days')::bigint  AS new_user_count,
  (SELECT count(*) FROM comments WHERE status = 'VISIBLE')::bigint                   AS comment_count,
  (SELECT count(*) FROM comments WHERE status = 'HIDDEN')::bigint                    AS hidden_comment_count,
  (SELECT count(*) FROM comments WHERE created_at > now() - interval '7 days')::bigint AS new_comment_count,
  (SELECT count(*) FROM media_assets WHERE state = 'READY')::bigint                  AS media_count,
  -- PENDING_DELETE rows are deletions interrupted half-way (P3 state machine); blog-admin media-check repairs them.
  (SELECT count(*) FROM media_assets WHERE state = 'PENDING_DELETE')::bigint         AS stuck_media_count,
  (SELECT count(*) FROM users WHERE role = 'ADMIN' AND status = 'ACTIVE')::bigint    AS admin_count;

-- name: AdminRecentArticles :many
SELECT a.id, a.status, a.updated_at, a.public_updated_at, d.title AS draft_title, p.title AS public_title, p.slug AS public_slug,
       (a.status IN ('PUBLISHED', 'WITHDRAWN') AND a.last_published_draft_revision IS DISTINCT FROM d.revision)::boolean AS has_unpublished_changes
FROM articles a
JOIN article_drafts d ON d.article_id = a.id
LEFT JOIN article_publications p ON p.article_id = a.id
ORDER BY a.updated_at DESC, a.id DESC
LIMIT @row_limit;

-- name: AdminRecentEvents :many
SELECT l.id, l.action, l.object_type, l.object_key, l.created_at, u.nickname AS actor_nickname
FROM admin_audit_logs l JOIN users u ON u.id = l.actor_user_id
ORDER BY l.created_at DESC, l.id DESC
LIMIT @row_limit;

-- ---- users ----

-- Optional filters: keyword over nickname/e-mail, status, verified, role.
-- name: AdminListUsers :many
SELECT u.id, u.email, u.nickname, u.role, u.status, u.email_verified_at, u.created_at,
       p.avatar_media_id,
       (SELECT count(*) FROM comments c WHERE c.author_id = u.id)::bigint AS comment_count,
       count(*) OVER ()::bigint AS total_count
FROM users u
LEFT JOIN user_profiles p ON p.user_id = u.id
WHERE (sqlc.narg(pattern)::text IS NULL OR u.nickname ILIKE sqlc.narg(pattern)::text OR u.email ILIKE sqlc.narg(pattern)::text)
  AND (sqlc.narg(status)::text IS NULL OR u.status = sqlc.narg(status)::text)
  AND (sqlc.narg(verified)::boolean IS NULL OR (u.email_verified_at IS NOT NULL) = sqlc.narg(verified)::boolean)
  AND (sqlc.narg(role)::text IS NULL OR u.role = sqlc.narg(role)::text)
ORDER BY u.created_at DESC, u.id DESC
LIMIT @page_limit OFFSET @page_offset;

-- name: AdminGetUser :one
SELECT u.id, u.email, u.nickname, u.role, u.status, u.email_verified_at, u.created_at, u.updated_at,
       p.avatar_media_id, p.bio,
       (SELECT count(*) FROM comments c WHERE c.author_id = u.id)::bigint AS comment_count,
       (SELECT count(*) FROM comments c WHERE c.author_id = u.id AND c.status = 'VISIBLE')::bigint AS visible_comment_count,
       (SELECT count(*) FROM comments c WHERE c.author_id = u.id AND c.status = 'HIDDEN')::bigint AS hidden_comment_count,
       (SELECT count(*) FROM auth_sessions s WHERE s.user_id = u.id AND s.revoked_at IS NULL AND s.expires_at > now())::bigint AS active_session_count
FROM users u
LEFT JOIN user_profiles p ON p.user_id = u.id
WHERE u.id = @id;

-- name: AdminUserRecentComments :many
SELECT c.id, c.status, c.body_text, c.created_at, c.article_id, coalesce(p.title, d.title) AS article_title,
       (a.status = 'PUBLISHED')::boolean AS article_public, p.slug AS article_slug
FROM comments c
JOIN articles a ON a.id = c.article_id
JOIN article_drafts d ON d.article_id = a.id
LEFT JOIN article_publications p ON p.article_id = a.id
WHERE c.author_id = @author_id
ORDER BY c.created_at DESC, c.id DESC
LIMIT @row_limit;

-- name: LockUser :one
SELECT id, role, status FROM users WHERE id = @id FOR UPDATE;

-- Every unused one-time token of a disabled account is withdrawn as well (verification, reset, e-mail change).
-- name: RevokeAllUnusedTokens :execrows
UPDATE auth_tokens SET revoked_at = now()
WHERE user_id = @user_id AND consumed_at IS NULL AND revoked_at IS NULL;

-- ---- site settings (singleton row id = 1) ----

-- name: LockSiteSettings :one
SELECT * FROM site_settings WHERE id = 1 FOR UPDATE;

-- name: UpdateSiteSettings :one
UPDATE site_settings
SET site_title = @site_title, site_description = @site_description, profile_name = @profile_name,
    profile_bio = @profile_bio, profile_avatar_media_id = sqlc.narg(profile_avatar_media_id),
    logo_media_id = sqlc.narg(logo_media_id), favicon_media_id = sqlc.narg(favicon_media_id),
    contact_email = sqlc.narg(contact_email), default_seo_title = sqlc.narg(default_seo_title),
    default_seo_description = sqlc.narg(default_seo_description), updated_at = now()
WHERE id = 1
RETURNING *;

-- name: ListAllSocialLinks :many
SELECT * FROM site_social_links ORDER BY sort_order, id;

-- name: DeleteAllSocialLinks :exec
DELETE FROM site_social_links;

-- name: InsertSocialLink :exec
INSERT INTO site_social_links (id, icon_key, label, url, sort_order, is_enabled)
VALUES (@id, @icon_key, @label, @url, @sort_order, @is_enabled);

-- name: ListAllNavigationLinks :many
SELECT * FROM site_navigation_links ORDER BY sort_order, id;

-- name: DeleteAllNavigationLinks :exec
DELETE FROM site_navigation_links;

-- name: InsertNavigationLink :exec
INSERT INTO site_navigation_links (id, label, link_type, url, sort_order, is_enabled)
VALUES (@id, @label, @link_type, @url, @sort_order, @is_enabled);

-- ---- about page (static_pages 'about'; saving publishes at once, DD §8.4) ----

-- name: GetAboutPageAdmin :one
SELECT sp.page_key, sp.title, sp.body_markdown, sp.updated_at, u.nickname AS updated_by_nickname
FROM static_pages sp JOIN users u ON u.id = sp.updated_by
WHERE sp.page_key = 'about';

-- name: UpsertAboutPage :one
INSERT INTO static_pages (page_key, title, body_markdown, body_plain_text, updated_by, updated_at)
VALUES ('about', @title, @body_markdown, @body_plain_text, @updated_by, now())
ON CONFLICT (page_key) DO UPDATE
SET title = EXCLUDED.title, body_markdown = EXCLUDED.body_markdown, body_plain_text = EXCLUDED.body_plain_text,
    updated_by = EXCLUDED.updated_by, updated_at = now()
RETURNING updated_at;

-- name: DeleteAboutPageMedia :exec
DELETE FROM static_page_media WHERE page_key = 'about';

-- name: AddAboutPageMedia :exec
INSERT INTO static_page_media (page_key, media_id)
SELECT 'about', unnest(@media_ids::uuid[])
ON CONFLICT DO NOTHING;

-- Locks the about row so concurrent saves serialise (the row may not exist yet: then nothing is locked).
-- name: LockAboutPage :exec
SELECT 1 FROM static_pages WHERE page_key = 'about' FOR UPDATE;
