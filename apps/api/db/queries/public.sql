-- ---- P5 public discovery: only the publication snapshot of PUBLISHED articles (DD §11.1). ----
-- Lists never select body_markdown / body_plain_text.

-- category_id / tag_id are optional filters; oldest=true flips the order (API-DD §4.1 sort).
-- name: ListPublicArticles :many
SELECT a.id, p.slug, p.title, p.summary, p.cover_media_id, p.comments_enabled,
       a.first_published_at, a.public_updated_at,
       c.id AS category_id, c.name AS category_name, c.slug AS category_slug,
       coalesce(m.view_count, 0)::bigint AS view_count,
       count(*) OVER ()::bigint AS total_count
FROM articles a
JOIN article_publications p ON p.article_id = a.id
LEFT JOIN categories c ON c.id = p.category_id
LEFT JOIN article_metrics m ON m.article_id = a.id
WHERE a.status = 'PUBLISHED'
  AND (sqlc.narg(category_id)::uuid IS NULL OR p.category_id = sqlc.narg(category_id)::uuid)
  AND (sqlc.narg(tag_id)::uuid IS NULL OR EXISTS (
        SELECT 1 FROM article_publication_tags pt WHERE pt.article_id = a.id AND pt.tag_id = sqlc.narg(tag_id)::uuid))
ORDER BY
  CASE WHEN @oldest::boolean THEN a.first_published_at END ASC,
  CASE WHEN @oldest::boolean THEN a.id END ASC,
  a.first_published_at DESC, a.id DESC
LIMIT @page_limit OFFSET @page_offset;

-- Tags of one page of articles in a single round trip (no N+1).
-- name: ListPublicationTagsForArticles :many
SELECT pt.article_id, t.id, t.name, t.slug
FROM article_publication_tags pt JOIN tags t ON t.id = pt.tag_id
WHERE pt.article_id = ANY(@article_ids::uuid[])
ORDER BY pt.article_id, t.sort_order, t.name;

-- Every term must occur in the title, summary or plain-text body (terms are ILIKE patterns escaped by Go).
-- The first term is also tested per column so the pg_trgm GIN indexes of 00007 can pre-filter.
-- Title hits rank first, then summary hits, then newest first.
-- name: SearchPublicArticles :many
SELECT a.id, p.slug, p.title, p.summary, p.cover_media_id, p.comments_enabled,
       a.first_published_at, a.public_updated_at,
       c.id AS category_id, c.name AS category_name, c.slug AS category_slug,
       coalesce(m.view_count, 0)::bigint AS view_count,
       count(*) OVER ()::bigint AS total_count
FROM articles a
JOIN article_publications p ON p.article_id = a.id
LEFT JOIN categories c ON c.id = p.category_id
LEFT JOIN article_metrics m ON m.article_id = a.id
WHERE a.status = 'PUBLISHED'
  AND (p.title ILIKE @first_pattern OR p.summary ILIKE @first_pattern OR p.body_plain_text ILIKE @first_pattern)
  AND (p.title || E'\n' || p.summary || E'\n' || p.body_plain_text) ILIKE ALL (@patterns::text[])
ORDER BY
  (p.title ILIKE ALL (@patterns::text[])) DESC,
  (p.summary ILIKE ALL (@patterns::text[])) DESC,
  a.first_published_at DESC, a.id DESC
LIMIT @page_limit OFFSET @page_offset;

-- Active categories with the number of public articles in each (API-DD §4.1).
-- name: ListPublicCategories :many
SELECT c.id, c.name, c.slug, c.description,
       (SELECT count(*) FROM article_publications p JOIN articles a ON a.id = p.article_id
        WHERE p.category_id = c.id AND a.status = 'PUBLISHED')::bigint AS article_count
FROM categories c
WHERE c.is_active
ORDER BY c.sort_order, c.name, c.id;

-- name: GetCategoryBySlug :one
SELECT id, name, slug, description FROM categories WHERE slug = @slug;

-- name: ListPublicTags :many
SELECT t.id, t.name, t.slug,
       (SELECT count(*) FROM article_publication_tags pt JOIN articles a ON a.id = pt.article_id
        WHERE pt.tag_id = t.id AND a.status = 'PUBLISHED')::bigint AS article_count
FROM tags t
WHERE t.is_active
ORDER BY t.sort_order, t.name, t.id;

-- name: GetTagBySlug :one
SELECT id, name, slug FROM tags WHERE slug = @slug;

-- Archive buckets use first_published_at in the site time zone @tz (DISCOVER-03 "统一口径"), the same
-- zone the Nuxt pages display dates in, so an article never shows one date and sits in another month.
-- name: ListArchiveYears :many
SELECT extract(year FROM a.first_published_at AT TIME ZONE @tz::text)::int AS year, count(*)::bigint AS article_count
FROM articles a
WHERE a.status = 'PUBLISHED'
GROUP BY 1 ORDER BY 1 DESC;

-- name: ListArchiveArticles :many
SELECT a.id, p.slug, p.title, a.first_published_at,
       extract(year FROM a.first_published_at AT TIME ZONE @tz::text)::int AS year,
       extract(month FROM a.first_published_at AT TIME ZONE @tz::text)::int AS month
FROM articles a
JOIN article_publications p ON p.article_id = a.id
WHERE a.status = 'PUBLISHED'
  AND (sqlc.narg(year)::int IS NULL OR extract(year FROM a.first_published_at AT TIME ZONE @tz::text)::int = sqlc.narg(year)::int)
ORDER BY a.first_published_at DESC, a.id DESC
LIMIT @row_limit;

-- name: IsArticlePublished :one
SELECT EXISTS (SELECT 1 FROM articles WHERE id = @id AND status = 'PUBLISHED')::boolean;

-- ---- Site profile and about page (API-DD §4.1 /site, /pages/about) ----

-- name: ListEnabledSocialLinks :many
SELECT id, icon_key, label, url FROM site_social_links WHERE is_enabled ORDER BY sort_order, id;

-- name: ListEnabledNavigationLinks :many
SELECT id, label, link_type, url FROM site_navigation_links WHERE is_enabled ORDER BY sort_order, id;

-- name: GetStaticPage :one
SELECT page_key, title, body_markdown, updated_at FROM static_pages WHERE page_key = @page_key;
