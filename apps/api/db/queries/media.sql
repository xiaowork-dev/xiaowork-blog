-- Queries used by the P3 media service (no schema change).

-- name: GetMediaAsset :one
SELECT * FROM media_assets WHERE id = @id;

-- FOR UPDATE conflicts with the FOR KEY SHARE lock that every referencing INSERT takes on the media row,
-- so reference checks made under this lock cannot race with new references (DD T06).
-- name: LockMediaAsset :one
SELECT * FROM media_assets WHERE id = @id FOR UPDATE;

-- name: SetMediaPendingDelete :execrows
UPDATE media_assets SET state = 'PENDING_DELETE' WHERE id = @id AND state = 'READY';

-- name: RevertMediaReady :execrows
UPDATE media_assets SET state = 'READY' WHERE id = @id AND state = 'PENDING_DELETE';

-- name: MarkMediaDeleted :execrows
UPDATE media_assets SET state = 'DELETED', deleted_at = now() WHERE id = @id AND state = 'PENDING_DELETE';

-- name: ListPendingDeleteMedia :many
SELECT id FROM media_assets WHERE state = 'PENDING_DELETE' ORDER BY created_at LIMIT 100;

-- Every row whose file is expected on disk, for the consistency report.
-- name: ListStoredMedia :many
SELECT id, storage_key, state FROM media_assets WHERE state <> 'DELETED';

-- Media library page (UI/UX §7.7). pattern is an escaped ILIKE pattern or '%'.
-- name: ListMediaLibrary :many
SELECT m.id, m.usage_kind, m.original_filename, m.mime_type, m.size_bytes, m.width, m.height, m.state, m.created_at,
       m.uploaded_by, u.nickname AS uploader_nickname,
       ((SELECT count(*) FROM article_draft_media d WHERE d.media_id = m.id)
      + (SELECT count(*) FROM article_drafts d WHERE d.cover_media_id = m.id)
      + (SELECT count(*) FROM article_publication_media p WHERE p.media_id = m.id)
      + (SELECT count(*) FROM article_publications p WHERE p.cover_media_id = m.id)
      + (SELECT count(*) FROM user_profiles up WHERE up.avatar_media_id = m.id)
      + (SELECT count(*) FROM site_settings s WHERE m.id IN (s.profile_avatar_media_id, s.logo_media_id, s.favicon_media_id))
      + (SELECT count(*) FROM static_page_media sm WHERE sm.media_id = m.id))::bigint AS reference_count,
       count(*) OVER ()::bigint AS total_count
FROM media_assets m
JOIN users u ON u.id = m.uploaded_by
WHERE m.state = 'READY'
  AND m.original_filename ILIKE @pattern
  AND (@usage_kind::text = '' OR m.usage_kind = @usage_kind::text)
ORDER BY m.created_at DESC, m.id DESC
LIMIT @page_limit OFFSET @page_offset;

-- name: GetUserAvatarMedia :one
SELECT avatar_media_id FROM user_profiles WHERE user_id = @user_id;
