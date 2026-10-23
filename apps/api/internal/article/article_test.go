package article_test

import (
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
)

// 1. New article is a draft; autosave increments the revision; the full-snapshot contract is enforced.
func TestCreateAndAutosave(t *testing.T) {
	h := newHarness(t)
	admin := h.user("ADMIN")
	id := admin.create()

	r := admin.get("/api/v1/admin/articles/" + id)
	if r.Status != 200 || r.data()["status"] != "DRAFT" || r.data()["current_publication"] != nil ||
		r.data()["has_unpublished_changes"] != true || r.data()["draft"].(map[string]any)["revision"] != float64(1) {
		t.Fatalf("new article: %d %s", r.Status, r.Body)
	}
	rev := admin.mustSave(id, 1, newDraft("", "")) // empty title/body may be saved before publishing
	rev = admin.mustSave(id, rev, newDraft("草稿标题", "第一段"))
	if rev != 3 {
		t.Fatalf("revision = %d, want 3", rev)
	}
	d := admin.get("/api/v1/admin/articles/" + id).data()["draft"].(map[string]any)
	if d["title"] != "草稿标题" || d["body_markdown"] != "第一段" {
		t.Fatalf("draft = %v", d)
	}

	partial := newDraft("x", "y")
	delete(partial, "seo_title")
	r = admin.save(id, rev, partial)
	expect(t, r, 400, "INVALID_REQUEST")
	if f := r.details()["fields"].(map[string]any); f["seo_title"] == nil {
		t.Fatalf("missing-field details: %v", r.details())
	}
	expect(t, admin.save(id, rev, newDraft("x", "y").with("author_id", "evil")), 400, "INVALID_REQUEST")
	expect(t, admin.save(id, rev, newDraft(strings.Repeat("长", 201), "y")), 422, "VALIDATION_FAILED")
	expect(t, admin.save(id, rev, newDraft("x", "y").with("candidate_slug", "Bad Slug!")), 422, "VALIDATION_FAILED")
	expect(t, admin.save(id, rev, newDraft("x", strings.Repeat("a", 2<<20+1))), 422, "VALIDATION_FAILED") // just over: field error
	expect(t, admin.save(id, rev, newDraft("x", strings.Repeat("a", 3<<20))), 413, "PAYLOAD_TOO_LARGE")   // request too large
	expect(t, admin.save(uuid.NewString(), 1, newDraft("x", "y")), 404, "NOT_FOUND")

	// Admin list shows it with its draft state.
	list := admin.get("/api/v1/admin/articles?status=DRAFT&q=草稿")
	items := list.JSON["data"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["has_unpublished_changes"] != true {
		t.Fatalf("list: %s", list.Body)
	}
}

// 2. Two editor windows on the same revision: one wins, the other gets 409 with the current revision.
func TestRevisionConflict(t *testing.T) {
	h := newHarness(t)
	admin := h.user("ADMIN")
	id := admin.create()
	results := make([]response, 2)
	var wg sync.WaitGroup
	for i, title := range []string{"窗口 A", "窗口 B"} {
		wg.Add(1)
		go func(i int, title string) {
			defer wg.Done()
			results[i] = admin.save(id, 1, newDraft(title, "body"))
		}(i, title)
	}
	wg.Wait()
	ok, conflict := 0, 0
	winner := ""
	for i, r := range results {
		switch {
		case r.Status == 200:
			ok++
			winner = []string{"窗口 A", "窗口 B"}[i]
		case r.Status == 409 && r.code() == "DRAFT_REVISION_CONFLICT" && r.details()["current_revision"] == float64(2):
			conflict++
		default:
			t.Fatalf("unexpected: %d %s", r.Status, r.Body)
		}
	}
	if ok != 1 || conflict != 1 {
		t.Fatalf("ok=%d conflict=%d", ok, conflict)
	}
	// A late request with the old revision cannot overwrite the newer draft.
	expect(t, admin.save(id, 1, newDraft("迟到的保存", "x")), 409, "DRAFT_REVISION_CONFLICT")
	if got := admin.get("/api/v1/admin/articles/" + id).data()["draft"].(map[string]any)["title"]; got != winner {
		t.Fatalf("title = %v, want %s", got, winner)
	}
	// Publishing with a stale revision is refused too.
	expect(t, admin.publish(id, 1), 409, "DRAFT_REVISION_CONFLICT")
}

// 3–5 & 7–8. First publish, idempotent repeat, draft edits never leak, publish update switches everything
// atomically (content, category, tags, cover, media, slug, SEO, comments), comments keep their article.
func TestPublishIsolationAndUpdate(t *testing.T) {
	h := newHarness(t)
	admin := h.user("ADMIN")
	catA, catB := admin.category("开发笔记", "dev"), admin.category("生活", "life")
	tagGo, tagDocker := admin.tag("Go", "go"), admin.tag("Docker", "docker")
	img1, img2 := admin.uploadPNG("one.png", 40, 30), admin.uploadPNG("two.png", 40, 30)
	cover1, cover2 := admin.uploadPNG("cover1.png", 64, 64), admin.uploadPNG("cover2.png", 64, 64)

	id := admin.create()
	v1 := newDraft("Hello V1", "# V1\n\n![图](/media/"+img1+"/file)\n\n正文 v1 `code`").
		with("candidate_slug", "hello").with("category_id", catA).with("tag_ids", []string{tagGo}).
		with("cover_media_id", cover1).with("seo_title", "SEO v1").with("summary", "摘要 v1")
	rev := admin.mustSave(id, 1, v1)

	// Unpublished: nothing is public, draft media is private.
	expect(t, h.public("hello"), 404, "NOT_FOUND")
	expect(t, h.anon().get("/media/"+img1+"/file"), 404, "NOT_FOUND")
	if admin.get("/media/"+img1+"/file").Status != 200 {
		t.Fatal("admin cannot preview the draft image")
	}

	r := admin.publish(id, rev)
	if r.Status != 200 || r.data()["published_changed"] != true || r.data()["publication_revision"] != float64(1) ||
		r.data()["canonical_path"] != "/posts/hello" {
		t.Fatalf("first publish: %d %s", r.Status, r.Body)
	}
	firstAt := r.data()["published_at"]
	pub := h.public("hello").data()
	if pub["title"] != "Hello V1" || pub["summary"] != "摘要 v1" || pub["category"].(map[string]any)["slug"] != "dev" ||
		pub["cover"].(map[string]any)["id"] != cover1 || pub["seo_title"] != "SEO v1" || pub["comments_enabled"] != true {
		t.Fatalf("public v1: %v", pub)
	}
	if h.anon().get("/media/"+img1+"/file").Status != 200 || h.anon().get("/media/"+cover1+"/file").Status != 200 {
		t.Fatal("published media not public")
	}
	var plain string
	if err := h.pool.QueryRow(t.Context(), `SELECT body_plain_text FROM article_publications WHERE article_id = $1`, id).Scan(&plain); err != nil ||
		!strings.Contains(plain, "正文 v1") || strings.Contains(plain, "![") || strings.Contains(plain, "#") {
		t.Fatalf("body_plain_text = %q (%v)", plain, err)
	}

	// Repeat: business no-op.
	r = admin.publish(id, rev)
	if r.Status != 200 || r.data()["published_changed"] != false || r.data()["publication_revision"] != float64(1) || r.data()["published_at"] != firstAt {
		t.Fatalf("repeat publish: %s", r.Body)
	}

	// A comment bound to the stable article id.
	h.exec(`INSERT INTO comments (id, article_id, author_id, body_text) VALUES ($1, $2, $3, 'nice')`, uuid.New(), id, admin.id)

	// Edit every publish-relevant field in the draft.
	v2 := newDraft("Hello V2", "# V2\n\n![图](/media/"+img2+"/file)\n\n正文 v2").
		with("candidate_slug", "hello-v2").with("category_id", catB).with("tag_ids", []string{tagDocker}).
		with("cover_media_id", cover2).with("seo_title", "SEO v2").with("comments_enabled", false)
	rev = admin.mustSave(id, rev, v2)
	if admin.get("/api/v1/admin/articles/" + id).data()["has_unpublished_changes"] != true {
		t.Fatal("unpublished changes not reported")
	}
	pub = h.public("hello").data()
	if pub["title"] != "Hello V1" || !strings.Contains(pub["body_markdown"].(string), "正文 v1") ||
		pub["category"].(map[string]any)["slug"] != "dev" || pub["cover"].(map[string]any)["id"] != cover1 ||
		pub["seo_title"] != "SEO v1" || pub["comments_enabled"] != true || pub["slug"] != "hello" {
		t.Fatalf("draft edit leaked into the public version: %v", pub)
	}
	if tags := pub["tags"].([]any); len(tags) != 1 || tags[0].(map[string]any)["slug"] != "go" {
		t.Fatalf("public tags changed: %v", tags)
	}
	expect(t, h.public("hello-v2"), 404, "NOT_FOUND")
	expect(t, h.anon().get("/media/"+img2+"/file"), 404, "NOT_FOUND")
	expect(t, h.anon().get("/media/"+cover2+"/file"), 404, "NOT_FOUND")

	// Publish the update: everything switches together.
	r = admin.publish(id, rev)
	if r.Status != 200 || r.data()["publication_revision"] != float64(2) || r.data()["slug"] != "hello-v2" {
		t.Fatalf("publish update: %s", r.Body)
	}
	pub = h.public("hello-v2").data()
	if pub["title"] != "Hello V2" || pub["category"].(map[string]any)["slug"] != "life" || pub["cover"].(map[string]any)["id"] != cover2 ||
		pub["comments_enabled"] != false || pub["first_published_at"] != firstAt || pub["id"] != id {
		t.Fatalf("public v2: %v", pub)
	}
	if tags := pub["tags"].([]any); len(tags) != 1 || tags[0].(map[string]any)["slug"] != "docker" {
		t.Fatalf("public tags after update: %v", tags)
	}
	if h.anon().get("/media/"+img2+"/file").Status != 200 || h.anon().get("/media/"+img1+"/file").Status != 404 {
		t.Fatal("public media did not switch with the publication")
	}
	// Old slug resolves to the canonical one (the Nuxt page issues the 301).
	old := h.public("hello").data()
	if old["resolved_from_old_slug"] != true || old["canonical_path"] != "/posts/hello-v2" {
		t.Fatalf("old slug: %v", old)
	}
	if n := h.scalar(`SELECT count(*) FROM comments WHERE article_id = $1`, id); n != int64(1) {
		t.Fatalf("comments after update = %v", n)
	}
	if n := h.scalar(`SELECT count(*) FROM admin_audit_logs WHERE action = 'ARTICLE_PUBLISH' AND object_key = $1`, id); n != int64(2) {
		t.Fatalf("publish audit entries = %v, want 2 (repeat publish is not audited)", n)
	}

	// Media protection: a published image cannot be deleted; the old one, no longer referenced, can.
	expect(t, admin.raw(http.MethodDelete, "/api/v1/admin/media/"+img2, nil, ""), 409, "RESOURCE_IN_USE")
	if d := admin.raw(http.MethodDelete, "/api/v1/admin/media/"+img1, nil, ""); d.Status != 204 {
		t.Fatalf("delete unreferenced image: %d %s", d.Status, d.Body)
	}
	// Referencing deleted or unknown media is refused at save time.
	r = admin.save(id, rev, v2.with("body_markdown", "![x](/media/"+img1+"/file)"))
	expect(t, r, 422, "MEDIA_NOT_READY")
	expect(t, admin.save(id, rev, v2.with("cover_media_id", uuid.NewString())), 422, "MEDIA_NOT_READY")
	expect(t, admin.save(id, rev, v2.with("category_id", uuid.NewString())), 422, "VALIDATION_FAILED")
	expect(t, admin.save(id, rev, v2.with("tag_ids", []string{uuid.NewString()})), 422, "VALIDATION_FAILED")
}

// 6. Slug conflicts (current and historical) roll the whole publish back; concurrent same-slug publishes
// let exactly one win; missing slugs are generated.
func TestSlugRulesAndRollback(t *testing.T) {
	h := newHarness(t)
	admin := h.user("ADMIN")
	owner := admin.create()
	rev := admin.mustSave(owner, 1, newDraft("Owner", "body").with("candidate_slug", "taken"))
	admin.publish(owner, rev)
	rev = admin.mustSave(owner, rev, newDraft("Owner", "body").with("candidate_slug", "owner-new"))
	admin.publish(owner, rev) // "taken" is now historical

	tag := admin.tag("Rust", "rust")
	other := admin.create()
	orev := admin.mustSave(other, 1, newDraft("Other", "body").with("candidate_slug", "taken").with("tag_ids", []string{tag}))
	expect(t, admin.publish(other, orev), 409, "SLUG_TAKEN")
	if st := admin.get("/api/v1/admin/articles/" + other).data(); st["status"] != "DRAFT" || st["current_publication"] != nil {
		t.Fatalf("failed publish left state: %v", st)
	}
	if n := h.scalar(`SELECT count(*) FROM article_publication_tags WHERE article_id = $1`, other); n != int64(0) {
		t.Fatal("partial publish left public tags")
	}
	expect(t, admin.publish(other, orev+0), 409, "SLUG_TAKEN") // still taken: historical slugs are never reused

	// Validation failures roll back too.
	empty := admin.create()
	r := admin.publish(empty, 1)
	expect(t, r, 422, "VALIDATION_FAILED")
	if f := r.details()["fields"].(map[string]any); f["title"] == nil || f["body_markdown"] == nil {
		t.Fatalf("fields: %v", f)
	}

	// Generated slugs: ASCII title → slugified; collisions get a suffix; Chinese title → post-<id>.
	a := admin.create()
	r = admin.publish(a, admin.mustSave(a, 1, newDraft("Docker Compose Guide!", "body")))
	if r.data()["slug"] != "docker-compose-guide" {
		t.Fatalf("generated slug = %v", r.data()["slug"])
	}
	b := admin.create()
	r = admin.publish(b, admin.mustSave(b, 1, newDraft("Docker  compose guide", "body")))
	if s, _ := r.data()["slug"].(string); !strings.HasPrefix(s, "docker-compose-guide-") {
		t.Fatalf("collision suffix slug = %v", r.data()["slug"])
	}
	c := admin.create()
	r = admin.publish(c, admin.mustSave(c, 1, newDraft("容器化部署笔记", "body")))
	if s, _ := r.data()["slug"].(string); !strings.HasPrefix(s, "post-") {
		t.Fatalf("chinese-title slug = %v", r.data()["slug"])
	}
	// Without a candidate slug an update keeps the current public slug.
	crev := admin.mustSave(c, 2, newDraft("容器化部署笔记（更新）", "body 2"))
	if r2 := admin.publish(c, crev); r2.data()["slug"] != r.data()["slug"] {
		t.Fatalf("slug changed on update: %v → %v", r.data()["slug"], r2.data()["slug"])
	}

	// Concurrent publish of the same manual slug by two articles.
	ids := []string{admin.create(), admin.create()}
	revs := []int{admin.mustSave(ids[0], 1, newDraft("Race", "b").with("candidate_slug", "race")), admin.mustSave(ids[1], 1, newDraft("Race", "b").with("candidate_slug", "race"))}
	results := make([]response, 2)
	var wg sync.WaitGroup
	for i := range ids {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i] = admin.publish(ids[i], revs[i])
		}(i)
	}
	wg.Wait()
	codes := []string{results[0].code(), results[1].code()}
	slices.Sort(codes)
	if !slices.Equal(codes, []string{"", "SLUG_TAKEN"}) {
		t.Fatalf("race results: %s / %s", results[0].Body, results[1].Body)
	}
}

// 9. Withdraw, trash, restore: nothing public afterwards, restore never republishes, trashed is read-only.
func TestWithdrawTrashRestore(t *testing.T) {
	h := newHarness(t)
	admin := h.user("ADMIN")
	img := admin.uploadPNG("p.png", 20, 20)
	id := admin.create()
	rev := admin.mustSave(id, 1, newDraft("公开文章", "![x](/media/"+img+"/file)").with("candidate_slug", "life-cycle"))
	admin.publish(id, rev)
	rev = admin.mustSave(id, rev, newDraft("公开文章", "![x](/media/"+img+"/file)").with("candidate_slug", "life-cycle-2"))
	admin.publish(id, rev)

	r := admin.json(http.MethodPost, "/api/v1/admin/articles/"+id+"/withdraw", nil)
	if r.Status != 200 || r.data()["status"] != "WITHDRAWN" {
		t.Fatalf("withdraw: %s", r.Body)
	}
	expect(t, h.public("life-cycle-2"), 404, "NOT_FOUND")
	expect(t, h.public("life-cycle"), 404, "NOT_FOUND") // old slug no longer redirects
	expect(t, h.anon().get("/media/"+img+"/file"), 404, "NOT_FOUND")
	if r := admin.json(http.MethodPost, "/api/v1/admin/articles/"+id+"/withdraw", nil); r.data()["status"] != "WITHDRAWN" {
		t.Fatal("repeated withdraw should report the current state")
	}
	// Explicit re-publish of the same revision after withdrawal is a real publish (API-15).
	if r := admin.publish(id, rev); r.data()["published_changed"] != true || r.data()["publication_revision"] != float64(3) {
		t.Fatalf("republish: %s", r.Body)
	}

	if r := admin.raw(http.MethodDelete, "/api/v1/admin/articles/"+id, nil, ""); r.Status != 204 {
		t.Fatalf("trash: %d %s", r.Status, r.Body)
	}
	expect(t, h.public("life-cycle-2"), 404, "NOT_FOUND")
	expect(t, admin.save(id, rev, newDraft("x", "y")), 409, "ARTICLE_TRASHED")
	expect(t, admin.publish(id, rev), 409, "ARTICLE_TRASHED")
	if items := admin.get("/api/v1/admin/articles?status=TRASHED").JSON["data"].([]any); len(items) != 1 {
		t.Fatalf("recycle bin: %v", items)
	}
	if items := admin.get("/api/v1/admin/articles").JSON["data"].([]any); len(items) != 0 {
		t.Fatal("trashed article in the default list")
	}
	r = admin.json(http.MethodPost, "/api/v1/admin/articles/"+id+"/restore", nil)
	if r.Status != 200 || r.data()["status"] != "WITHDRAWN" {
		t.Fatalf("restore: %s", r.Body)
	}
	expect(t, h.public("life-cycle-2"), 404, "NOT_FOUND")

	never := admin.create()
	admin.raw(http.MethodDelete, "/api/v1/admin/articles/"+never, nil, "")
	if r := admin.json(http.MethodPost, "/api/v1/admin/articles/"+never+"/restore", nil); r.data()["status"] != "DRAFT" {
		t.Fatalf("restore never-published: %s", r.Body)
	}
	expect(t, admin.json(http.MethodPost, "/api/v1/admin/articles/"+never+"/withdraw", nil), 409, "ARTICLE_NOT_PUBLISHED")
	for _, action := range []string{"ARTICLE_WITHDRAW", "ARTICLE_TRASH", "ARTICLE_RESTORE"} {
		if n := h.scalar(`SELECT count(*) FROM admin_audit_logs WHERE action = $1`, action); n.(int64) < 1 {
			t.Fatalf("no audit entry for %s", action)
		}
	}
}

// 7. Category and tag management.
func TestTaxonomy(t *testing.T) {
	h := newHarness(t)
	admin := h.user("ADMIN")
	cat := admin.category("开发", "dev")
	expect(t, admin.json(http.MethodPost, "/api/v1/admin/categories", map[string]any{"name": "重复", "slug": "dev"}), 409, "SLUG_TAKEN")
	expect(t, admin.json(http.MethodPost, "/api/v1/admin/categories", map[string]any{"name": "", "slug": "x"}), 422, "VALIDATION_FAILED")
	expect(t, admin.json(http.MethodPost, "/api/v1/admin/tags", map[string]any{"name": "t", "slug": "Not Valid"}), 422, "VALIDATION_FAILED")
	r := admin.json(http.MethodPatch, "/api/v1/admin/categories/"+cat, map[string]any{"name": "开发笔记", "is_active": false})
	if r.Status != 200 || r.data()["name"] != "开发笔记" || r.data()["slug"] != "dev" || r.data()["is_active"] != false {
		t.Fatalf("patch category: %s", r.Body)
	}
	tag := admin.tag("Go", "go")
	id := admin.create()
	admin.mustSave(id, 1, newDraft("t", "b").with("category_id", cat).with("tag_ids", []string{tag}))
	expect(t, admin.raw(http.MethodDelete, "/api/v1/admin/categories/"+cat, nil, ""), 409, "RESOURCE_IN_USE")
	expect(t, admin.raw(http.MethodDelete, "/api/v1/admin/tags/"+tag, nil, ""), 409, "RESOURCE_IN_USE")
	list := admin.get("/api/v1/admin/tags").JSON["data"].([]any)
	if len(list) != 1 || list[0].(map[string]any)["draft_count"] != float64(1) {
		t.Fatalf("tag list: %v", list)
	}
	unused := admin.tag("Unused", "unused")
	if r := admin.raw(http.MethodDelete, "/api/v1/admin/tags/"+unused, nil, ""); r.Status != 204 {
		t.Fatalf("delete unused tag: %d", r.Status)
	}
	expect(t, admin.raw(http.MethodDelete, "/api/v1/admin/tags/"+unused, nil, ""), 404, "NOT_FOUND")
}

// 11. Normal users and visitors cannot use any admin article API; the public API exposes no drafts.
func TestArticlePermissions(t *testing.T) {
	h := newHarness(t)
	admin := h.user("ADMIN")
	user := h.user("USER")
	id := admin.create()
	for _, c := range []struct {
		method, path string
		body         any
	}{
		{http.MethodGet, "/api/v1/admin/articles", nil},
		{http.MethodPost, "/api/v1/admin/articles", nil},
		{http.MethodGet, "/api/v1/admin/articles/" + id, nil},
		{http.MethodPut, "/api/v1/admin/articles/" + id + "/draft", newDraft("x", "y").with("expected_revision", 1)},
		{http.MethodPost, "/api/v1/admin/articles/" + id + "/publish", map[string]any{"expected_draft_revision": 1}},
		{http.MethodPost, "/api/v1/admin/articles/" + id + "/withdraw", nil},
		{http.MethodDelete, "/api/v1/admin/articles/" + id, nil},
		{http.MethodPost, "/api/v1/admin/articles/" + id + "/restore", nil},
		{http.MethodPost, "/api/v1/admin/categories", map[string]any{"name": "x", "slug": "x"}},
		{http.MethodPost, "/api/v1/admin/tags", map[string]any{"name": "x", "slug": "x"}},
	} {
		expect(t, user.json(c.method, c.path, c.body), 403, "FORBIDDEN")
		expect(t, h.anon().json(c.method, c.path, c.body), 401, "AUTH_REQUIRED")
	}
	if st := admin.get("/api/v1/admin/articles/" + id).data()["status"]; st != "DRAFT" {
		t.Fatalf("state changed by unauthorised calls: %v", st)
	}
	expect(t, h.public("Not_A_Slug"), 404, "NOT_FOUND")
}
