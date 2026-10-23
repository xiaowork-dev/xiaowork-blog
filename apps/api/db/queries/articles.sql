-- name: CreateArticle :one
INSERT INTO articles (id, author_id) VALUES (@id, @author_id) RETURNING *;

-- name: CreateArticleDraft :one
INSERT INTO article_drafts (article_id, last_edited_by) VALUES (@article_id, @last_edited_by) RETURNING *;

-- name: CreateArticleMetrics :exec
INSERT INTO article_metrics (article_id) VALUES (@article_id);

-- Draft save and publish both lock the article row first (DD T03 lock order).
-- name: LockArticle :one
SELECT * FROM articles WHERE id = @id FOR UPDATE;

-- name: GetArticle :one
SELECT * FROM articles WHERE id = @id;

-- name: GetArticleDraft :one
SELECT * FROM article_drafts WHERE article_id = @article_id;

-- Full-snapshot draft save with revision CAS (API §5.2). No row => 409 DRAFT_REVISION_CONFLICT.
-- name: UpdateArticleDraft :one
UPDATE article_drafts
SET revision         = revision + 1,
    title            = @title,
    summary          = @summary,
    body_markdown    = @body_markdown,
    candidate_slug   = @candidate_slug,
    category_id      = @category_id,
    cover_media_id   = @cover_media_id,
    seo_title        = @seo_title,
    seo_description  = @seo_description,
    comments_enabled = @comments_enabled,
    saved_at         = now(),
    last_edited_by   = @last_edited_by
WHERE article_id = @article_id AND revision = @expected_revision
RETURNING revision, saved_at;

-- name: DeleteDraftTags :exec
DELETE FROM article_draft_tags WHERE article_id = @article_id;

-- name: AddDraftTags :exec
INSERT INTO article_draft_tags (article_id, tag_id)
SELECT @article_id::uuid, unnest(@tag_ids::uuid[]);

-- name: DeleteDraftMedia :exec
DELETE FROM article_draft_media WHERE article_id = @article_id;

-- name: AddDraftMedia :exec
INSERT INTO article_draft_media (article_id, media_id)
SELECT @article_id::uuid, unnest(@media_ids::uuid[]);

-- name: ListDraftTagSlugs :many
SELECT t.slug FROM article_draft_tags dt JOIN tags t ON t.id = dt.tag_id
WHERE dt.article_id = @article_id ORDER BY t.slug;

-- ---- Publish transaction steps (DD T03). The caller runs them in one ExecTx after LockArticle. ----

-- Registers the slug for this article. Re-registering its own slug is a no-op; a slug owned by any other
-- article (current or historical) violates the slug primary key => 409 SLUG_TAKEN.
-- name: RegisterArticleSlug :exec
INSERT INTO article_slugs (slug, article_id) VALUES (@slug, @article_id)
ON CONFLICT (slug, article_id) DO NOTHING;

-- name: ClearCurrentArticleSlug :exec
UPDATE article_slugs SET is_current = FALSE
WHERE article_id = @article_id AND is_current AND slug <> @slug;

-- name: SetCurrentArticleSlug :exec
UPDATE article_slugs SET is_current = TRUE WHERE slug = @slug AND article_id = @article_id;

-- Copies the current draft into the single public snapshot. Plain text and the fallback summary
-- are derived from Markdown by the Go service and passed in.
-- name: UpsertPublicationFromDraft :one
INSERT INTO article_publications (
    article_id, publication_revision, source_draft_revision, title, summary, body_markdown, body_plain_text,
    slug, category_id, cover_media_id, seo_title, seo_description, comments_enabled, published_at)
SELECT d.article_id, 1, d.revision, d.title, coalesce(d.summary, @fallback_summary::text), d.body_markdown,
       @body_plain_text::text, @slug::varchar, d.category_id, d.cover_media_id, d.seo_title, d.seo_description,
       d.comments_enabled, @published_at::timestamptz
FROM article_drafts d
WHERE d.article_id = @article_id::uuid
ON CONFLICT (article_id) DO UPDATE
SET publication_revision  = article_publications.publication_revision + 1,
    source_draft_revision = EXCLUDED.source_draft_revision,
    title                 = EXCLUDED.title,
    summary               = EXCLUDED.summary,
    body_markdown         = EXCLUDED.body_markdown,
    body_plain_text       = EXCLUDED.body_plain_text,
    slug                  = EXCLUDED.slug,
    category_id           = EXCLUDED.category_id,
    cover_media_id        = EXCLUDED.cover_media_id,
    seo_title             = EXCLUDED.seo_title,
    seo_description       = EXCLUDED.seo_description,
    comments_enabled      = EXCLUDED.comments_enabled,
    published_at          = EXCLUDED.published_at
RETURNING publication_revision, source_draft_revision, slug, published_at;

-- name: DeletePublicationTags :exec
DELETE FROM article_publication_tags WHERE article_id = @article_id;

-- name: CopyDraftTagsToPublication :exec
INSERT INTO article_publication_tags (article_id, tag_id)
SELECT dt.article_id, dt.tag_id FROM article_draft_tags dt WHERE dt.article_id = @article_id;

-- name: DeletePublicationMedia :exec
DELETE FROM article_publication_media WHERE article_id = @article_id;

-- name: CopyDraftMediaToPublication :exec
INSERT INTO article_publication_media (article_id, media_id)
SELECT dm.article_id, dm.media_id FROM article_draft_media dm WHERE dm.article_id = @article_id;

-- first_published_at is only set once (DD §5.1).
-- name: MarkArticlePublished :exec
UPDATE articles
SET status                        = 'PUBLISHED',
    first_published_at            = coalesce(first_published_at, @published_at::timestamptz),
    public_updated_at             = @published_at::timestamptz,
    last_published_draft_revision = @draft_revision::bigint,
    withdrawn_at                  = NULL,
    updated_at                    = now()
WHERE id = @id::uuid;

-- name: WithdrawArticle :execrows
UPDATE articles SET status = 'WITHDRAWN', withdrawn_at = now(), updated_at = now()
WHERE id = @id AND status = 'PUBLISHED';

-- name: TrashArticle :execrows
UPDATE articles SET status = 'TRASHED', trashed_at = now(), updated_at = now()
WHERE id = @id AND status <> 'TRASHED';

-- Restore never republishes (DD T04): previously published => WITHDRAWN, otherwise DRAFT.
-- name: RestoreArticle :one
UPDATE articles
SET status     = CASE WHEN first_published_at IS NULL THEN 'DRAFT' ELSE 'WITHDRAWN' END,
    trashed_at = NULL,
    updated_at = now()
WHERE id = @id AND status = 'TRASHED'
RETURNING status;

-- ---- Public reads: always the publication snapshot of a PUBLISHED article. ----

-- Resolves current and historical slugs; resolved_from_old_slug drives the Nuxt 301 (API §5.1).
-- name: GetPublishedArticleBySlug :one
SELECT a.id, a.first_published_at, a.public_updated_at,
       p.publication_revision, p.title, p.summary, p.body_markdown, p.slug AS current_slug,
       p.category_id, p.cover_media_id, p.seo_title, p.seo_description, p.comments_enabled,
       (s.slug <> p.slug)::boolean AS resolved_from_old_slug
FROM article_slugs s
JOIN articles a ON a.id = s.article_id
JOIN article_publications p ON p.article_id = a.id
WHERE s.slug = @slug AND a.status = 'PUBLISHED';

-- name: ListPublishedArticles :many
SELECT a.id, p.slug, p.title, p.summary, a.first_published_at, a.public_updated_at
FROM articles a
JOIN article_publications p ON p.article_id = a.id
WHERE a.status = 'PUBLISHED'
ORDER BY a.first_published_at DESC, a.id DESC
LIMIT @page_limit OFFSET @page_offset;

-- name: ListPublicationTagSlugs :many
SELECT t.slug FROM article_publication_tags pt JOIN tags t ON t.id = pt.tag_id
WHERE pt.article_id = @article_id ORDER BY t.slug;

-- pattern is an ILIKE pattern built (and escaped) by the Go service.
-- name: SearchPublishedArticles :many
SELECT a.id, p.slug, p.title
FROM articles a
JOIN article_publications p ON p.article_id = a.id
WHERE a.status = 'PUBLISHED'
  AND (p.title ILIKE @pattern OR p.summary ILIKE @pattern OR p.body_plain_text ILIKE @pattern)
ORDER BY a.first_published_at DESC, a.id DESC
LIMIT @page_limit OFFSET @page_offset;

-- name: IncrementArticleViews :exec
UPDATE article_metrics SET view_count = view_count + 1, updated_at = now() WHERE article_id = @article_id;

-- name: GetArticleViews :one
SELECT view_count FROM article_metrics WHERE article_id = @article_id;
