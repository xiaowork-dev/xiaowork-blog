package article_test

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"
)

const browserUA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0 Safari/537.36"

// publishOne creates, saves and publishes an article, then pins its first_published_at so ordering
// and archive buckets are deterministic.
func (c *client) publishOne(slug, title, body string, publishedAt string, set func(draft) draft) string {
	c.h.t.Helper()
	id := c.create()
	d := newDraft(title, body).with("candidate_slug", slug)
	if set != nil {
		d = set(d)
	}
	rev := c.mustSave(id, 1, d)
	if r := c.publish(id, rev); r.Status != 200 {
		c.h.t.Fatalf("publish %s: %d %s", slug, r.Status, r.Body)
	}
	c.h.exec(`UPDATE articles SET first_published_at = $2, public_updated_at = $2 WHERE id = $1`, id, publishedAt)
	return id
}

func list(r response) []map[string]any {
	items, _ := r.JSON["data"].([]any)
	out := make([]map[string]any, len(items))
	for i, it := range items {
		out[i], _ = it.(map[string]any)
	}
	return out
}

func meta(r response) map[string]any {
	m, _ := r.JSON["meta"].(map[string]any)
	return m
}

func slugs(r response) []string {
	var out []string
	for _, it := range list(r) {
		out = append(out, it["slug"].(string))
	}
	return out
}

func TestPublicListTaxonomyAndArchives(t *testing.T) {
	h := newHarness(t)
	admin := h.user("ADMIN")
	dev, life := admin.category("开发笔记", "dev"), admin.category("生活", "life")
	admin.category("空分类", "empty")
	goTag, dockerTag := admin.tag("Go", "go"), admin.tag("Docker", "docker")
	cover := admin.uploadPNG("cover.png", 32, 32)

	// 12 public articles: 2025-01 … 2025-12, alternating categories; every third one tagged.
	for i := 1; i <= 12; i++ {
		i := i
		admin.publishOne(fmt.Sprintf("post-%02d", i), fmt.Sprintf("文章 %02d", i), "正文", fmt.Sprintf("2025-%02d-10T08:00:00Z", i),
			func(d draft) draft {
				cat := dev
				if i%2 == 0 {
					cat = life
				}
				d = d.with("category_id", cat).with("summary", fmt.Sprintf("摘要 %02d", i))
				if i%3 == 0 {
					d = d.with("tag_ids", []string{goTag, dockerTag})
				}
				if i == 12 {
					d = d.with("cover_media_id", cover)
				}
				return d
			})
	}
	// One 2026 article, then three that must never be listed: draft, withdrawn, trashed.
	admin.publishOne("post-2026", "新年文章", "正文", "2026-02-01T00:00:00Z", func(d draft) draft { return d.with("category_id", dev) })
	draftOnly := admin.create()
	admin.mustSave(draftOnly, 1, newDraft("未发布草稿", "正文").with("candidate_slug", "draft-only").with("category_id", dev))
	withdrawn := admin.publishOne("withdrawn", "已撤回", "正文", "2026-03-01T00:00:00Z", func(d draft) draft {
		return d.with("category_id", dev).with("tag_ids", []string{goTag})
	})
	admin.json(http.MethodPost, "/api/v1/admin/articles/"+withdrawn+"/withdraw", nil)
	trashed := admin.publishOne("trashed", "已删除", "正文", "2026-04-01T00:00:00Z", func(d draft) draft { return d.with("tag_ids", []string{goTag}) })
	admin.json(http.MethodDelete, "/api/v1/admin/articles/"+trashed, nil)

	anon := h.anon()

	// Home: newest first, 10 per page by default, stable meta.
	r := anon.get("/api/v1/articles")
	m := meta(r)
	if r.Status != 200 || m["total"] != float64(13) || m["total_pages"] != float64(2) || m["page_size"] != float64(10) || len(list(r)) != 10 {
		t.Fatalf("page 1: %d %v %s", r.Status, m, r.Body)
	}
	if got := slugs(r); got[0] != "post-2026" || got[1] != "post-12" || got[9] != "post-04" {
		t.Fatalf("order: %v", got)
	}
	if cc := r.Header.Get("Cache-Control"); cc != "public, no-cache" {
		t.Fatalf("cache-control %q", cc)
	}
	first := list(r)[1] // post-12
	if strings.Contains(string(r.Body), "body_markdown") || first["summary"] != "摘要 12" || first["category"].(map[string]any)["slug"] != "life" ||
		first["cover"].(map[string]any)["id"] != cover || len(first["tags"].([]any)) != 2 || first["view_count"] != float64(0) ||
		first["canonical_path"] != "/posts/post-12" {
		t.Fatalf("summary dto: %v", first)
	}
	for _, s := range []string{"draft-only", "withdrawn", "trashed"} {
		if strings.Contains(string(r.Body), `"`+s+`"`) {
			t.Fatalf("non-public article %s listed", s)
		}
	}
	r = anon.get("/api/v1/articles?page=2")
	if got := slugs(r); len(got) != 3 || got[2] != "post-01" {
		t.Fatalf("page 2: %v", got)
	}
	r = anon.get("/api/v1/articles?page=9")
	if len(list(r)) != 0 || meta(r)["total"] != float64(13) {
		t.Fatalf("out-of-range page: %s", r.Body)
	}
	if got := slugs(anon.get("/api/v1/articles?sort=oldest&page_size=2")); got[0] != "post-01" || got[1] != "post-02" {
		t.Fatalf("oldest: %v", got)
	}
	expect(t, anon.get("/api/v1/articles?page_size=101"), 422, "VALIDATION_FAILED")
	expect(t, anon.get("/api/v1/articles?sort=random"), 422, "VALIDATION_FAILED")

	// Categories: counts only public articles; detail pages filter by the public snapshot.
	r = anon.get("/api/v1/categories")
	counts := map[string]float64{}
	for _, c := range list(r) {
		counts[c["slug"].(string)] = c["article_count"].(float64)
	}
	if counts["dev"] != 7 || counts["life"] != 6 || counts["empty"] != 0 {
		t.Fatalf("category counts %v", counts)
	}
	r = anon.get("/api/v1/categories/life/articles")
	if meta(r)["total"] != float64(6) || meta(r)["category"].(map[string]any)["name"] != "生活" {
		t.Fatalf("category detail: %s", r.Body)
	}
	expect(t, anon.get("/api/v1/categories/nope/articles"), 404, "NOT_FOUND")
	expect(t, anon.get("/api/v1/categories/BAD SLUG/articles"), 404, "NOT_FOUND")

	// Tags: withdrawn/trashed tagged articles are not counted.
	r = anon.get("/api/v1/tags")
	tagCounts := map[string]float64{}
	for _, tg := range list(r) {
		tagCounts[tg["slug"].(string)] = tg["article_count"].(float64)
	}
	if tagCounts["go"] != 4 || tagCounts["docker"] != 4 {
		t.Fatalf("tag counts %v", tagCounts)
	}
	r = anon.get("/api/v1/tags/go/articles?page_size=3")
	if got := slugs(r); meta(r)["total"] != float64(4) || meta(r)["total_pages"] != float64(2) || got[0] != "post-12" {
		t.Fatalf("tag detail: %v %v", got, meta(r))
	}
	if got := slugs(anon.get("/api/v1/articles?tag=docker&category=dev")); len(got) != 2 {
		t.Fatalf("combined filter: %v", got) // post-03, post-09
	}

	// Archives: grouped by year and month, newest first; years carry counts.
	r = anon.get("/api/v1/archives")
	groups := list(r)
	years := meta(r)["years"].([]any)
	if len(groups) != 13 || groups[0]["year"] != float64(2026) || groups[0]["month"] != float64(2) ||
		years[0].(map[string]any)["article_count"] != float64(1) || years[1].(map[string]any)["article_count"] != float64(12) ||
		meta(r)["total"] != float64(13) {
		t.Fatalf("archives: %v %v", groups[0], years)
	}
	r = anon.get("/api/v1/archives?year=2025")
	if g := list(r); len(g) != 12 || g[0]["month"] != float64(12) || g[11]["month"] != float64(1) ||
		len(g[0]["articles"].([]any)) != 1 {
		t.Fatalf("archives 2025: %s", r.Body)
	}
	expect(t, anon.get("/api/v1/archives?year=abc"), 422, "VALIDATION_FAILED")
}

func TestSearchChineseSamples(t *testing.T) {
	h := newHarness(t)
	admin := h.user("ADMIN")
	admin.publishOne("docker-guide", "Docker Compose 容器化部署指南", "本文介绍如何使用 Docker Compose 部署容器化应用，并持久化数据卷。", "2026-01-01T00:00:00Z",
		func(d draft) draft { return d.with("summary", "一个可以复现的部署流程") })
	admin.publishOne("go-backend", "Go 后端学习笔记", "使用 Chi 路由与 PostgreSQL 数据库，配合 sqlc 生成代码。\n\n```go\nfunc main() {}\n```", "2026-02-01T00:00:00Z", nil)
	admin.publishOne("vue3-notes", "Vue 3 组合式 API", "介绍 Vue 3 的 setup 语法与响应式原理。前端也会用到容器吗？", "2026-03-01T00:00:00Z", nil)
	admin.publishOne("spring-boot", "Spring Boot 入门", "Java 生态里最常见的 Web 框架，讲解 Spring Boot 自动配置与数据库连接池。", "2026-04-01T00:00:00Z", nil)
	admin.publishOne("percent", "100% 覆盖率的误区", "测试覆盖率不是唯一指标。", "2026-05-01T00:00:00Z", nil)
	// Never searchable: an unpublished draft and a withdrawn article.
	d := admin.create()
	admin.mustSave(d, 1, newDraft("秘密草稿 容器", "草稿专用词：机密容器方案"))
	w := admin.publishOne("withdrawn-search", "已撤回的容器文章", "撤回专用词：撤回容器", "2026-06-01T00:00:00Z", nil)
	admin.json(http.MethodPost, "/api/v1/admin/articles/"+w+"/withdraw", nil)

	anon := h.anon()
	search := func(q string, extra string) response {
		return anon.get("/api/v1/search/articles?q=" + url.QueryEscape(q) + extra)
	}
	cases := []struct {
		q    string
		want []string
	}{
		{"容器", []string{"docker-guide", "vue3-notes"}}, // title hit ranks first
		{"容器化", []string{"docker-guide"}},
		{"数据库", []string{"spring-boot", "go-backend"}}, // body hits only: newest first
		{"Java", []string{"spring-boot"}},
		{"java", []string{"spring-boot"}}, // case-insensitive
		{"Go", []string{"go-backend"}},
		{"Spring Boot", []string{"spring-boot"}},
		{"Vue 3", []string{"vue3-notes"}},
		{"docker compose", []string{"docker-guide"}},
		{"Docker  容器", []string{"docker-guide"}}, // multiple terms are ANDed
		{"复现的部署", []string{"docker-guide"}},      // summary
		{"func main", []string{"go-backend"}},    // code text is part of the plain text
		{"100%", []string{"percent"}},            // % is literal, not a wildcard
		{"_", nil},                               // _ is literal too
		{"机密容器方案", nil},                          // draft content never searched
		{"撤回专用词", nil},                           // withdrawn content never searched
		{"Kubernetes", nil},                      // empty result
	}
	for _, c := range cases {
		r := search(c.q, "")
		if r.Status != 200 {
			t.Fatalf("%q: %d %s", c.q, r.Status, r.Body)
		}
		got := slugs(r)
		if strings.Join(got, ",") != strings.Join(c.want, ",") || meta(r)["total"] != float64(len(c.want)) {
			t.Errorf("search %q = %v (total %v), want %v", c.q, got, meta(r)["total"], c.want)
		}
	}
	// Pagination over the result set.
	r := search("容器", "&page_size=1&page=2")
	if got := slugs(r); len(got) != 1 || got[0] != "vue3-notes" || meta(r)["total_pages"] != float64(2) || meta(r)["q"] != "容器" {
		t.Fatalf("search page 2: %v %v", got, meta(r))
	}
	expect(t, search("", ""), 422, "VALIDATION_FAILED")
	expect(t, search("   ", ""), 422, "VALIDATION_FAILED")
	expect(t, search(strings.Repeat("长", 101), ""), 422, "VALIDATION_FAILED")
}

func TestSearchRateLimit(t *testing.T) {
	h := newHarness(t)
	anon := h.anon()
	for i := 0; i < 60; i++ {
		if r := anon.get("/api/v1/search/articles?q=x"); r.Status != 200 {
			t.Fatalf("request %d: %d", i, r.Status)
		}
	}
	r := anon.get("/api/v1/search/articles?q=x")
	expect(t, r, 429, "RATE_LIMITED")
	if r.Header.Get("Retry-After") == "" {
		t.Fatal("missing Retry-After")
	}
}

func TestViewCounting(t *testing.T) {
	h := newHarness(t)
	admin := h.user("ADMIN")
	id := admin.publishOne("viewed", "浏览量", "正文", "2026-01-01T00:00:00Z", nil)
	views := func() int64 {
		return h.scalar(`SELECT view_count FROM article_metrics WHERE article_id = $1`, id).(int64)
	}
	reader := func() *client { c := h.anon(); c.ua = browserUA; return c }
	post := func(c *client, articleID string) response {
		return c.json(http.MethodPost, "/api/v1/articles/"+articleID+"/views", nil)
	}

	a := reader()
	expect(t, post(a, id), 204, "")
	expect(t, post(a, id), 204, "") // refresh within the window: not counted again
	expect(t, post(a, id), 204, "")
	if v := views(); v != 1 {
		t.Fatalf("after one browser: %d", v)
	}
	expect(t, post(reader(), id), 204, "")
	if v := views(); v != 2 {
		t.Fatalf("after two browsers: %d", v)
	}
	if got := h.public("viewed").data()["view_count"]; got != float64(2) {
		t.Fatalf("detail view_count %v", got)
	}

	// Bots and clients without a browser context never count.
	bot := h.anon()
	bot.ua = "Mozilla/5.0 (compatible; Googlebot/2.1; +http://www.google.com/bot.html)"
	expect(t, post(bot, id), 204, "")
	plain := h.anon() // Go-http-client default User-Agent
	expect(t, post(plain, id), 204, "")
	if v := views(); v != 2 {
		t.Fatalf("bots counted: %d", v)
	}
	noCSRF := h.anon()
	noCSRF.ua = browserUA
	noCSRF.csrf = "forged"
	expect(t, post(noCSRF, id), 403, "CSRF_INVALID")

	// Only published articles accept views; nothing reveals drafts.
	draftID := admin.create()
	expect(t, post(reader(), draftID), 404, "NOT_FOUND")
	expect(t, post(reader(), uuid.NewString()), 404, "NOT_FOUND")
	expect(t, post(reader(), "not-a-uuid"), 404, "NOT_FOUND")
	if v := views(); v != 2 {
		t.Fatalf("views changed by invalid requests: %d", v)
	}

	// No raw identifiers are stored: only HMAC digests with an expiry.
	if n := h.scalar(`SELECT count(*) FROM auth_rate_windows WHERE action_key = 'ARTICLE_VIEW' AND expires_at > now()`).(int64); n != 2 {
		t.Fatalf("dedupe windows: %d", n)
	}

	// Per-IP flood limit (120 / 10 min), shared by all browsers behind one address.
	flood := reader()
	var last response
	for i := 0; i < 130; i++ {
		last = post(flood, id)
		if last.Status == 429 {
			break
		}
	}
	expect(t, last, 429, "RATE_LIMITED")
	if v := views(); v != 3 {
		t.Fatalf("flood counted more than once: %d", v)
	}
}

func TestSiteAndAboutPage(t *testing.T) {
	h := newHarness(t)
	anon := h.anon()
	r := anon.get("/api/v1/site")
	d := r.data()
	if r.Status != 200 || d["site_title"] != "XiaoWork Blog" || d["profile"].(map[string]any)["name"] != "XiaoWork" ||
		len(d["social_links"].([]any)) != 0 || d["profile"].(map[string]any)["avatar"] != nil {
		t.Fatalf("default site: %d %s", r.Status, r.Body)
	}

	admin := h.user("ADMIN")
	avatar := admin.uploadPNG("avatar.png", 64, 64)
	h.exec(`UPDATE site_settings SET profile_bio = '分享开发、技术与生活', profile_avatar_media_id = $1, contact_email = 'hello@example.test'`, avatar)
	h.exec(`INSERT INTO site_social_links (id, icon_key, label, url, sort_order, is_enabled) VALUES
		($1, 'mail', '邮箱', 'mailto:hello@example.test', 2, true),
		($2, 'github', 'GitHub', 'https://github.com/xiaowork-dev', 1, true),
		($3, 'link', '隐藏', 'https://example.test/hidden', 0, false)`, uuid.New(), uuid.New(), uuid.New())
	h.exec(`INSERT INTO site_navigation_links (id, label, link_type, url, sort_order) VALUES ($1, '关于', 'INTERNAL', '/about', 1)`, uuid.New())

	d = anon.get("/api/v1/site").data()
	links := d["social_links"].([]any)
	if len(links) != 2 || links[0].(map[string]any)["icon_key"] != "github" || links[1].(map[string]any)["url"] != "mailto:hello@example.test" {
		t.Fatalf("social links %v", links)
	}
	if d["profile"].(map[string]any)["avatar"].(map[string]any)["id"] != avatar || len(d["navigation_links"].([]any)) != 1 ||
		d["contact_email"] != "hello@example.test" {
		t.Fatalf("site %v", d)
	}
	// The site avatar is public through the site_settings reference.
	if anon.get("/media/"+avatar+"/file").Status != 200 {
		t.Fatal("site avatar not public")
	}

	expect(t, anon.get("/api/v1/pages/about"), 404, "NOT_FOUND")
	h.exec(`INSERT INTO static_pages (page_key, title, body_markdown, body_plain_text, updated_by) VALUES ('about', '关于我', '# 你好', '你好', $1)`, admin.id)
	r = anon.get("/api/v1/pages/about")
	if r.Status != 200 || r.data()["title"] != "关于我" || r.data()["body_markdown"] != "# 你好" {
		t.Fatalf("about: %d %s", r.Status, r.Body)
	}
}

// 2025-12-31T17:00Z is 2026-01-01 01:00 in Asia/Shanghai: the archive follows the display time zone.
func TestArchiveUsesSiteTimeZone(t *testing.T) {
	h := newHarness(t)
	admin := h.user("ADMIN")
	admin.publishOne("new-year", "跨年", "正文", "2025-12-31T17:00:00Z", nil)
	r := h.anon().get("/api/v1/archives")
	g := list(r)
	years := meta(r)["years"].([]any)
	if len(g) != 1 || g[0]["year"] != float64(2026) || g[0]["month"] != float64(1) || years[0].(map[string]any)["year"] != float64(2026) {
		t.Fatalf("archive bucket: %s", r.Body)
	}
	if len(list(h.anon().get("/api/v1/archives?year=2025"))) != 0 {
		t.Fatal("2025 should be empty")
	}
}
