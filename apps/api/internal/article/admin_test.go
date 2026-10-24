package article_test

// P7 admin console through the full router: overview, users, site settings and the about page.

import (
	"crypto/sha256"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestAdminEndpointsRequireAdmin(t *testing.T) {
	h := newHarness(t)
	user := h.account("USER", "普通用户", true, "ACTIVE")
	anon := h.anon()
	type call struct{ method, path string }
	calls := []call{
		{http.MethodGet, "/api/v1/admin/overview"}, {http.MethodGet, "/api/v1/admin/users"},
		{http.MethodGet, "/api/v1/admin/users/" + uuid.NewString()}, {http.MethodPost, "/api/v1/admin/users/" + user.id.String() + "/disable"},
		{http.MethodGet, "/api/v1/admin/site"}, {http.MethodPatch, "/api/v1/admin/site"},
		{http.MethodPut, "/api/v1/admin/site/social-links"}, {http.MethodPut, "/api/v1/admin/site/navigation-links"},
		{http.MethodGet, "/api/v1/admin/pages/about"}, {http.MethodPut, "/api/v1/admin/pages/about"},
	}
	for _, c := range calls {
		expect(t, user.json(c.method, c.path, map[string]any{}), 403, "FORBIDDEN")
		expect(t, anon.json(c.method, c.path, map[string]any{}), 401, "AUTH_REQUIRED")
	}
}

func TestAdminOverview(t *testing.T) {
	h := newHarness(t)
	admin := h.user("ADMIN")
	pub := admin.publishOne("ov-pub", "已发布", "正文", "2026-01-01T00:00:00Z", nil)
	admin.publishOne("ov-pub2", "已发布二", "正文", "2026-01-02T00:00:00Z", nil)
	admin.create() // draft
	w := admin.publishOne("ov-w", "撤回", "正文", "2026-01-03T00:00:00Z", nil)
	admin.json(http.MethodPost, "/api/v1/admin/articles/"+w+"/withdraw", nil)
	tr := admin.create()
	admin.json(http.MethodDelete, "/api/v1/admin/articles/"+tr, nil)
	h.account("USER", "未验证", false, "ACTIVE")
	h.account("USER", "禁用", true, "DISABLED")
	reader := h.account("USER", "读者", true, "ACTIVE")
	mustID(t, reader.comment(pub, "可见评论"))
	hidden := mustID(t, reader.comment(pub, "将被隐藏"))
	admin.json(http.MethodPost, "/api/v1/admin/comments/"+hidden+"/hide", nil)

	r := admin.get("/api/v1/admin/overview")
	d := r.data()
	want := map[string]float64{
		"article_count": 4, "published_count": 2, "draft_count": 1, "withdrawn_count": 1, "trashed_count": 1,
		"user_count": 4, "unverified_user_count": 1, "disabled_user_count": 1, "comment_count": 1, "hidden_comment_count": 1,
	}
	for k, v := range want {
		if d[k] != v {
			t.Errorf("%s = %v, want %v", k, d[k], v)
		}
	}
	if len(d["recent_articles"].([]any)) != 5 || len(d["recent_events"].([]any)) == 0 {
		t.Fatalf("recent lists: %s", r.Body)
	}
	if strings.Contains(string(r.Body), "pending") || strings.Contains(string(r.Body), "审核") {
		t.Fatal("overview must not mention review queues")
	}
	if notices := d["notices"].([]any); len(notices) != 1 || notices[0].(map[string]any)["code"] != "SINGLE_ADMIN" {
		t.Fatalf("notices: %v", notices)
	}
}

func TestAdminUserDisable(t *testing.T) {
	h := newHarness(t)
	admin := h.user("ADMIN")
	other := h.account("ADMIN", "另一位管理员", true, "ACTIVE")
	articleID := admin.publishOne("disable", "禁用测试", "正文", "2026-01-01T00:00:00Z", nil)
	bad := h.account("USER", "违规用户", true, "ACTIVE")
	mustID(t, bad.comment(articleID, "违规前的评论"))
	// A second device and an unused reset token.
	raw2 := "session-second-" + bad.id.String()
	sum := sha256.Sum256([]byte(raw2))
	h.exec(`INSERT INTO auth_sessions (id, user_id, token_digest, expires_at) VALUES ($1, $2, $3, now() + interval '1 hour')`, uuid.New(), bad.id, sum[:])
	tok := sha256.Sum256([]byte("token-" + bad.id.String()))
	h.exec(`INSERT INTO auth_tokens (id, user_id, purpose, token_digest, expires_at) VALUES ($1, $2, 'RESET_PASSWORD', $3, now() + interval '1 hour')`, uuid.New(), bad.id, tok[:])

	// List and detail.
	r := admin.get("/api/v1/admin/users?q=" + "%E8%BF%9D%E8%A7%84") // 违规
	if got := list(r); len(got) != 1 || got[0]["id"] != bad.id.String() || got[0]["comment_count"] != float64(1) || got[0]["email"] == nil {
		t.Fatalf("search: %s", r.Body)
	}
	if n := len(list(admin.get("/api/v1/admin/users?role=ADMIN"))); n != 2 {
		t.Fatalf("role filter: %d", n)
	}
	expect(t, admin.get("/api/v1/admin/users?status=GONE"), 422, "VALIDATION_FAILED")
	d := admin.get("/api/v1/admin/users/" + bad.id.String()).data()
	if d["can_disable"] != true || d["active_session_count"] != float64(2) || len(d["recent_comments"].([]any)) != 1 {
		t.Fatalf("detail: %v", d)
	}
	expect(t, admin.get("/api/v1/admin/users/"+uuid.NewString()), 404, "NOT_FOUND")

	// Disable: sessions and tokens revoked at once, the user cannot act any more.
	r = admin.json(http.MethodPost, "/api/v1/admin/users/"+bad.id.String()+"/disable", nil)
	if r.Status != 200 || r.data()["status"] != "DISABLED" || r.data()["revoked_sessions"] != float64(2) {
		t.Fatalf("disable: %s", r.Body)
	}
	expect(t, bad.get("/api/v1/auth/me"), 401, "SESSION_EXPIRED")
	if n := h.scalar(`SELECT count(*) FROM auth_tokens WHERE user_id = $1 AND revoked_at IS NULL AND consumed_at IS NULL`, bad.id).(int64); n != 0 {
		t.Fatalf("unused tokens left: %d", n)
	}
	// Even a fresh session (e.g. one created just before) is refused: the account itself is disabled.
	again := h.account("USER", "x", true, "ACTIVE")
	h.exec(`UPDATE auth_sessions SET user_id = $1 WHERE user_id = $2`, bad.id, again.id)
	expect(t, again.comment(articleID, "禁用后评论"), 403, "ACCOUNT_DISABLED")
	if r := admin.json(http.MethodPost, "/api/v1/admin/users/"+bad.id.String()+"/disable", nil); r.Status != 200 || r.data()["revoked_sessions"] != float64(0) {
		t.Fatalf("repeat disable: %s", r.Body)
	}
	// History stays: their comment is still listed.
	if n := len(list(h.anon().get("/api/v1/articles/" + articleID + "/comments"))); n != 1 {
		t.Fatalf("comments of a disabled user: %d", n)
	}

	// Protected accounts.
	expect(t, admin.json(http.MethodPost, "/api/v1/admin/users/"+other.id.String()+"/disable", nil), 409, "ADMIN_PROTECTED")
	expect(t, admin.json(http.MethodPost, "/api/v1/admin/users/"+admin.id.String()+"/disable", nil), 409, "CANNOT_DISABLE_SELF")
	expect(t, admin.json(http.MethodPost, "/api/v1/admin/users/"+uuid.NewString()+"/disable", nil), 404, "NOT_FOUND")
	if v := h.scalar(`SELECT status FROM users WHERE id = $1`, other.id); v != "ACTIVE" {
		t.Fatal("other admin changed")
	}

	// Enable restores the account (sessions stay revoked: the user signs in again).
	r = admin.json(http.MethodPost, "/api/v1/admin/users/"+bad.id.String()+"/enable", nil)
	if r.Status != 200 || r.data()["status"] != "ACTIVE" {
		t.Fatalf("enable: %s", r.Body)
	}
	if n := h.scalar(`SELECT count(*) FROM admin_audit_logs WHERE object_type = 'user' AND object_key = $1`, bad.id.String()).(int64); n != 2 {
		t.Fatalf("audit entries: %d", n)
	}
}

func TestAdminSiteSettings(t *testing.T) {
	h := newHarness(t)
	admin := h.user("ADMIN")
	anon := h.anon()

	r := admin.get("/api/v1/admin/site")
	if r.Status != 200 || r.data()["site_title"] != "XiaoWork Blog" || strings.Contains(strings.ToLower(string(r.Body)), "smtp") ||
		strings.Contains(strings.ToLower(string(r.Body)), "password") || strings.Contains(strings.ToLower(string(r.Body)), "secret") {
		t.Fatalf("admin site: %s", r.Body)
	}

	r = admin.json(http.MethodPatch, "/api/v1/admin/site", map[string]any{
		"site_title": "  小王的博客 ", "site_description": "记录技术与生活", "profile_name": "小王", "profile_bio": "后端工程师",
		"contact_email": "hello@example.test", "default_seo_title": "小王的博客 · 技术笔记", "default_seo_description": "SEO 描述",
	})
	if r.Status != 200 || r.data()["site_title"] != "小王的博客" {
		t.Fatalf("patch: %d %s", r.Status, r.Body)
	}
	pub := anon.get("/api/v1/site").data()
	if pub["site_title"] != "小王的博客" || pub["profile"].(map[string]any)["bio"] != "后端工程师" || pub["default_seo_title"] != "小王的博客 · 技术笔记" {
		t.Fatalf("public site not updated: %v", pub)
	}
	// Partial update keeps the other fields; null clears optional ones.
	admin.json(http.MethodPatch, "/api/v1/admin/site", map[string]any{"default_seo_title": nil})
	if pub := anon.get("/api/v1/site").data(); pub["default_seo_title"] != nil || pub["site_title"] != "小王的博客" {
		t.Fatalf("partial patch: %v", pub)
	}

	// Validation and forbidden fields.
	expect(t, admin.json(http.MethodPatch, "/api/v1/admin/site", map[string]any{"site_title": "  "}), 422, "VALIDATION_FAILED")
	expect(t, admin.json(http.MethodPatch, "/api/v1/admin/site", map[string]any{"site_title": strings.Repeat("长", 121)}), 422, "VALIDATION_FAILED")
	expect(t, admin.json(http.MethodPatch, "/api/v1/admin/site", map[string]any{"contact_email": "not-an-email"}), 422, "VALIDATION_FAILED")
	expect(t, admin.json(http.MethodPatch, "/api/v1/admin/site", map[string]any{"profile_bio": "a\u202eb"}), 422, "VALIDATION_FAILED")
	expect(t, admin.json(http.MethodPatch, "/api/v1/admin/site", map[string]any{"smtp_password": "x"}), 400, "INVALID_REQUEST")
	expect(t, admin.json(http.MethodPatch, "/api/v1/admin/site", map[string]any{"logo_media_id": "nope"}), 422, "VALIDATION_FAILED")
	expect(t, admin.json(http.MethodPatch, "/api/v1/admin/site", map[string]any{"logo_media_id": uuid.NewString()}), 422, "MEDIA_NOT_READY")

	// Media from the P3 library: referencing it makes it public and protects it from deletion.
	logo := admin.uploadPNG("logo.png", 64, 64)
	if anon.get("/media/"+logo+"/file").Status != 404 {
		t.Fatal("unreferenced upload should be private")
	}
	r = admin.json(http.MethodPatch, "/api/v1/admin/site", map[string]any{"logo_media_id": logo, "favicon_media_id": logo, "profile_avatar_media_id": logo})
	if r.Status != 200 || r.data()["logo"].(map[string]any)["id"] != logo {
		t.Fatalf("logo: %s", r.Body)
	}
	if anon.get("/media/"+logo+"/file").Status != 200 || anon.get("/api/v1/site").data()["favicon"].(map[string]any)["id"] != logo {
		t.Fatal("site media not public")
	}
	expect(t, admin.json(http.MethodDelete, "/api/v1/admin/media/"+logo, nil), 409, "RESOURCE_IN_USE")
	// Avatars are personal images and can never become site images.
	avatarID := admin.uploadPNG("a.png", 8, 8)
	h.exec(`UPDATE media_assets SET usage_kind = 'AVATAR' WHERE id = $1`, avatarID)
	expect(t, admin.json(http.MethodPatch, "/api/v1/admin/site", map[string]any{"logo_media_id": avatarID}), 422, "MEDIA_NOT_READY")
	// The image picker lists every reusable kind but never avatars.
	for _, m := range list(admin.get("/api/v1/admin/media?usage_kind=ARTICLE,SITE,PAGE&page_size=100")) {
		if m["id"] == avatarID {
			t.Fatal("avatar offered by the image picker")
		}
	}
	expect(t, admin.get("/api/v1/admin/media?usage_kind=ARTICLE,BOGUS"), 422, "VALIDATION_FAILED")
	admin.json(http.MethodPatch, "/api/v1/admin/site", map[string]any{"logo_media_id": nil, "favicon_media_id": nil, "profile_avatar_media_id": nil})
	if anon.get("/media/"+logo+"/file").Status != 404 {
		t.Fatal("cleared site media still public")
	}

	// Social links: only https / mailto, known icons, order = array order, disabled ones hidden.
	bad := []map[string]any{
		{"icon_key": "link", "label": "x", "url": "javascript:alert(1)"},
		{"icon_key": "link", "label": "x", "url": "http://example.com"},
		{"icon_key": "link", "label": "x", "url": "data:text/html,x"},
		{"icon_key": "mail", "label": "x", "url": "mailto:not an email"},
		{"icon_key": "skull", "label": "x", "url": "https://example.com"},
		{"icon_key": "link", "label": "", "url": "https://example.com"},
	}
	r = admin.json(http.MethodPut, "/api/v1/admin/site/social-links", map[string]any{"links": bad})
	expect(t, r, 422, "VALIDATION_FAILED")
	f := r.details()["fields"].(map[string]any)
	for _, k := range []string{"links[0].url", "links[1].url", "links[2].url", "links[3].url", "links[4].icon_key", "links[5].label"} {
		if f[k] == nil {
			t.Errorf("missing field error %s: %v", k, f)
		}
	}
	many := make([]map[string]any, 13)
	for i := range many {
		many[i] = map[string]any{"icon_key": "link", "label": "x", "url": "https://example.com"}
	}
	expect(t, admin.json(http.MethodPut, "/api/v1/admin/site/social-links", map[string]any{"links": many}), 422, "VALIDATION_FAILED")
	r = admin.json(http.MethodPut, "/api/v1/admin/site/social-links", map[string]any{"links": []map[string]any{
		{"icon_key": "mail", "label": "邮箱", "url": "mailto:hello@example.test"},
		{"icon_key": "github", "label": "GitHub", "url": "https://github.com/xiaowork-dev"},
		{"icon_key": "rss", "label": "隐藏的 RSS", "url": "https://example.com/rss.xml", "is_enabled": false},
	}})
	if r.Status != 200 || len(r.data()["social_links"].([]any)) != 3 {
		t.Fatalf("social: %s", r.Body)
	}
	links := anon.get("/api/v1/site").data()["social_links"].([]any)
	if len(links) != 2 || links[0].(map[string]any)["icon_key"] != "mail" || links[1].(map[string]any)["icon_key"] != "github" {
		t.Fatalf("public social order: %v", links)
	}
	// Reorder by sending the list in a new order with the same ids.
	cur := admin.get("/api/v1/admin/site").data()["social_links"].([]any)
	admin.json(http.MethodPut, "/api/v1/admin/site/social-links", map[string]any{"links": []any{cur[1], cur[0]}})
	links = anon.get("/api/v1/site").data()["social_links"].([]any)
	if len(links) != 2 || links[0].(map[string]any)["id"] != cur[1].(map[string]any)["id"] {
		t.Fatalf("reorder: %v", links)
	}

	// Navigation: public site paths or https only.
	for _, l := range []map[string]any{
		{"label": "后台", "link_type": "INTERNAL", "url": "/admin/articles"},
		{"label": "账号", "link_type": "INTERNAL", "url": "/account/profile"},
		{"label": "协议相对", "link_type": "INTERNAL", "url": "//evil.example"},
		{"label": "明文", "link_type": "EXTERNAL", "url": "http://example.com"},
		{"label": "脚本", "link_type": "EXTERNAL", "url": "javascript:alert(1)"},
		{"label": "类型", "link_type": "OTHER", "url": "/about"},
	} {
		expect(t, admin.json(http.MethodPut, "/api/v1/admin/site/navigation-links", map[string]any{"links": []any{l}}), 422, "VALIDATION_FAILED")
	}
	r = admin.json(http.MethodPut, "/api/v1/admin/site/navigation-links", map[string]any{"links": []map[string]any{
		{"label": "首页", "link_type": "INTERNAL", "url": "/"},
		{"label": "归档", "link_type": "INTERNAL", "url": "/archives"},
		{"label": "GitHub", "link_type": "EXTERNAL", "url": "https://github.com/xiaowork-dev"},
	}})
	if r.Status != 200 {
		t.Fatalf("nav: %s", r.Body)
	}
	if nav := anon.get("/api/v1/site").data()["navigation_links"].([]any); len(nav) != 3 || nav[2].(map[string]any)["link_type"] != "EXTERNAL" {
		t.Fatalf("public nav: %v", nav)
	}
	if n := h.scalar(`SELECT count(*) FROM admin_audit_logs WHERE object_type = 'site'`).(int64); n < 5 {
		t.Fatalf("site audit entries: %d", n)
	}
}

func TestAdminAboutPage(t *testing.T) {
	h := newHarness(t)
	admin := h.user("ADMIN")
	anon := h.anon()
	if d := admin.get("/api/v1/admin/pages/about").data(); d["exists"] != false {
		t.Fatalf("initial about: %v", d)
	}
	expect(t, anon.get("/api/v1/pages/about"), 404, "NOT_FOUND")

	img := admin.uploadPNG("about.png", 40, 40)
	body := "# 你好\n\n<script>alert(1)</script>\n\n![我](/media/" + img + "/file)\n\n[x](javascript:alert(1))"
	r := admin.json(http.MethodPut, "/api/v1/admin/pages/about", map[string]any{"title": "关于我", "body_markdown": body})
	if r.Status != 200 || r.data()["exists"] != true || r.data()["updated_by"] != "tester" {
		t.Fatalf("save about: %d %s", r.Status, r.Body)
	}
	// Saved = public at once; the Markdown is returned as stored (rendered safely by the client, see markdown.test.ts).
	p := anon.get("/api/v1/pages/about").data()
	if p["title"] != "关于我" || p["body_markdown"] != body {
		t.Fatalf("public about: %v", p)
	}
	if anon.get("/media/"+img+"/file").Status != 200 {
		t.Fatal("about image not public")
	}
	expect(t, admin.json(http.MethodDelete, "/api/v1/admin/media/"+img, nil), 409, "RESOURCE_IN_USE")
	if v := h.scalar(`SELECT body_plain_text FROM static_pages WHERE page_key = 'about'`).(string); strings.Contains(v, "![") || !strings.Contains(v, "你好") {
		t.Fatalf("plain text: %q", v)
	}

	// Removing the image releases it.
	admin.json(http.MethodPut, "/api/v1/admin/pages/about", map[string]any{"title": "关于我", "body_markdown": "没有图片了"})
	if anon.get("/media/"+img+"/file").Status != 404 {
		t.Fatal("released about image still public")
	}
	expect(t, admin.json(http.MethodPut, "/api/v1/admin/pages/about", map[string]any{"title": "", "body_markdown": "x"}), 422, "VALIDATION_FAILED")
	expect(t, admin.json(http.MethodPut, "/api/v1/admin/pages/about", map[string]any{"title": "t"}), 422, "VALIDATION_FAILED")
	expect(t, admin.json(http.MethodPut, "/api/v1/admin/pages/about", map[string]any{"title": "t", "body_markdown": "![x](/media/" + uuid.NewString() + "/file)"}), 422, "MEDIA_NOT_READY")
}
