package article_test

// P6 comment and user-centre flows through the full router (auth, articles and comments together).

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func (c *client) comment(articleID, body string) response {
	c.h.t.Helper()
	return c.json(http.MethodPost, "/api/v1/articles/"+articleID+"/comments", map[string]any{"body_text": body})
}

func (c *client) reply(parentID, body string) response {
	c.h.t.Helper()
	return c.json(http.MethodPost, "/api/v1/comments/"+parentID+"/replies", map[string]any{"body_text": body})
}

func mustID(t *testing.T, r response) string {
	t.Helper()
	if r.Status != 201 {
		t.Fatalf("create comment: %d %s", r.Status, r.Body)
	}
	return r.data()["id"].(string)
}

func findComment(items []map[string]any, id string) map[string]any {
	for _, it := range items {
		if it["id"] == id {
			return it
		}
	}
	return nil
}

func TestCommentPostingAndPermissions(t *testing.T) {
	h := newHarness(t)
	admin := h.user("ADMIN")
	articleID := admin.publishOne("commented", "可评论文章", "正文", "2026-01-01T00:00:00Z", nil)
	listPath := "/api/v1/articles/" + articleID + "/comments"

	anon := h.anon()
	r := anon.get(listPath)
	if r.Status != 200 || len(list(r)) != 0 || meta(r)["comment_count"] != float64(0) || meta(r)["comments_enabled"] != true {
		t.Fatalf("empty list: %d %s", r.Status, r.Body)
	}

	// Who may not post: guests, unverified and disabled accounts (API-DD §3.5 "U").
	expect(t, anon.comment(articleID, "游客评论"), 401, "AUTH_REQUIRED")
	expect(t, h.account("USER", "未验证", false, "ACTIVE").comment(articleID, "未验证评论"), 403, "EMAIL_NOT_VERIFIED")
	expect(t, h.account("USER", "已禁用", true, "DISABLED").comment(articleID, "禁用评论"), 403, "ACCOUNT_DISABLED")
	forged := h.account("USER", "伪造", true, "ACTIVE")
	forged.csrf = "forged"
	expect(t, forged.comment(articleID, "没有 CSRF"), 403, "CSRF_INVALID")

	// A verified user posts: 201, VISIBLE immediately, no review state, no e-mail anywhere.
	alice := h.account("USER", "Alice", true, "ACTIVE")
	r = alice.comment(articleID, "  文章很有帮助，谢谢！👍🏻👨‍👩‍👧\r\n\r\n\r\n\r\n第二段 https://example.com/a?b=1  ")
	id := mustID(t, r)
	d := r.data()
	if d["status"] != "VISIBLE" || d["body_text"] != "文章很有帮助，谢谢！👍🏻👨‍👩‍👧\n\n第二段 https://example.com/a?b=1" ||
		d["author"].(map[string]any)["nickname"] != "Alice" || d["author"].(map[string]any)["is_blog_owner"] != false ||
		d["can_delete"] != true || d["reply_count"] != float64(0) {
		t.Fatalf("created: %v", d)
	}
	r = anon.get(listPath)
	got := findComment(list(r), id)
	if got == nil || got["body_text"] == nil || got["can_delete"] != false || meta(r)["comment_count"] != float64(1) {
		t.Fatalf("not public at once: %s", r.Body)
	}
	if strings.Contains(string(r.Body), "@example.test") || strings.Contains(string(r.Body), "session") {
		t.Fatalf("public comments leak account data: %s", r.Body)
	}

	// The blog owner is marked.
	ownerID := mustID(t, admin.comment(articleID, "博主回复"))
	if findComment(list(anon.get(listPath)), ownerID)["author"].(map[string]any)["is_blog_owner"] != true {
		t.Fatal("blog owner badge missing")
	}

	// Input rules: plain text only (HTML stays literal text), 1–2000 characters, control characters removed.
	r = alice.comment(articleID, `<script>alert(1)</script><img src=x onerror=alert(2)>`)
	if r.Status != 201 || r.data()["body_text"] != `<script>alert(1)</script><img src=x onerror=alert(2)>` ||
		!strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		t.Fatalf("html as text: %d %s", r.Status, r.Body)
	}
	r = alice.comment(articleID, "a\u202eb\u0000c\u200bd")
	if r.Status != 201 || r.data()["body_text"] != "abcd" {
		t.Fatalf("control characters: %s", r.Body)
	}
	expect(t, alice.comment(articleID, "   \n\t  "), 422, "VALIDATION_FAILED")
	expect(t, alice.comment(articleID, strings.Repeat("长", 2001)), 422, "VALIDATION_FAILED")
	if r := alice.comment(articleID, strings.Repeat("长", 2000)); r.Status != 201 {
		t.Fatalf("2000 characters: %d %s", r.Status, r.Body)
	}
	expect(t, alice.json(http.MethodPost, "/api/v1/articles/"+articleID+"/comments", map[string]any{}), 422, "VALIDATION_FAILED")
	expect(t, alice.json(http.MethodPost, "/api/v1/articles/"+articleID+"/comments", map[string]any{"body_text": "x", "html": "<b>"}), 400, "INVALID_REQUEST")
	// API-DD §7: 5 per minute per user; rejected input (422/400) is not counted.
	// Fixed one-minute windows: crossing a boundary mid-test allows at most two windows' worth.
	flood := h.account("USER", "Flood", true, "ACTIVE")
	posted := 0
	for posted <= 10 {
		if r = flood.comment(articleID, fmt.Sprintf("第 %d 条", posted+1)); r.Status != 201 {
			break
		}
		posted++
	}
	expect(t, r, 429, "RATE_LIMITED")
	if posted < 5 || posted > 10 || r.Header.Get("Retry-After") == "" {
		t.Fatalf("429 after %d comments, Retry-After %q", posted, r.Header.Get("Retry-After"))
	}

	bob := h.account("USER", "Bob", true, "ACTIVE")
	mustID(t, bob.comment(articleID, "相同内容"))
	expect(t, bob.comment(articleID, "相同内容"), 409, "DUPLICATE_COMMENT")
	expect(t, bob.comment(uuid.NewString(), "不存在的文章"), 404, "NOT_FOUND")
	expect(t, bob.comment("not-a-uuid", "坏 ID"), 404, "NOT_FOUND")
	expect(t, anon.get("/api/v1/articles/"+uuid.NewString()+"/comments"), 404, "NOT_FOUND")
	expect(t, anon.get(listPath+"?sort=hot"), 422, "VALIDATION_FAILED")
}

func TestCommentRepliesAndTree(t *testing.T) {
	h := newHarness(t)
	admin := h.user("ADMIN")
	articleID := admin.publishOne("thread", "楼中楼", "正文", "2026-01-01T00:00:00Z", nil)
	other := admin.publishOne("other", "另一篇", "正文", "2026-01-02T00:00:00Z", nil)
	alice, bob, carol := h.account("USER", "Alice", true, "ACTIVE"), h.account("USER", "Bob", true, "ACTIVE"), h.account("USER", "Carol", true, "ACTIVE")

	root := mustID(t, alice.comment(articleID, "根评论"))
	r1 := bob.reply(root, "@Alice 同意")
	p1 := mustID(t, r1)
	if r1.data()["root_id"] != root || r1.data()["parent_id"] != root || r1.data()["reply_to"].(map[string]any)["nickname"] != "Alice" {
		t.Fatalf("reply to root: %v", r1.data())
	}
	r2 := carol.reply(p1, "@Bob 楼中楼")
	p2 := mustID(t, r2)
	// The real parent is kept; the thread root stays the top-level comment (two visual levels only).
	if r2.data()["root_id"] != root || r2.data()["parent_id"] != p1 || r2.data()["reply_to"].(map[string]any)["nickname"] != "Bob" {
		t.Fatalf("nested reply: %v", r2.data())
	}
	p3 := mustID(t, alice.reply(p2, "第三条回复"))
	mustID(t, alice.comment(articleID, "第二个根评论"))

	r := h.anon().get("/api/v1/articles/" + articleID + "/comments?sort=oldest")
	items := list(r)
	if len(items) != 2 || items[0]["id"] != root || meta(r)["total"] != float64(2) || meta(r)["comment_count"] != float64(5) {
		t.Fatalf("roots: %s", r.Body)
	}
	if items[0]["reply_count"] != float64(3) || len(items[0]["replies"].([]any)) != 2 {
		t.Fatalf("reply preview: %v", items[0])
	}
	r = h.anon().get("/api/v1/articles/" + articleID + "/comments")
	if list(r)[0]["body_text"] != "第二个根评论" {
		t.Fatalf("latest first: %s", r.Body)
	}
	r = h.anon().get("/api/v1/comments/" + root + "/replies?page_size=2&page=2")
	if got := list(r); len(got) != 1 || got[0]["id"] != p3 || meta(r)["total"] != float64(3) {
		t.Fatalf("reply page 2: %s", r.Body)
	}
	expect(t, h.anon().get("/api/v1/comments/"+p1+"/replies"), 404, "NOT_FOUND") // not a root

	// Cross-article replies are impossible: the API derives the article from the parent, and the
	// composite foreign keys reject any row whose parent/root belongs to another article (DB-11).
	_, err := h.pool.Exec(context.Background(), `INSERT INTO comments (id, article_id, author_id, parent_id, root_id, body_text)
		VALUES ($1, $2, $3, $4, $4, 'cross')`, uuid.New(), other, alice.id, root)
	if err == nil || !strings.Contains(err.Error(), "comments_parent_same_article") {
		t.Fatalf("cross-article reply accepted: %v", err)
	}
	expect(t, alice.reply(uuid.NewString(), "回复不存在"), 404, "NOT_FOUND")
}

func TestCommentDeletionAndModeration(t *testing.T) {
	h := newHarness(t)
	admin := h.user("ADMIN")
	articleID := admin.publishOne("moderated", "治理", "正文", "2026-01-01T00:00:00Z", nil)
	alice, bob := h.account("USER", "Alice", true, "ACTIVE"), h.account("USER", "Bob", true, "ACTIVE")
	root := mustID(t, alice.comment(articleID, "将被作者删除的根评论"))
	reply := mustID(t, bob.reply(root, "违规回复：秘密原文"))
	nested := mustID(t, alice.reply(reply, "回复违规内容"))
	listPath := "/api/v1/articles/" + articleID + "/comments"

	// Only the author deletes; others get 403, admins use the admin endpoint.
	expect(t, bob.json(http.MethodDelete, "/api/v1/comments/"+root, nil), 403, "FORBIDDEN")
	expect(t, bob.json(http.MethodDelete, "/api/v1/comments/"+uuid.NewString(), nil), 404, "NOT_FOUND")
	expect(t, h.anon().json(http.MethodDelete, "/api/v1/comments/"+root, nil), 401, "AUTH_REQUIRED")
	expect(t, alice.json(http.MethodDelete, "/api/v1/comments/"+root, nil), 204, "")
	expect(t, alice.json(http.MethodDelete, "/api/v1/comments/"+root, nil), 204, "") // idempotent

	// The tombstone keeps the tree: replies are still listed under it.
	r := h.anon().get(listPath)
	tomb := findComment(list(r), root)
	if tomb["status"] != "DELETED_AUTHOR" || tomb["body_text"] != nil || tomb["author"] != nil || tomb["reply_count"] != float64(2) {
		t.Fatalf("tombstone: %v", tomb)
	}
	if strings.Contains(string(r.Body), "将被作者删除") {
		t.Fatal("deleted text still public")
	}
	replies := list(h.anon().get("/api/v1/comments/" + root + "/replies"))
	if len(replies) != 2 || replies[0]["reply_to"].(map[string]any)["nickname"] != nil {
		t.Fatalf("replies under tombstone: %v", replies)
	}
	expect(t, bob.reply(root, "回复已删除的评论"), 409, "COMMENT_NOT_REPLYABLE")

	// Users cannot moderate.
	expect(t, bob.json(http.MethodPost, "/api/v1/admin/comments/"+reply+"/hide", nil), 403, "FORBIDDEN")
	expect(t, bob.get("/api/v1/admin/comments"), 403, "FORBIDDEN")

	// Hide: the text disappears from every public response; replying is refused; unhide restores it.
	r = admin.json(http.MethodPost, "/api/v1/admin/comments/"+reply+"/hide", nil)
	if r.Status != 200 || r.data()["status"] != "HIDDEN" {
		t.Fatalf("hide: %s", r.Body)
	}
	if r := admin.json(http.MethodPost, "/api/v1/admin/comments/"+reply+"/hide", nil); r.data()["status"] != "HIDDEN" {
		t.Fatal("repeat hide")
	}
	for _, path := range []string{listPath, "/api/v1/comments/" + root + "/replies"} {
		r := h.anon().get(path)
		if strings.Contains(string(r.Body), "秘密原文") {
			t.Fatalf("hidden text leaked by %s: %s", path, r.Body)
		}
	}
	hidden := findComment(list(h.anon().get("/api/v1/comments/"+root+"/replies")), reply)
	if hidden["status"] != "HIDDEN" || hidden["body_text"] != nil || hidden["author"] != nil {
		t.Fatalf("hidden placeholder: %v", hidden)
	}
	if n := findComment(list(h.anon().get("/api/v1/comments/"+root+"/replies")), nested); n["reply_to"].(map[string]any)["nickname"] != nil {
		t.Fatal("reply target name of a hidden comment leaked")
	}
	expect(t, alice.reply(reply, "回复隐藏评论"), 409, "COMMENT_NOT_REPLYABLE")
	// The author still sees their own hidden text in the user centre.
	mine := list(bob.get("/api/v1/me/comments"))
	if mine[0]["status"] != "HIDDEN" || mine[0]["body_text"] != "违规回复：秘密原文" {
		t.Fatalf("own hidden comment: %v", mine)
	}

	// Admin list: filters by status, article, author, keyword and time; shows e-mail to admins only.
	r = admin.get("/api/v1/admin/comments?status=HIDDEN")
	if got := list(r); len(got) != 1 || got[0]["id"] != reply || got[0]["body_text"] != "违规回复：秘密原文" ||
		!strings.HasSuffix(got[0]["author"].(map[string]any)["email"].(string), "@example.test") ||
		got[0]["article"].(map[string]any)["canonical_path"] != "/posts/moderated" {
		t.Fatalf("admin list hidden: %s", r.Body)
	}
	if n := len(list(admin.get("/api/v1/admin/comments?author_id=" + alice.id.String()))); n != 2 {
		t.Fatalf("author filter: %d", n)
	}
	if n := len(list(admin.get("/api/v1/admin/comments?article_id=" + articleID + "&status=DELETED"))); n != 1 {
		t.Fatalf("deleted filter: %d", n)
	}
	if n := len(list(admin.get("/api/v1/admin/comments?q=" + "%E7%A7%98%E5%AF%86"))); n != 1 {
		t.Fatalf("keyword filter: %d", n)
	}
	future := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	if n := len(list(admin.get("/api/v1/admin/comments?from=" + future))); n != 0 {
		t.Fatalf("time filter: %d", n)
	}
	expect(t, admin.get("/api/v1/admin/comments?status=PENDING"), 422, "VALIDATION_FAILED")

	r = admin.json(http.MethodPost, "/api/v1/admin/comments/"+reply+"/unhide", nil)
	if r.Status != 200 || r.data()["status"] != "VISIBLE" || !strings.Contains(string(h.anon().get("/api/v1/comments/"+root+"/replies").Body), "秘密原文") {
		t.Fatalf("unhide: %s", r.Body)
	}
	// Admin delete is final: text gone, replies kept, cannot be unhidden.
	expect(t, admin.json(http.MethodDelete, "/api/v1/admin/comments/"+reply, nil), 204, "")
	expect(t, admin.json(http.MethodPost, "/api/v1/admin/comments/"+reply+"/unhide", nil), 409, "COMMENT_STATE_CONFLICT")
	if v := h.scalar(`SELECT body_text IS NULL AND status = 'DELETED_ADMIN' FROM comments WHERE id = $1`, reply); v != true {
		t.Fatal("admin delete did not clear the text")
	}
	if n := len(list(h.anon().get("/api/v1/comments/" + root + "/replies"))); n != 2 {
		t.Fatalf("tree broken after admin delete: %d", n)
	}
	if n := h.scalar(`SELECT count(*) FROM admin_audit_logs WHERE object_type = 'comment'`).(int64); n != 3 {
		t.Fatalf("audit entries: %d", n)
	}
	if v := h.scalar(`SELECT count(*) FROM admin_audit_logs WHERE object_type = 'comment' AND details::text LIKE '%秘密%'`).(int64); v != 0 {
		t.Fatal("audit log stores comment text")
	}

	// A hidden or deleted comment without replies is not listed publicly at all (no empty placeholders);
	// its author still sees it in the user centre.
	lone := mustID(t, alice.comment(articleID, "没有回复的评论"))
	quiet := mustID(t, bob.comment(articleID, "将被隐藏且没有回复"))
	expect(t, alice.json(http.MethodDelete, "/api/v1/comments/"+lone, nil), 204, "")
	admin.json(http.MethodPost, "/api/v1/admin/comments/"+quiet+"/hide", nil)
	r = h.anon().get(listPath)
	if findComment(list(r), lone) != nil || findComment(list(r), quiet) != nil || meta(r)["total"] != float64(1) {
		t.Fatalf("childless placeholders listed: %s", r.Body)
	}
	if findComment(list(alice.get("/api/v1/me/comments")), lone)["status"] != "DELETED_AUTHOR" {
		t.Fatal("own deleted comment missing from my comments")
	}
	// The admin-deleted reply had a reply of its own, so it stays as a placeholder; root reply_count counts it.
	if root := findComment(list(h.anon().get(listPath)), root); root["reply_count"] != float64(2) {
		t.Fatalf("reply_count with placeholders: %v", root["reply_count"])
	}
}

func TestCommentsClosedAndUnpublished(t *testing.T) {
	h := newHarness(t)
	admin := h.user("ADMIN")
	articleID := admin.publishOne("closing", "关闭评论", "正文", "2026-01-01T00:00:00Z", nil)
	alice := h.account("USER", "Alice", true, "ACTIVE")
	root := mustID(t, alice.comment(articleID, "关闭前的评论"))

	// Close comments through a published update (the public snapshot decides).
	detail := admin.get("/api/v1/admin/articles/" + articleID).data()
	rev := int(detail["draft"].(map[string]any)["revision"].(float64))
	rev = admin.mustSave(articleID, rev, newDraft("关闭评论", "正文").with("candidate_slug", "closing").with("comments_enabled", false))
	if r := admin.publish(articleID, rev); r.Status != 200 {
		t.Fatalf("publish closed: %s", r.Body)
	}
	r := h.anon().get("/api/v1/articles/" + articleID + "/comments")
	if r.Status != 200 || meta(r)["comments_enabled"] != false || findComment(list(r), root)["body_text"] != "关闭前的评论" {
		t.Fatalf("history after closing: %s", r.Body)
	}
	expect(t, alice.comment(articleID, "关闭后的评论"), 422, "COMMENT_CLOSED")
	expect(t, alice.reply(root, "关闭后的回复"), 422, "COMMENT_CLOSED")

	// Withdrawn: nothing about the comments is reachable publicly.
	admin.json(http.MethodPost, "/api/v1/admin/articles/"+articleID+"/withdraw", nil)
	expect(t, h.anon().get("/api/v1/articles/"+articleID+"/comments"), 404, "NOT_FOUND")
	expect(t, h.anon().get("/api/v1/comments/"+root+"/replies"), 404, "NOT_FOUND")
	expect(t, alice.reply(root, "撤回后回复"), 404, "NOT_FOUND")
	mine := list(alice.get("/api/v1/me/comments"))
	art := mine[0]["article"].(map[string]any)
	if art["available"] != false || art["title"] != nil || art["canonical_path"] != nil || mine[0]["body_text"] != "关闭前的评论" {
		t.Fatalf("my comment on a withdrawn article: %v", mine[0])
	}
	// Trashed and never-published articles behave the same.
	admin.json(http.MethodDelete, "/api/v1/admin/articles/"+articleID, nil)
	expect(t, h.anon().get("/api/v1/articles/"+articleID+"/comments"), 404, "NOT_FOUND")
	draft := admin.create()
	expect(t, alice.comment(draft, "草稿评论"), 404, "NOT_FOUND")
	// The author can still delete their comment on a withdrawn article.
	expect(t, alice.json(http.MethodDelete, "/api/v1/comments/"+root, nil), 204, "")
}

func TestMyCommentsAndProfile(t *testing.T) {
	h := newHarness(t)
	admin := h.user("ADMIN")
	articleID := admin.publishOne("mine", "我的评论所在文章", "正文", "2026-01-01T00:00:00Z", nil)
	alice := h.account("USER", "Alice", true, "ACTIVE")
	for i := 0; i < 3; i++ {
		mustID(t, alice.comment(articleID, fmt.Sprintf("第 %d 条", i)))
	}
	r := alice.get("/api/v1/me/comments?page_size=2")
	if got := list(r); len(got) != 2 || meta(r)["total"] != float64(3) || got[0]["body_text"] != "第 2 条" ||
		got[0]["article"].(map[string]any)["canonical_path"] != "/posts/mine" || got[0]["article"].(map[string]any)["title"] != "我的评论所在文章" {
		t.Fatalf("my comments: %s", r.Body)
	}
	if n := len(list(h.account("USER", "Bob", true, "ACTIVE").get("/api/v1/me/comments"))); n != 0 {
		t.Fatalf("someone else's comments listed: %d", n)
	}
	expect(t, h.anon().get("/api/v1/me/comments"), 401, "AUTH_REQUIRED")

	// Profile: nickname and bio; the new nickname shows on existing comments.
	r = alice.json(http.MethodPatch, "/api/v1/me/profile", map[string]any{"nickname": "  爱丽丝  ", "bio": "写代码\n也写字"})
	if r.Status != 200 || r.data()["nickname"] != "爱丽丝" || r.data()["bio"] != "写代码\n也写字" {
		t.Fatalf("profile: %d %s", r.Status, r.Body)
	}
	me := alice.get("/api/v1/auth/me").data()
	if me["nickname"] != "爱丽丝" || me["bio"] != "写代码\n也写字" {
		t.Fatalf("me: %v", me)
	}
	if list(h.anon().get("/api/v1/articles/" + articleID + "/comments"))[0]["author"].(map[string]any)["nickname"] != "爱丽丝" {
		t.Fatal("comment author nickname not updated")
	}
	r = alice.json(http.MethodPatch, "/api/v1/me/profile", map[string]any{"bio": nil})
	if r.Status != 200 || r.data()["bio"] != "" || r.data()["nickname"] != "爱丽丝" {
		t.Fatalf("clear bio: %s", r.Body)
	}
	expect(t, alice.json(http.MethodPatch, "/api/v1/me/profile", map[string]any{"nickname": ""}), 422, "VALIDATION_FAILED")
	expect(t, alice.json(http.MethodPatch, "/api/v1/me/profile", map[string]any{"nickname": strings.Repeat("名", 41)}), 422, "VALIDATION_FAILED")
	expect(t, alice.json(http.MethodPatch, "/api/v1/me/profile", map[string]any{"bio": strings.Repeat("字", 201)}), 422, "VALIDATION_FAILED")
	expect(t, alice.json(http.MethodPatch, "/api/v1/me/profile", map[string]any{"bio": "a\u202eb"}), 422, "VALIDATION_FAILED")
	expect(t, alice.json(http.MethodPatch, "/api/v1/me/profile", map[string]any{"email": "x@example.test"}), 400, "INVALID_REQUEST")
	expect(t, h.anon().json(http.MethodPatch, "/api/v1/me/profile", map[string]any{"bio": "x"}), 401, "AUTH_REQUIRED")

	// A revoked session loses the user centre at once (SESSION_EXPIRED, cookie cleared).
	h.exec(`UPDATE auth_sessions SET revoked_at = now() WHERE user_id = $1`, alice.id)
	expect(t, alice.json(http.MethodPatch, "/api/v1/me/profile", map[string]any{"bio": "x"}), 401, "SESSION_EXPIRED")
	expect(t, alice.get("/api/v1/me/comments"), 401, "AUTH_REQUIRED") // the stale cookie was cleared
}
