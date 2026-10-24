-- ---- P6 comments (API-DD §4.1/§4.2/§4.4, §5.5; DD §6.1, T05) ----
-- Public projections never expose body_text unless the comment is VISIBLE, nor author e-mail.
-- Withheld text comes back as '' (a stored VISIBLE/HIDDEN body is never blank, see comments_body_when_present).
-- A hidden or deleted comment is only listed publicly while it has direct replies, as a placeholder that
-- keeps the thread readable (UI/UX §5.2); without replies it simply disappears.

-- Commenting needs a PUBLISHED article whose public snapshot allows comments. FOR SHARE on both rows
-- serialises with publish/withdraw (which lock the article FOR UPDATE), so a comment cannot slip in after
-- comments were closed or the article was withdrawn (DD T05 step 2).
-- name: LockArticleForComment :one
SELECT a.id, p.comments_enabled
FROM articles a JOIN article_publications p ON p.article_id = a.id
WHERE a.id = @id AND a.status = 'PUBLISHED'
FOR SHARE OF a, p;

-- name: GetPublicArticleCommentState :one
SELECT a.id, p.comments_enabled,
       (SELECT count(*) FROM comments c WHERE c.article_id = a.id AND c.status = 'VISIBLE')::bigint AS visible_count
FROM articles a JOIN article_publications p ON p.article_id = a.id
WHERE a.id = @id AND a.status = 'PUBLISHED';

-- Root comments of one page; oldest=false is "latest first" (API-DD §5.5 default).
-- name: ListPublicRootComments :many
SELECT c.id, c.article_id, c.author_id, c.status, c.created_at,
       coalesce(CASE WHEN c.status = 'VISIBLE' THEN c.body_text END, '')::text AS body_text,
       u.nickname AS author_nickname, u.role AS author_role, p.avatar_media_id AS author_avatar,
       (SELECT count(*) FROM comments r WHERE r.root_id = c.id
          AND (r.status = 'VISIBLE' OR EXISTS (SELECT 1 FROM comments k WHERE k.article_id = r.article_id AND k.parent_id = r.id)))::bigint AS reply_count,
       count(*) OVER ()::bigint AS total_count
FROM comments c
JOIN users u ON u.id = c.author_id
LEFT JOIN user_profiles p ON p.user_id = u.id
WHERE c.article_id = @article_id AND c.parent_id IS NULL
  AND (c.status = 'VISIBLE' OR EXISTS (SELECT 1 FROM comments k WHERE k.article_id = c.article_id AND k.parent_id = c.id))
ORDER BY
  CASE WHEN @oldest::boolean THEN c.created_at END ASC,
  CASE WHEN @oldest::boolean THEN c.id END ASC,
  c.created_at DESC, c.id DESC
LIMIT @page_limit OFFSET @page_offset;

-- The first @per_root replies (oldest first) of each listed root, in one round trip.
-- name: ListReplyPreviews :many
SELECT x.id, x.article_id, x.root_id, x.parent_id, x.author_id, x.status, x.created_at, x.body_text,
       x.author_nickname, x.author_role, x.author_avatar, x.parent_nickname, x.parent_status
FROM (
  SELECT c.id, c.article_id, c.root_id, c.parent_id, c.author_id, c.status, c.created_at,
         coalesce(CASE WHEN c.status = 'VISIBLE' THEN c.body_text END, '')::text AS body_text,
         u.nickname AS author_nickname, u.role AS author_role, p.avatar_media_id AS author_avatar,
         pu.nickname AS parent_nickname, pc.status AS parent_status,
         row_number() OVER (PARTITION BY c.root_id ORDER BY c.created_at, c.id) AS rn
  FROM comments c
  JOIN users u ON u.id = c.author_id
  LEFT JOIN user_profiles p ON p.user_id = u.id
  JOIN comments pc ON pc.id = c.parent_id
  JOIN users pu ON pu.id = pc.author_id
  WHERE c.root_id = ANY(@root_ids::uuid[])
    AND (c.status = 'VISIBLE' OR EXISTS (SELECT 1 FROM comments k WHERE k.article_id = c.article_id AND k.parent_id = c.id))
) x
WHERE x.rn <= @per_root::int
ORDER BY x.root_id, x.created_at, x.id;

-- name: ListPublicReplies :many
SELECT c.id, c.article_id, c.root_id, c.parent_id, c.author_id, c.status, c.created_at,
       coalesce(CASE WHEN c.status = 'VISIBLE' THEN c.body_text END, '')::text AS body_text,
       u.nickname AS author_nickname, u.role AS author_role, p.avatar_media_id AS author_avatar,
       pu.nickname AS parent_nickname, pc.status AS parent_status,
       count(*) OVER ()::bigint AS total_count
FROM comments c
JOIN users u ON u.id = c.author_id
LEFT JOIN user_profiles p ON p.user_id = u.id
JOIN comments pc ON pc.id = c.parent_id
JOIN users pu ON pu.id = pc.author_id
WHERE c.root_id = @root_id
  AND (c.status = 'VISIBLE' OR EXISTS (SELECT 1 FROM comments k WHERE k.article_id = c.article_id AND k.parent_id = c.id))
ORDER BY c.created_at, c.id
LIMIT @page_limit OFFSET @page_offset;

-- A root comment whose article is currently PUBLISHED (the replies endpoint is public).
-- name: GetPublicRoot :one
SELECT c.id, c.article_id
FROM comments c JOIN articles a ON a.id = c.article_id
WHERE c.id = @id AND c.parent_id IS NULL AND a.status = 'PUBLISHED';

-- name: GetCommentForUpdate :one
SELECT id, article_id, author_id, parent_id, root_id, status FROM comments WHERE id = @id FOR UPDATE;

-- name: GetCommentRef :one
SELECT c.id, c.article_id, c.author_id, c.root_id, c.status, u.nickname AS author_nickname
FROM comments c JOIN users u ON u.id = c.author_id
WHERE c.id = @id;

-- Same author, same article, same text within 10 minutes: an accidental double submit or a spam loop.
-- name: HasRecentDuplicateComment :one
SELECT EXISTS (
  SELECT 1 FROM comments
  WHERE author_id = @author_id AND article_id = @article_id AND body_text = @body_text
    AND created_at > now() - interval '10 minutes'
)::boolean;

-- name: InsertComment :one
INSERT INTO comments (id, article_id, author_id, parent_id, root_id, body_text)
VALUES (@id, @article_id, @author_id, sqlc.narg(parent_id), sqlc.narg(root_id), @body_text)
RETURNING created_at;

-- name: DeleteOwnComment :execrows
UPDATE comments
SET status = 'DELETED_AUTHOR', body_text = NULL, deleted_at = now(), updated_at = now()
WHERE id = @id AND author_id = @author_id AND status IN ('VISIBLE', 'HIDDEN');

-- Own history (GET /me/comments). The author may see the text of their own hidden comment; deleted ones
-- are tombstones. Article title/slug only while the article is PUBLISHED.
-- name: ListMyComments :many
SELECT c.id, c.article_id, c.parent_id, c.root_id, c.status, c.created_at,
       coalesce(CASE WHEN c.status IN ('VISIBLE', 'HIDDEN') THEN c.body_text END, '')::text AS body_text,
       (a.status = 'PUBLISHED')::boolean AS article_public,
       coalesce(CASE WHEN a.status = 'PUBLISHED' THEN p.title END, '')::text AS article_title,
       coalesce(CASE WHEN a.status = 'PUBLISHED' THEN p.slug END, '')::text AS article_slug,
       count(*) OVER ()::bigint AS total_count
FROM comments c
JOIN articles a ON a.id = c.article_id
LEFT JOIN article_publications p ON p.article_id = a.id
WHERE c.author_id = @author_id
ORDER BY c.created_at DESC, c.id DESC
LIMIT @page_limit OFFSET @page_offset;

-- ---- admin moderation (API-DD §4.4, UI/UX §7.5) ----

-- Optional filters: article, author, status, time range [from, to), keyword over text/nickname/e-mail.
-- name: ListAdminComments :many
SELECT c.id, c.article_id, c.parent_id, c.root_id, c.status, c.body_text, c.created_at, c.hidden_at, c.deleted_at,
       u.id AS author_id, u.nickname AS author_nickname, u.email AS author_email, u.role AS author_role,
       u.status AS author_status,
       a.status AS article_status, coalesce(p.title, d.title) AS article_title, p.slug AS article_slug,
       (SELECT count(*) FROM comments r WHERE r.root_id = c.id)::bigint AS reply_count,
       count(*) OVER ()::bigint AS total_count
FROM comments c
JOIN users u ON u.id = c.author_id
JOIN articles a ON a.id = c.article_id
JOIN article_drafts d ON d.article_id = a.id
LEFT JOIN article_publications p ON p.article_id = a.id
WHERE (sqlc.narg(article_id)::uuid IS NULL OR c.article_id = sqlc.narg(article_id)::uuid)
  AND (sqlc.narg(author_id)::uuid IS NULL OR c.author_id = sqlc.narg(author_id)::uuid)
  AND (sqlc.narg(status)::text IS NULL OR c.status = sqlc.narg(status)::text
       OR (sqlc.narg(status)::text = 'DELETED' AND c.status IN ('DELETED_AUTHOR', 'DELETED_ADMIN')))
  AND (sqlc.narg(created_from)::timestamptz IS NULL OR c.created_at >= sqlc.narg(created_from)::timestamptz)
  AND (sqlc.narg(created_to)::timestamptz IS NULL OR c.created_at < sqlc.narg(created_to)::timestamptz)
  AND (sqlc.narg(pattern)::text IS NULL OR c.body_text ILIKE sqlc.narg(pattern)::text
       OR u.nickname ILIKE sqlc.narg(pattern)::text OR u.email ILIKE sqlc.narg(pattern)::text)
ORDER BY c.created_at DESC, c.id DESC
LIMIT @page_limit OFFSET @page_offset;

-- name: AdminDeleteComment :execrows
UPDATE comments
SET status = 'DELETED_ADMIN', body_text = NULL, deleted_at = now(), updated_at = now()
WHERE id = @id AND status IN ('VISIBLE', 'HIDDEN');
