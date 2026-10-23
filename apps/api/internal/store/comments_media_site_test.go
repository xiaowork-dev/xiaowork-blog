package store_test

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/xiaowork-dev/xiaowork-blog/apps/api/internal/platform/database/dbtest"
	"github.com/xiaowork-dev/xiaowork-blog/apps/api/internal/store"
)

// DB-11, DB-12, DB-13: same-article threading, tombstones keep the tree, hidden text is never public.
func TestCommentIntegrity(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.NewMigratedPool(t)
	q := store.New(pool)
	admin := newAdmin(t, pool)
	reader := newUser(t, q, "reader@example.com")
	articleA := createPublished(t, pool, admin.ID, "a", "A", "body")
	articleB := createPublished(t, pool, admin.ID, "b", "B", "body")

	comment := func(article uuid.UUID, parent, root uuid.NullUUID, body string) (store.Comment, error) {
		return q.CreateComment(ctx, store.CreateCommentParams{
			ID: uuid.New(), ArticleID: article, AuthorID: reader.ID, ParentID: parent, RootID: root, BodyText: text(body),
		})
	}

	root, err := comment(articleA, uuid.NullUUID{}, uuid.NullUUID{}, "root comment")
	if err != nil {
		t.Fatal(err)
	}
	if root.Status != "VISIBLE" {
		t.Fatalf("new comment status = %s, want VISIBLE (no review queue)", root.Status)
	}
	reply, err := comment(articleA, nullID(root.ID), nullID(root.ID), "reply")
	if err != nil {
		t.Fatal(err)
	}

	// Cross-article reply is rejected by the composite FK.
	_, err = comment(articleB, nullID(root.ID), nullID(root.ID), "cross")
	expectPgError(t, err, codeForeignKeyViolation, "comments_parent_same_article")
	// parent without root (or vice versa) is malformed.
	_, err = comment(articleA, nullID(root.ID), uuid.NullUUID{}, "half")
	expectPgError(t, err, codeCheckViolation, "comments_parent_root_pairing")
	// Empty and over-long bodies.
	_, err = comment(articleA, uuid.NullUUID{}, uuid.NullUUID{}, "   ")
	expectPgError(t, err, codeCheckViolation, "comments_body_when_present")
	_, err = comment(articleA, uuid.NullUUID{}, uuid.NullUUID{}, strings.Repeat("评", 2001))
	expectPgError(t, err, codeCheckViolation, "comments_body_when_present")
	// A comment cannot reply to itself.
	self := uuid.New()
	_, err = pool.Exec(ctx, `INSERT INTO comments (id, article_id, author_id, parent_id, root_id, body_text)
		VALUES ($1, $2, $3, $1, $1, 'self')`, self, articleA, reader.ID)
	expectPgError(t, err, codeCheckViolation, "comments_not_self_parent")

	// Only the author can delete; deletion is a tombstone and the reply survives.
	if n, _ := q.DeleteCommentByAuthor(ctx, store.DeleteCommentByAuthorParams{ID: root.ID, AuthorID: admin.ID}); n != 0 {
		t.Fatal("non-author deleted a comment")
	}
	if n, _ := q.DeleteCommentByAuthor(ctx, store.DeleteCommentByAuthorParams{ID: root.ID, AuthorID: reader.ID}); n != 1 {
		t.Fatal("author delete failed")
	}
	roots, err := q.ListRootComments(ctx, store.ListRootCommentsParams{ArticleID: articleA, PageLimit: 20, PageOffset: 0})
	if err != nil || len(roots) != 1 || roots[0].Status != "DELETED_AUTHOR" || roots[0].PublicBody.Valid {
		t.Fatalf("roots after delete = %+v, %v", roots, err)
	}
	replies, err := q.ListRepliesForRoots(ctx, store.ListRepliesForRootsParams{ArticleID: articleA, RootIds: []uuid.UUID{root.ID}})
	if err != nil || len(replies) != 1 || replies[0].ID != reply.ID || replies[0].PublicBody.String != "reply" {
		t.Fatalf("replies after parent delete = %+v, %v", replies, err)
	}
	// Tombstones cannot keep text.
	_, err = pool.Exec(ctx, `UPDATE comments SET status = 'DELETED_ADMIN', deleted_at = now() WHERE id = $1`, reply.ID)
	expectPgError(t, err, codeCheckViolation, "comments_tombstone_cleared")
	// No cascade: a comment with replies cannot be physically removed.
	_, err = pool.Exec(ctx, `DELETE FROM comments WHERE id = $1`, root.ID)
	expectPgError(t, err, codeForeignKeyViolation, "")

	// Hide hides the text from the public projection; unhide restores it.
	if n, _ := q.HideComment(ctx, reply.ID); n != 1 {
		t.Fatal("hide failed")
	}
	replies, _ = q.ListRepliesForRoots(ctx, store.ListRepliesForRootsParams{ArticleID: articleA, RootIds: []uuid.UUID{root.ID}})
	if replies[0].Status != "HIDDEN" || replies[0].PublicBody.Valid {
		t.Fatalf("hidden reply leaked: %+v", replies[0])
	}
	if n, _ := q.UnhideComment(ctx, reply.ID); n != 1 {
		t.Fatal("unhide failed")
	}
	replies, _ = q.ListRepliesForRoots(ctx, store.ListRepliesForRootsParams{ArticleID: articleA, RootIds: []uuid.UUID{root.ID}})
	if replies[0].Status != "VISIBLE" || replies[0].PublicBody.String != "reply" {
		t.Fatalf("unhidden reply = %+v", replies[0])
	}
}

// DB-14, DB-15: referenced media cannot be deleted through any channel; visibility follows public references.
func TestMediaReferencesAndVisibility(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.NewMigratedPool(t)
	q := store.New(pool)
	admin := newAdmin(t, pool)
	user := newUser(t, q, "avatar@example.com")

	draftCover := newMedia(t, q, admin.ID, "ARTICLE")
	id := newArticle(t, pool, admin.ID)
	if _, err := saveDraft(ctx, pool, id, admin.ID, 1, draftInput{Title: "t", Body: "b", Cover: nullID(draftCover)}); err != nil {
		t.Fatal(err)
	}
	refs, err := q.CountMediaReferences(ctx, draftCover)
	if err != nil || refs.DraftCoverRefs != 1 {
		t.Fatalf("refs = %+v, %v", refs, err)
	}
	assertMediaVisible(t, q, draftCover, false)
	if n, _ := q.MarkMediaPendingDelete(ctx, draftCover); n != 0 {
		t.Fatal("referenced draft cover moved to PENDING_DELETE")
	}
	_, err = pool.Exec(ctx, `DELETE FROM media_assets WHERE id = $1`, draftCover)
	expectPgError(t, err, codeForeignKeyViolation, "article_drafts_cover_media_id_fkey")

	avatar := newMedia(t, q, user.ID, "AVATAR")
	if err := q.SetUserAvatar(ctx, store.SetUserAvatarParams{UserID: user.ID, AvatarMediaID: nullID(avatar)}); err != nil {
		t.Fatal(err)
	}
	if refs, _ := q.CountMediaReferences(ctx, avatar); refs.AvatarRefs != 1 {
		t.Fatalf("avatar refs = %+v", refs)
	}
	if n, _ := q.MarkMediaPendingDelete(ctx, avatar); n != 0 {
		t.Fatal("avatar in use moved to PENDING_DELETE")
	}
	assertMediaVisible(t, q, avatar, true)

	logo := newMedia(t, q, admin.ID, "SITE")
	if _, err := pool.Exec(ctx, `UPDATE site_settings SET logo_media_id = $1`, logo); err != nil {
		t.Fatal(err)
	}
	if refs, _ := q.CountMediaReferences(ctx, logo); refs.SiteRefs != 1 {
		t.Fatalf("site refs = %+v", refs)
	}
	assertMediaVisible(t, q, logo, true)

	unused := newMedia(t, q, admin.ID, "ARTICLE")
	assertMediaVisible(t, q, unused, false)
	if n, _ := q.MarkMediaPendingDelete(ctx, unused); n != 1 {
		t.Fatal("unreferenced media could not move to PENDING_DELETE")
	}

	// Schema guards on media metadata.
	bad := func(storageKey, mime string) error {
		_, err := q.CreateMediaAsset(ctx, store.CreateMediaAssetParams{
			ID: uuid.New(), UploadedBy: admin.ID, UsageKind: "ARTICLE", StorageKey: storageKey,
			OriginalFilename: "x", MimeType: mime, SizeBytes: 1, Width: 1, Height: 1,
		})
		return err
	}
	expectPgError(t, bad("../etc/passwd", "image/png"), codeCheckViolation, "media_assets_storage_key_safe")
	expectPgError(t, bad("/abs/path.png", "image/png"), codeCheckViolation, "media_assets_storage_key_safe")
	expectPgError(t, bad("ok/file.svg", "image/svg+xml"), codeCheckViolation, "media_assets_mime_type_check")
	_, err = pool.Exec(ctx, `UPDATE media_assets SET state = 'DELETED' WHERE id = $1`, unused)
	expectPgError(t, err, codeCheckViolation, "media_assets_deleted_at_check")
}

func TestSiteAndAuditConstraints(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.NewMigratedPool(t)
	q := store.New(pool)
	admin := newAdmin(t, pool)

	settings, err := q.GetSiteSettings(ctx)
	if err != nil || settings.SiteTitle != "XiaoWork Blog" {
		t.Fatalf("seeded site settings = %+v, %v", settings, err)
	}
	_, err = pool.Exec(ctx, `INSERT INTO site_settings (id, site_title, profile_name) VALUES (2, 'x', 'y')`)
	expectPgError(t, err, codeCheckViolation, "site_settings_singleton")

	social := func(url string) error {
		_, err := pool.Exec(ctx, `INSERT INTO site_social_links (id, icon_key, label, url) VALUES ($1, 'github', 'GitHub', $2)`, uuid.New(), url)
		return err
	}
	if err := social("https://github.com/xiaowork-dev"); err != nil {
		t.Fatal(err)
	}
	if err := social("mailto:hello@example.com"); err != nil {
		t.Fatal(err)
	}
	expectPgError(t, social("javascript:alert(1)"), codeCheckViolation, "site_social_links_url_scheme")
	expectPgError(t, social("http://insecure.example.com"), codeCheckViolation, "site_social_links_url_scheme")

	nav := func(kind, url string) error {
		_, err := pool.Exec(ctx, `INSERT INTO site_navigation_links (id, label, link_type, url) VALUES ($1, 'nav', $2, $3)`, uuid.New(), kind, url)
		return err
	}
	if err := nav("INTERNAL", "/about"); err != nil {
		t.Fatal(err)
	}
	if err := nav("EXTERNAL", "https://example.com/docs"); err != nil {
		t.Fatal(err)
	}
	expectPgError(t, nav("INTERNAL", "//evil.example.com"), codeCheckViolation, "site_navigation_links_url_form")
	expectPgError(t, nav("EXTERNAL", "data:text/html,hi"), codeCheckViolation, "site_navigation_links_url_form")

	_, err = pool.Exec(ctx, `INSERT INTO static_pages (page_key, title, body_markdown, body_plain_text, updated_by)
		VALUES ('contact', 't', 'b', 'b', $1)`, admin.ID)
	expectPgError(t, err, codeCheckViolation, "static_pages_key_check")

	if err := q.InsertAuditLog(ctx, store.InsertAuditLogParams{
		ID: uuid.New(), ActorUserID: admin.ID, Action: "ARTICLE_PUBLISH", ObjectType: "article", ObjectKey: uuid.NewString(),
		RequestID: text("req_test"), Details: []byte(`{"publication_revision":1}`),
	}); err != nil {
		t.Fatal(err)
	}
	err = q.InsertAuditLog(ctx, store.InsertAuditLogParams{
		ID: uuid.New(), ActorUserID: admin.ID, Action: "X", ObjectType: "article", ObjectKey: "k", Details: []byte(`[1,2]`),
	})
	expectPgError(t, err, codeCheckViolation, "admin_audit_logs_details_object")
}
