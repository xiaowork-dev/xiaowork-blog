-- name: CreateComment :one
INSERT INTO comments (id, article_id, author_id, parent_id, root_id, body_text)
VALUES (@id, @article_id, @author_id, @parent_id, @root_id, @body_text)
RETURNING *;

-- Public projection: only VISIBLE comments expose text (the self LEFT JOIN yields NULL otherwise);
-- hidden/deleted ones keep their place in the tree.
-- name: ListRootComments :many
SELECT c.id, c.author_id, c.status, c.created_at,
       v.body_text AS public_body
FROM comments c
LEFT JOIN comments v ON v.id = c.id AND v.status = 'VISIBLE'
WHERE c.article_id = @article_id AND c.parent_id IS NULL
ORDER BY c.created_at DESC, c.id DESC
LIMIT @page_limit OFFSET @page_offset;

-- name: ListRepliesForRoots :many
SELECT c.id, c.root_id, c.parent_id, c.author_id, c.status, c.created_at,
       v.body_text AS public_body
FROM comments c
LEFT JOIN comments v ON v.id = c.id AND v.status = 'VISIBLE'
WHERE c.article_id = @article_id AND c.root_id = ANY(@root_ids::uuid[])
ORDER BY c.root_id, c.created_at, c.id;

-- name: DeleteCommentByAuthor :execrows
UPDATE comments
SET status = 'DELETED_AUTHOR', body_text = NULL, deleted_at = now(), updated_at = now()
WHERE id = @id AND author_id = @author_id AND status IN ('VISIBLE', 'HIDDEN');

-- name: HideComment :execrows
UPDATE comments SET status = 'HIDDEN', hidden_at = now(), updated_at = now()
WHERE id = @id AND status = 'VISIBLE';

-- name: UnhideComment :execrows
UPDATE comments SET status = 'VISIBLE', hidden_at = NULL, updated_at = now()
WHERE id = @id AND status = 'HIDDEN';

-- name: GetSiteSettings :one
SELECT * FROM site_settings WHERE id = 1;

-- name: InsertAuditLog :exec
INSERT INTO admin_audit_logs (id, actor_user_id, action, object_type, object_key, request_id, details)
VALUES (@id, @actor_user_id, @action, @object_type, @object_key, @request_id, @details);
