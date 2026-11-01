-- ---- P8-B12: related articles and reader bookmarks (public snapshots of PUBLISHED articles only). ----
-- The article queries select exactly the columns of ListPublicArticles so the shared list writer can use them.

-- Up to @row_limit other public articles, most related first: shared tags weigh 2, the same category 1,
-- then newest first. Unrelated articles fill the remaining places so the list is never empty.
-- name: ListRelatedArticles :many
WITH src AS (
    SELECT p.category_id FROM article_publications p WHERE p.article_id = @article_id
), src_tags AS (
    SELECT pt.tag_id FROM article_publication_tags pt WHERE pt.article_id = @article_id
)
SELECT a.id, p.slug, p.title, p.summary, p.cover_media_id, p.comments_enabled,
       a.first_published_at, a.public_updated_at,
       c.id AS category_id, c.name AS category_name, c.slug AS category_slug,
       coalesce(m.view_count, 0)::bigint AS view_count,
       count(*) OVER ()::bigint AS total_count
FROM articles a
JOIN article_publications p ON p.article_id = a.id
LEFT JOIN categories c ON c.id = p.category_id
LEFT JOIN article_metrics m ON m.article_id = a.id
WHERE a.status = 'PUBLISHED' AND a.id <> @article_id
ORDER BY
  (2 * (SELECT count(*) FROM article_publication_tags pt WHERE pt.article_id = a.id AND pt.tag_id IN (SELECT tag_id FROM src_tags))
   + CASE WHEN p.category_id IS NOT NULL AND p.category_id = (SELECT category_id FROM src) THEN 1 ELSE 0 END) DESC,
  a.first_published_at DESC, a.id DESC
LIMIT @row_limit;

-- name: CountBookmarks :one
SELECT count(*)::bigint FROM article_bookmarks WHERE user_id = @user_id;

-- name: AddBookmark :execrows
INSERT INTO article_bookmarks (user_id, article_id) VALUES (@user_id, @article_id)
ON CONFLICT DO NOTHING;

-- name: DeleteBookmark :execrows
DELETE FROM article_bookmarks WHERE user_id = @user_id AND article_id = @article_id;

-- Bookmarked articles that are public right now, newest bookmark first. Withdrawn or trashed articles stay
-- bookmarked (they reappear when published again) but are not listed.
-- name: ListBookmarkedArticles :many
SELECT a.id, p.slug, p.title, p.summary, p.cover_media_id, p.comments_enabled,
       a.first_published_at, a.public_updated_at,
       c.id AS category_id, c.name AS category_name, c.slug AS category_slug,
       coalesce(m.view_count, 0)::bigint AS view_count,
       count(*) OVER ()::bigint AS total_count
FROM article_bookmarks b
JOIN articles a ON a.id = b.article_id
JOIN article_publications p ON p.article_id = a.id
LEFT JOIN categories c ON c.id = p.category_id
LEFT JOIN article_metrics m ON m.article_id = a.id
WHERE b.user_id = @user_id AND a.status = 'PUBLISHED'
ORDER BY b.created_at DESC, a.id DESC
LIMIT @page_limit OFFSET @page_offset;

-- IDs of the public bookmarked articles, so pages can mark bookmarked cards without per-card requests.
-- name: ListBookmarkedArticleIDs :many
SELECT b.article_id
FROM article_bookmarks b JOIN articles a ON a.id = b.article_id
WHERE b.user_id = @user_id AND a.status = 'PUBLISHED'
ORDER BY b.created_at DESC
LIMIT @row_limit;
