package article_test

import (
	"net/http"
	"testing"
)

func listSlugs(t *testing.T, r response) []string {
	t.Helper()
	items, _ := r.JSON["data"].([]any)
	out := make([]string, 0, len(items))
	for _, it := range items {
		out = append(out, it.(map[string]any)["slug"].(string))
	}
	return out
}

// P8-B12: related articles rank shared tags above the same category above recency, never include the
// article itself or non-public articles, and fill up with the newest articles.
func TestRelatedArticles(t *testing.T) {
	h := newHarness(t)
	admin := h.user("ADMIN")
	dev, life := admin.category("开发", "dev"), admin.category("生活", "life")
	goTag, dockerTag := admin.tag("Go", "go"), admin.tag("Docker", "docker")
	src := admin.publishOne("src", "源文章", "正文", "2025-01-01T00:00:00Z", func(d draft) draft {
		return d.with("category_id", dev).with("tag_ids", []string{goTag, dockerTag})
	})
	admin.publishOne("two-tags", "两个相同标签", "正文", "2025-01-02T00:00:00Z", func(d draft) draft {
		return d.with("category_id", life).with("tag_ids", []string{goTag, dockerTag})
	})
	admin.publishOne("one-tag", "一个相同标签", "正文", "2025-01-03T00:00:00Z", func(d draft) draft {
		return d.with("category_id", life).with("tag_ids", []string{goTag})
	})
	admin.publishOne("same-cat", "同分类", "正文", "2025-01-04T00:00:00Z", func(d draft) draft { return d.with("category_id", dev) })
	admin.publishOne("newest-unrelated", "最新但无关", "正文", "2025-06-01T00:00:00Z", func(d draft) draft { return d.with("category_id", life) })
	hidden := admin.publishOne("withdrawn-related", "已撤回", "正文", "2025-07-01T00:00:00Z", func(d draft) draft {
		return d.with("category_id", dev).with("tag_ids", []string{goTag, dockerTag})
	})
	admin.json(http.MethodPost, "/api/v1/admin/articles/"+hidden+"/withdraw", nil)

	anon := h.anon()
	r := anon.get("/api/v1/articles/" + src + "/related")
	expect(t, r, 200, "")
	if got := listSlugs(t, r); len(got) != 3 || got[0] != "two-tags" || got[1] != "one-tag" || got[2] != "same-cat" {
		t.Fatalf("related = %v, want [two-tags one-tag same-cat]", got)
	}
	if cc := r.Header.Get("Cache-Control"); cc != "public, no-cache" {
		t.Errorf("related Cache-Control = %q", cc)
	}

	// An article without any shared terms still gets the newest public articles.
	lonely := admin.publishOne("lonely", "孤立文章", "正文", "2025-08-01T00:00:00Z", nil)
	if got := listSlugs(t, anon.get("/api/v1/articles/"+lonely+"/related")); len(got) != 3 || got[0] != "newest-unrelated" {
		t.Fatalf("fallback related = %v", got)
	}
	expect(t, anon.get("/api/v1/articles/"+hidden+"/related"), 404, "NOT_FOUND")
	expect(t, anon.get("/api/v1/articles/not-a-uuid/related"), 404, "NOT_FOUND")
}

// P8-B12 bookmarks: signed-in readers only, public articles only, idempotent, private responses, and
// withdrawn articles disappear from the list until they are public again.
func TestBookmarks(t *testing.T) {
	h := newHarness(t)
	admin := h.user("ADMIN")
	a := admin.publishOne("first", "第一篇", "正文", "2025-01-01T00:00:00Z", nil)
	b := admin.publishOne("second", "第二篇", "正文", "2025-01-02T00:00:00Z", nil)
	draftOnly := admin.create()
	reader, other, anon := h.user("USER"), h.account("USER", "other", true, "ACTIVE"), h.anon()

	expect(t, anon.get("/api/v1/me/bookmarks"), 401, "AUTH_REQUIRED")
	expect(t, anon.json(http.MethodPut, "/api/v1/me/bookmarks/"+a, nil), 401, "AUTH_REQUIRED")
	expect(t, reader.json(http.MethodPut, "/api/v1/me/bookmarks/"+draftOnly, nil), 404, "NOT_FOUND")

	for _, id := range []string{a, b, a} { // the repeated PUT is a no-op
		r := reader.json(http.MethodPut, "/api/v1/me/bookmarks/"+id, nil)
		expect(t, r, 200, "")
		if r.data()["bookmarked"] != true {
			t.Fatalf("PUT %s: %s", id, r.Body)
		}
	}
	if n := h.scalar(`SELECT count(*) FROM article_bookmarks`); n.(int64) != 2 {
		t.Fatalf("rows = %v, want 2", n)
	}
	r := reader.get("/api/v1/me/bookmarks")
	expect(t, r, 200, "")
	if got := listSlugs(t, r); len(got) != 2 || got[0] != "second" || got[1] != "first" {
		t.Fatalf("bookmarks = %v, want newest bookmark first [second first]", got)
	}
	if cc := r.Header.Get("Cache-Control"); cc != "private, no-store" {
		t.Errorf("bookmarks Cache-Control = %q", cc)
	}
	if ids, _ := reader.get("/api/v1/me/bookmarks/ids").JSON["data"].([]any); len(ids) != 2 {
		t.Fatalf("ids = %v", ids)
	}
	// Another reader sees nothing of it.
	if got := listSlugs(t, other.get("/api/v1/me/bookmarks")); len(got) != 0 {
		t.Fatalf("other reader bookmarks = %v", got)
	}

	// Withdrawn → hidden but kept; published again → listed again.
	admin.json(http.MethodPost, "/api/v1/admin/articles/"+a+"/withdraw", nil)
	if got := listSlugs(t, reader.get("/api/v1/me/bookmarks")); len(got) != 1 || got[0] != "second" {
		t.Fatalf("after withdraw = %v", got)
	}
	detail := admin.get("/api/v1/admin/articles/" + a).data()
	if r := admin.publish(a, int(detail["draft"].(map[string]any)["revision"].(float64))); r.Status != 200 {
		t.Fatalf("re-publish: %d %s", r.Status, r.Body)
	}
	if got := listSlugs(t, reader.get("/api/v1/me/bookmarks")); len(got) != 2 {
		t.Fatalf("after re-publish = %v", got)
	}

	expect(t, reader.json(http.MethodDelete, "/api/v1/me/bookmarks/"+a, nil), 200, "")
	expect(t, reader.json(http.MethodDelete, "/api/v1/me/bookmarks/"+a, nil), 200, "") // idempotent
	if got := listSlugs(t, reader.get("/api/v1/me/bookmarks")); len(got) != 1 || got[0] != "second" {
		t.Fatalf("after delete = %v", got)
	}
}
