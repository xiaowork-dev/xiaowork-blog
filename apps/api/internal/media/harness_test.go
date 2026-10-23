package media_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"hash/crc32"
	"image"
	"image/color"
	"image/color/palette"
	"image/gif"
	"image/jpeg"
	"image/png"
	"io"
	"io/fs"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/mail"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/xiaowork-dev/xiaowork-blog/apps/api/internal/auth"
	"github.com/xiaowork-dev/xiaowork-blog/apps/api/internal/config"
	"github.com/xiaowork-dev/xiaowork-blog/apps/api/internal/httpapi"
	xmail "github.com/xiaowork-dev/xiaowork-blog/apps/api/internal/mail"
	"github.com/xiaowork-dev/xiaowork-blog/apps/api/internal/media"
	"github.com/xiaowork-dev/xiaowork-blog/apps/api/internal/platform/database/dbtest"
)

const siteOrigin = "http://127.0.0.1:3000"

type harness struct {
	t       *testing.T
	pool    *pgxpool.Pool
	root    string
	storage *media.Storage
	svc     *media.Service
	srv     *httptest.Server
}

// Small limits keep the "too large" cases cheap: 2 MiB / 20 MP for articles, 256 KiB / 1 MP for avatars.
func newHarness(t *testing.T) *harness {
	t.Helper()
	h := &harness{t: t, pool: dbtest.NewMigratedPool(t), root: t.TempDir()}
	u, _ := url.Parse(siteOrigin)
	cfg := &config.Config{
		PublicBaseURL: u, SecretKey: []byte("media-test-secret-key-0123456789abcdef"),
		SessionTTL: time.Hour, VerifyTokenTTL: time.Hour, ResetTokenTTL: time.Hour, EmailTokenTTL: time.Hour,
		SMTP: config.SMTP{Timeout: time.Second, From: &mail.Address{Address: "no-reply@xiaowork.test"}},
	}
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	authSvc := auth.NewService(h.pool, cfg, &xmail.Recorder{}, auth.NewPasswordHasher(auth.DefaultArgon2, 2), logger)
	h.storage = media.NewStorage(h.root)
	if err := h.storage.Init(); err != nil {
		t.Fatal(err)
	}
	h.svc = media.NewService(h.pool, authSvc, h.storage, media.Options{
		Root:   h.root,
		Media:  media.Limits{MaxBytes: 2 << 20, MaxPixels: 20_000_000},
		Avatar: media.Limits{MaxBytes: 256 << 10, MaxPixels: 1_000_000},
	}, logger)
	h.srv = httptest.NewServer(httpapi.NewRouter(httpapi.Deps{Ping: h.pool.Ping, Auth: authSvc, Media: h.svc, Logger: logger}))
	t.Cleanup(h.srv.Close)
	return h
}

type client struct {
	h    *harness
	http *http.Client
	id   uuid.UUID
	csrf string
}

// user inserts an account with a live session (the auth flows themselves are covered in internal/auth).
func (h *harness) user(role string, verified bool) *client {
	h.t.Helper()
	id := uuid.New()
	email := id.String()[:8] + "@example.test"
	verifiedAt := "NULL"
	if verified {
		verifiedAt = "now()"
	}
	h.exec(`INSERT INTO users (id, email, email_normalized, password_hash, nickname, role, email_verified_at)
		VALUES ($1, $2, $2, 'x', 'tester', $3, `+verifiedAt+`)`, id, email, role)
	h.exec(`INSERT INTO user_profiles (user_id) VALUES ($1)`, id)
	raw := "session-" + id.String()
	sum := sha256.Sum256([]byte(raw))
	h.exec(`INSERT INTO auth_sessions (id, user_id, token_digest, expires_at) VALUES ($1, $2, $3, now() + interval '1 hour')`,
		uuid.New(), id, sum[:])
	jar, _ := cookiejar.New(nil)
	u, _ := url.Parse(h.srv.URL)
	jar.SetCookies(u, []*http.Cookie{{Name: "blog_session", Value: raw}})
	return &client{h: h, http: &http.Client{Jar: jar}, id: id}
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

func (c *client) do(method, path string, body io.Reader, contentType string) response {
	c.h.t.Helper()
	if method != http.MethodGet && method != http.MethodHead && c.csrf == "" {
		r := c.do(http.MethodGet, "/api/v1/auth/csrf", nil, "")
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
	resp, err := c.http.Do(req)
	if err != nil {
		c.h.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	out := response{Status: resp.StatusCode, Header: resp.Header, Body: raw}
	_ = json.Unmarshal(raw, &out.JSON)
	return out
}

func (c *client) get(path string) response { return c.do(http.MethodGet, path, nil, "") }

// upload posts multipart/form-data with one file part and optional text fields.
func (c *client) upload(path, filename string, data []byte, fields map[string]string) response {
	c.h.t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for k, v := range fields {
		_ = mw.WriteField(k, v)
	}
	fw, _ := mw.CreateFormFile("file", filename)
	_, _ = fw.Write(data)
	_ = mw.Close()
	return c.do(http.MethodPost, path, &buf, mw.FormDataContentType())
}

func (c *client) uploadAdmin(filename string, data []byte) response {
	return c.upload("/api/v1/admin/media", filename, data, nil)
}

func expect(t *testing.T, r response, status int, code string) {
	t.Helper()
	if r.Status != status || r.code() != code {
		t.Fatalf("got %d %q (%s), want %d %q", r.Status, r.code(), r.Body, status, code)
	}
}

// ---- image fixtures ----

func pngImage(w, h int) []byte {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.NRGBA{uint8(x), uint8(y), 128, 255})
		}
	}
	var b bytes.Buffer
	_ = png.Encode(&b, img)
	return b.Bytes()
}

func jpegImage(w, h int) []byte {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{uint8(x), 80, uint8(y), 255})
		}
	}
	var b bytes.Buffer
	_ = jpeg.Encode(&b, img, &jpeg.Options{Quality: 80})
	return b.Bytes()
}

func animatedGIF(frames, side int) []byte {
	g := &gif.GIF{}
	for i := 0; i < frames; i++ {
		img := image.NewPaletted(image.Rect(0, 0, side, side), palette.Plan9)
		img.SetColorIndex(i%side, i%side, uint8(i))
		g.Image = append(g.Image, img)
		g.Delay = append(g.Delay, 5)
	}
	var b bytes.Buffer
	_ = gif.EncodeAll(&b, g)
	return b.Bytes()
}

// 1×1 lossless WebP (Go has no WebP encoder).
var tinyWebP, _ = base64.StdEncoding.DecodeString("UklGRhoAAABXRUJQVlA4TA0AAAAvAAAAEAcQERGIiP4HAA==")

// pngBomb claims huge dimensions in IHDR (with a valid CRC) while the file itself is tiny.
func pngBomb(w, h uint32) []byte {
	b := pngImage(1, 1)
	binary.BigEndian.PutUint32(b[16:], w)
	binary.BigEndian.PutUint32(b[20:], h)
	binary.BigEndian.PutUint32(b[29:], crc32.ChecksumIEEE(b[12:29])) // IHDR type + data
	return b
}

// ---- disk helpers ----

// files lists every regular file under dir relative to the media root.
func (h *harness) files(dir string) []string {
	var out []string
	_ = filepath.WalkDir(filepath.Join(h.root, dir), func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			rel, _ := filepath.Rel(h.root, p)
			out = append(out, filepath.ToSlash(rel))
		}
		return nil
	})
	return out
}

func (h *harness) mediaRow(id string) (state, key string) {
	h.t.Helper()
	_ = h.pool.QueryRow(context.Background(), `SELECT state, storage_key FROM media_assets WHERE id = $1`, id).Scan(&state, &key)
	return
}

func fileExists(root, key string) bool {
	_, err := os.Stat(filepath.Join(root, filepath.FromSlash(key)))
	return err == nil
}

func noTempFiles(t *testing.T, h *harness) {
	t.Helper()
	if left := h.files("temp"); len(left) > 0 {
		t.Fatalf("temp files left behind: %v", left)
	}
}
