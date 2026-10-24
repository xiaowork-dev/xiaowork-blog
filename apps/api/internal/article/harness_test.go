package article_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"image"
	"image/png"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/mail"
	"net/url"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/xiaowork-dev/xiaowork-blog/apps/api/internal/admin"
	"github.com/xiaowork-dev/xiaowork-blog/apps/api/internal/article"
	"github.com/xiaowork-dev/xiaowork-blog/apps/api/internal/auth"
	"github.com/xiaowork-dev/xiaowork-blog/apps/api/internal/comment"
	"github.com/xiaowork-dev/xiaowork-blog/apps/api/internal/config"
	"github.com/xiaowork-dev/xiaowork-blog/apps/api/internal/httpapi"
	xmail "github.com/xiaowork-dev/xiaowork-blog/apps/api/internal/mail"
	"github.com/xiaowork-dev/xiaowork-blog/apps/api/internal/media"
	"github.com/xiaowork-dev/xiaowork-blog/apps/api/internal/platform/database/dbtest"
	"github.com/xiaowork-dev/xiaowork-blog/apps/api/internal/site"
)

const siteOrigin = "http://127.0.0.1:3000"

type harness struct {
	t    *testing.T
	pool *pgxpool.Pool
	srv  *httptest.Server
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	h := &harness{t: t, pool: dbtest.NewMigratedPool(t)}
	u, _ := url.Parse(siteOrigin)
	cfg := &config.Config{
		PublicBaseURL: u, SecretKey: []byte("article-test-secret-key-0123456789abc"),
		SessionTTL: time.Hour, VerifyTokenTTL: time.Hour, ResetTokenTTL: time.Hour, EmailTokenTTL: time.Hour,
		SMTP: config.SMTP{Timeout: time.Second, From: &mail.Address{Address: "no-reply@xiaowork.test"}},
	}
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	authSvc := auth.NewService(h.pool, cfg, &xmail.Recorder{}, auth.NewPasswordHasher(auth.DefaultArgon2, 2), logger)
	root := t.TempDir()
	storage := media.NewStorage(root)
	if err := storage.Init(); err != nil {
		t.Fatal(err)
	}
	mediaSvc := media.NewService(h.pool, authSvc, storage, media.Options{
		Root: root, Media: media.Limits{MaxBytes: 2 << 20, MaxPixels: 20_000_000}, Avatar: media.Limits{MaxBytes: 1 << 20, MaxPixels: 4_000_000},
	}, logger)
	articles := article.NewService(h.pool, "127.0.0.1:3000", authSvc, logger)
	h.srv = httptest.NewServer(httpapi.NewRouter(httpapi.Deps{
		Ping: h.pool.Ping, Auth: authSvc, Media: mediaSvc, Articles: articles, Site: site.NewService(h.pool, "127.0.0.1:3000"), Admin: admin.NewService(h.pool, logger),
		Comments: comment.NewService(h.pool, authSvc, logger), Logger: logger,
	}))
	t.Cleanup(h.srv.Close)
	return h
}

type client struct {
	h    *harness
	http *http.Client
	id   uuid.UUID
	csrf string
	ua   string // User-Agent; empty keeps Go's default (which the view counter treats as a bot)
}

func (h *harness) user(role string) *client {
	h.t.Helper()
	return h.account(role, "tester", true, "ACTIVE")
}

// account creates a signed-in user with the given verification state and account status.
func (h *harness) account(role, nickname string, verified bool, status string) *client {
	h.t.Helper()
	id := uuid.New()
	email := id.String()[:8] + "@example.test"
	h.exec(`INSERT INTO users (id, email, email_normalized, password_hash, nickname, role, status, email_verified_at)
		VALUES ($1, $2, $2, 'x', $3, $4, $5, CASE WHEN $6::boolean THEN now() END)`, id, email, nickname, role, status, verified)
	h.exec(`INSERT INTO user_profiles (user_id) VALUES ($1)`, id)
	raw := "session-" + id.String()
	sum := sha256.Sum256([]byte(raw))
	h.exec(`INSERT INTO auth_sessions (id, user_id, token_digest, expires_at) VALUES ($1, $2, $3, now() + interval '1 hour')`, uuid.New(), id, sum[:])
	jar, _ := cookiejar.New(nil)
	u, _ := url.Parse(h.srv.URL)
	jar.SetCookies(u, []*http.Cookie{{Name: "blog_session", Value: raw}})
	return &client{h: h, http: &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, id: id}
}

func (h *harness) anon() *client {
	jar, _ := cookiejar.New(nil)
	return &client{h: h, http: &http.Client{Jar: jar}}
}

func (h *harness) exec(sql string, args ...any) {
	h.t.Helper()
	if _, err := h.pool.Exec(context.Background(), sql, args...); err != nil {
		h.t.Fatal(err)
	}
}

func (h *harness) scalar(sql string, args ...any) any {
	h.t.Helper()
	var v any
	if err := h.pool.QueryRow(context.Background(), sql, args...).Scan(&v); err != nil {
		h.t.Fatal(err)
	}
	return v
}

type response struct {
	Status int
	Header http.Header
	Body   []byte
	JSON   map[string]any
}

func (r response) code() string {
	if e, ok := r.JSON["error"].(map[string]any); ok {
		s, _ := e["code"].(string)
		return s
	}
	return ""
}

func (r response) data() map[string]any {
	d, _ := r.JSON["data"].(map[string]any)
	return d
}

func (r response) details() map[string]any {
	if e, ok := r.JSON["error"].(map[string]any); ok {
		d, _ := e["details"].(map[string]any)
		return d
	}
	return nil
}

func (c *client) raw(method, path string, body io.Reader, contentType string) response {
	c.h.t.Helper()
	if method != http.MethodGet && c.csrf == "" {
		r := c.raw(http.MethodGet, "/api/v1/auth/csrf", nil, "")
		c.csrf, _ = r.data()["csrf_token"].(string)
	}
	req, _ := http.NewRequest(method, c.h.srv.URL+path, body)
	req.Header.Set("Origin", siteOrigin)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if c.csrf != "" {
		req.Header.Set("X-CSRF-Token", c.csrf)
	}
	if c.ua != "" {
		req.Header.Set("User-Agent", c.ua)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		c.h.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	out := response{Status: resp.StatusCode, Header: resp.Header, Body: b}
	_ = json.Unmarshal(b, &out.JSON)
	return out
}

func (c *client) json(method, path string, body any) response {
	c.h.t.Helper()
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	return c.raw(method, path, r, "application/json")
}

func (c *client) get(path string) response { return c.raw(http.MethodGet, path, nil, "") }

func expect(t *testing.T, r response, status int, code string) {
	t.Helper()
	if r.Status != status || r.code() != code {
		t.Fatalf("got %d %q (%s), want %d %q", r.Status, r.code(), r.Body, status, code)
	}
}

// ---- article helpers ----

// draft is the full-snapshot DTO of PUT /admin/articles/{id}/draft.
type draft map[string]any

func newDraft(title, body string) draft {
	return draft{"title": title, "summary": nil, "body_markdown": body, "candidate_slug": nil, "category_id": nil,
		"tag_ids": []string{}, "cover_media_id": nil, "seo_title": nil, "seo_description": nil, "comments_enabled": true}
}

func (d draft) with(k string, v any) draft {
	out := draft{}
	for key, val := range d {
		out[key] = val
	}
	out[k] = v
	return out
}

func (c *client) create() string {
	c.h.t.Helper()
	r := c.json(http.MethodPost, "/api/v1/admin/articles", nil)
	if r.Status != 201 || r.data()["draft"].(map[string]any)["revision"] != float64(1) || r.data()["status"] != "DRAFT" {
		c.h.t.Fatalf("create: %d %s", r.Status, r.Body)
	}
	return r.data()["article_id"].(string)
}

func (c *client) save(id string, rev int, d draft) response {
	c.h.t.Helper()
	return c.json(http.MethodPut, "/api/v1/admin/articles/"+id+"/draft", d.with("expected_revision", rev))
}

func (c *client) mustSave(id string, rev int, d draft) int {
	c.h.t.Helper()
	r := c.save(id, rev, d)
	if r.Status != 200 {
		c.h.t.Fatalf("save rev %d: %d %s", rev, r.Status, r.Body)
	}
	return int(r.data()["revision"].(float64))
}

func (c *client) publish(id string, rev int) response {
	c.h.t.Helper()
	return c.json(http.MethodPost, "/api/v1/admin/articles/"+id+"/publish", map[string]any{"expected_draft_revision": rev})
}

func (c *client) uploadPNG(name string, w, h int) string {
	c.h.t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	var pngBuf bytes.Buffer
	_ = png.Encode(&pngBuf, img)
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, _ := mw.CreateFormFile("file", name)
	_, _ = fw.Write(pngBuf.Bytes())
	_ = mw.Close()
	r := c.raw(http.MethodPost, "/api/v1/admin/media", &body, mw.FormDataContentType())
	if r.Status != 201 {
		c.h.t.Fatalf("upload: %d %s", r.Status, r.Body)
	}
	return r.data()["id"].(string)
}

func (c *client) category(name, slug string) string {
	c.h.t.Helper()
	r := c.json(http.MethodPost, "/api/v1/admin/categories", map[string]any{"name": name, "slug": slug})
	if r.Status != 201 {
		c.h.t.Fatalf("category: %d %s", r.Status, r.Body)
	}
	return r.data()["id"].(string)
}

func (c *client) tag(name, slug string) string {
	c.h.t.Helper()
	r := c.json(http.MethodPost, "/api/v1/admin/tags", map[string]any{"name": name, "slug": slug})
	if r.Status != 201 {
		c.h.t.Fatalf("tag: %d %s", r.Status, r.Body)
	}
	return r.data()["id"].(string)
}

func (h *harness) public(slug string) response { return h.anon().get("/api/v1/articles/" + slug) }
