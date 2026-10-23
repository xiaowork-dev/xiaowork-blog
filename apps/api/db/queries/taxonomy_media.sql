-- name: CreateCategory :one
INSERT INTO categories (id, name, slug) VALUES (@id, @name, @slug) RETURNING *;

-- name: CreateTag :one
INSERT INTO tags (id, name, slug) VALUES (@id, @name, @slug) RETURNING *;

-- name: CreateMediaAsset :one
INSERT INTO media_assets (id, uploaded_by, usage_kind, storage_key, original_filename, mime_type, size_bytes, width, height, sha256)
VALUES (@id, @uploaded_by, @usage_kind, @storage_key, @original_filename, @mime_type, @size_bytes, @width, @height, @sha256)
RETURNING *;

-- Every reference channel listed in DD §7.4/§7.5; deletion is refused while any count is non-zero.
-- name: CountMediaReferences :one
SELECT
    (SELECT count(*) FROM article_draft_media d WHERE d.media_id = @media_id::uuid)          AS draft_body_refs,
    (SELECT count(*) FROM article_drafts d WHERE d.cover_media_id = @media_id::uuid)         AS draft_cover_refs,
    (SELECT count(*) FROM article_publication_media p WHERE p.media_id = @media_id::uuid)    AS publication_body_refs,
    (SELECT count(*) FROM article_publications p WHERE p.cover_media_id = @media_id::uuid)   AS publication_cover_refs,
    (SELECT count(*) FROM user_profiles u WHERE u.avatar_media_id = @media_id::uuid)         AS avatar_refs,
    (SELECT count(*) FROM site_settings s
      WHERE @media_id::uuid IN (s.profile_avatar_media_id, s.logo_media_id, s.favicon_media_id)) AS site_refs,
    (SELECT count(*) FROM static_page_media m WHERE m.media_id = @media_id::uuid)           AS page_refs;

-- Anonymous visibility (DD §7.1, API §5.7): READY is necessary but not sufficient; the media must be
-- referenced by a PUBLISHED article snapshot, the site settings, the about page or an active user's avatar.
-- name: IsMediaPubliclyVisible :one
SELECT (EXISTS (SELECT 1 FROM media_assets m WHERE m.id = @media_id::uuid AND m.state = 'READY')
   AND EXISTS (
        SELECT 1 FROM article_publication_media pm JOIN articles a ON a.id = pm.article_id
        WHERE pm.media_id = @media_id::uuid AND a.status = 'PUBLISHED'
        UNION ALL
        SELECT 1 FROM article_publications p JOIN articles a ON a.id = p.article_id
        WHERE p.cover_media_id = @media_id::uuid AND a.status = 'PUBLISHED'
        UNION ALL
        SELECT 1 FROM site_settings s
        WHERE @media_id::uuid IN (s.profile_avatar_media_id, s.logo_media_id, s.favicon_media_id)
        UNION ALL
        SELECT 1 FROM static_page_media sm WHERE sm.media_id = @media_id::uuid
        UNION ALL
        SELECT 1 FROM user_profiles up JOIN users u ON u.id = up.user_id
        WHERE up.avatar_media_id = @media_id::uuid AND u.status = 'ACTIVE'
   ))::boolean AS visible;

-- First step of the delete state machine (DD T06): only unreferenced READY media can move to PENDING_DELETE.
-- name: MarkMediaPendingDelete :execrows
UPDATE media_assets m
SET state = 'PENDING_DELETE'
WHERE m.id = @media_id::uuid
  AND m.state = 'READY'
  AND NOT EXISTS (SELECT 1 FROM article_draft_media d WHERE d.media_id = m.id)
  AND NOT EXISTS (SELECT 1 FROM article_drafts d WHERE d.cover_media_id = m.id)
  AND NOT EXISTS (SELECT 1 FROM article_publication_media p WHERE p.media_id = m.id)
  AND NOT EXISTS (SELECT 1 FROM article_publications p WHERE p.cover_media_id = m.id)
  AND NOT EXISTS (SELECT 1 FROM user_profiles u WHERE u.avatar_media_id = m.id)
  AND NOT EXISTS (SELECT 1 FROM site_settings s WHERE m.id IN (s.profile_avatar_media_id, s.logo_media_id, s.favicon_media_id))
  AND NOT EXISTS (SELECT 1 FROM static_page_media sm WHERE sm.media_id = m.id);
