-- Queries used by the P4 article service (no schema change).

-- Admin list (UI/UX §7.3). status_filter: '' (everything but the recycle bin), DRAFT, PUBLISHED, WITHDRAWN,
-- TRASHED or CHANGED (published/withdrawn with unpublished draft edits). pattern is an escaped ILIKE pattern.
-- name: ListAdminArticles :many
SELECT a.id, a.status, a.first_published_at, a.public_updated_at, a.last_published_draft_revision,
       a.created_at, a.updated_at, a.trashed_at,
       d.title AS draft_title, d.revision AS draft_revision, d.saved_at, d.candidate_slug,
       p.title AS public_title, p.slug AS public_slug,
       c.name AS category_name,
       count(*) OVER ()::bigint AS total_count
FROM articles a
JOIN article_drafts d ON d.article_id = a.id
LEFT JOIN article_publications p ON p.article_id = a.id
LEFT JOIN categories c ON c.id = d.category_id
WHERE (CASE @status_filter::text
         WHEN '' THEN a.status <> 'TRASHED'
         WHEN 'CHANGED' THEN a.status IN ('PUBLISHED', 'WITHDRAWN')
                             AND a.last_published_draft_revision IS DISTINCT FROM d.revision
         ELSE a.status = @status_filter::text
       END)
  AND (d.title ILIKE @pattern OR coalesce(p.title, '') ILIKE @pattern)
ORDER BY a.updated_at DESC, a.id DESC
LIMIT @page_limit OFFSET @page_offset;

-- name: TouchArticle :exec
UPDATE articles SET updated_at = now() WHERE id = @id;

-- name: ListDraftTagIDs :many
SELECT tag_id FROM article_draft_tags WHERE article_id = @article_id ORDER BY tag_id;

-- name: GetPublication :one
SELECT * FROM article_publications WHERE article_id = @article_id;

-- Locks the referenced media rows FOR SHARE: this blocks the FOR UPDATE taken by media deletion, so a
-- draft/publish transaction and a delete can never both succeed on the same image (DD T06).
-- name: LockReadyMedia :many
SELECT id, usage_kind FROM media_assets
WHERE id = ANY(@ids::uuid[]) AND state = 'READY'
FOR SHARE;

-- name: ListDraftMediaIDs :many
SELECT media_id FROM article_draft_media WHERE article_id = @article_id;

-- name: CurrentDraftRevision :one
SELECT revision FROM article_drafts WHERE article_id = @article_id;

-- ---- Categories and tags (API-DD §4.3, §5.8) ----

-- name: ListCategoriesAdmin :many
SELECT c.*,
       (SELECT count(*) FROM article_drafts d WHERE d.category_id = c.id)::bigint AS draft_count,
       (SELECT count(*) FROM article_publications p JOIN articles a ON a.id = p.article_id
         WHERE p.category_id = c.id AND a.status = 'PUBLISHED')::bigint AS published_count
FROM categories c
ORDER BY c.sort_order, c.name, c.id;

-- name: CreateCategoryFull :one
INSERT INTO categories (id, name, slug, description, sort_order, is_active)
VALUES (@id, @name, @slug, @description, @sort_order, @is_active)
RETURNING *;

-- name: UpdateCategory :one
UPDATE categories
SET name = @name, slug = @slug, description = @description, sort_order = @sort_order, is_active = @is_active,
    updated_at = now()
WHERE id = @id
RETURNING *;

-- name: GetCategory :one
SELECT * FROM categories WHERE id = @id;

-- name: DeleteCategory :execrows
DELETE FROM categories WHERE id = @id;

-- name: ListTagsAdmin :many
SELECT t.*,
       (SELECT count(*) FROM article_draft_tags d WHERE d.tag_id = t.id)::bigint AS draft_count,
       (SELECT count(*) FROM article_publication_tags pt JOIN articles a ON a.id = pt.article_id
         WHERE pt.tag_id = t.id AND a.status = 'PUBLISHED')::bigint AS published_count
FROM tags t
ORDER BY t.sort_order, t.name, t.id;

-- name: CreateTagFull :one
INSERT INTO tags (id, name, slug, sort_order, is_active)
VALUES (@id, @name, @slug, @sort_order, @is_active)
RETURNING *;

-- name: UpdateTag :one
UPDATE tags
SET name = @name, slug = @slug, sort_order = @sort_order, is_active = @is_active, updated_at = now()
WHERE id = @id
RETURNING *;

-- name: GetTag :one
SELECT * FROM tags WHERE id = @id;

-- name: DeleteTag :execrows
DELETE FROM tags WHERE id = @id;

-- name: CountExistingTags :one
SELECT count(*)::bigint FROM tags WHERE id = ANY(@ids::uuid[]);

-- ---- Public detail (API-DD §5.1) ----

-- name: ListPublicationTagsFull :many
SELECT t.id, t.name, t.slug FROM article_publication_tags pt JOIN tags t ON t.id = pt.tag_id
WHERE pt.article_id = @article_id ORDER BY t.sort_order, t.name;

-- name: PreviousPublishedArticle :one
SELECT p.slug, p.title FROM articles a JOIN article_publications p ON p.article_id = a.id
WHERE a.status = 'PUBLISHED' AND (a.first_published_at, a.id) < (@first_published_at::timestamptz, @id::uuid)
ORDER BY a.first_published_at DESC, a.id DESC LIMIT 1;

-- name: NextPublishedArticle :one
SELECT p.slug, p.title FROM articles a JOIN article_publications p ON p.article_id = a.id
WHERE a.status = 'PUBLISHED' AND (a.first_published_at, a.id) > (@first_published_at::timestamptz, @id::uuid)
ORDER BY a.first_published_at ASC, a.id ASC LIMIT 1;

-- name: SlugOwner :one
SELECT article_id FROM article_slugs WHERE slug = @slug;
