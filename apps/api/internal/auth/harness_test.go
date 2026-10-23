package auth_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/mail"
	"net/netip"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/xiaowork-dev/xiaowork-blog/apps/api/internal/auth"
	"github.com/xiaowork-dev/xiaowork-blog/apps/api/internal/config"
	"github.com/xiaowork-dev/xiaowork-blog/apps/api/internal/httpapi"
	xmail "github.com/xiaowork-dev/xiaowork-blog/apps/api/internal/mail"
	"github.com/xiaowork-dev/xiaowork-blog/apps/api/internal/platform/database/dbtest"
)

const (
	siteOrigin   = "http://127.0.0.1:3000"
	goodPassword = "correct-horse-battery-1"
)

type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

type harness struct {
	t      *testing.T
	pool   *pgxpool.Pool
	cfg    *config.Config
	svc    *auth.Service
	mailer *xmail.Recorder
	hasher *auth.PasswordHasher
	router http.Handler
	srv    *httptest.Server
	logs   *syncBuffer
	nextIP int
}

func testConfig() *config.Config {
	u, _ := url.Parse(siteOrigin)
	return &config.Config{
		PublicBaseURL: u, SecretKey: []byte("test-secret-key-0123456789abcdefghij"),
		SessionTTL: 14 * 24 * time.Hour, VerifyTokenTTL: 24 * time.Hour, ResetTokenTTL: 30 * time.Minute, EmailTokenTTL: 24 * time.Hour,
		TrustedProxies: []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8")},
		SMTP:           config.SMTP{Timeout: 2 * time.Second, From: &mail.Address{Address: "no-reply@xiaowork.test"}},
	}
}

func newHarness(t *testing.T, mailer xmail.Mailer) *harness {
	t.Helper()
	h := &harness{t: t, pool: dbtest.NewMigratedPool(t), cfg: testConfig(), logs: &syncBuffer{}, nextIP: 1}
	if mailer == nil {
		mailer = &xmail.Recorder{}
	}
	h.mailer, _ = mailer.(*xmail.Recorder) // nil for the real SMTP mailer
	logger := slog.New(slog.NewJSONHandler(h.logs, nil))
	h.hasher = auth.NewPasswordHasher(auth.DefaultArgon2, 4)
	h.svc = auth.NewService(h.pool, h.cfg, mailer, h.hasher, logger)
	mux := httpapi.NewRouter(httpapi.Deps{Ping: h.pool.Ping, Auth: h.svc, Logger: logger})
	// Probe route for the U permission level (comments arrive in P6).
	mux.With(h.svc.Authenticate, h.svc.RequireVerified).Get("/probe/verified", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	h.router = mux
	h.srv = httptest.NewServer(mux)
	t.Cleanup(func() {
		h.srv.Close()
		h.svc.Wait()
	})
	return h
}

// client is one browser: own cookie jar, own client IP (via trusted X-Forwarded-For) and CSRF token.
type client struct {
	h    *harness
	http *http.Client
	ip   string
	csrf string
}

func (h *harness) client() *client {
	jar, _ := cookiejar.New(nil)
	h.nextIP++
	return &client{h: h, http: &http.Client{Jar: jar}, ip: "198.51.100." + strconv.Itoa(h.nextIP)}
}

type response struct {
	Status int
	Header http.Header
	Body   map[string]any
	Raw    string
}

func (r response) code() string {
	if e, ok := r.Body["error"].(map[string]any); ok {
		s, _ := e["code"].(string)
		return s
	}
	return ""
}

func (r response) data() map[string]any {
	d, _ := r.Body["data"].(map[string]any)
	return d
}

type reqOpt func(*http.Request)

func withOrigin(o string) reqOpt { return func(r *http.Request) { r.Header.Set("Origin", o) } }
func withoutCSRF() reqOpt        { return func(r *http.Request) { r.Header.Del("X-CSRF-Token") } }

func (c *client) do(method, path string, body any, opts ...reqOpt) response {
	c.h.t.Helper()
	var reader io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		reader = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, c.h.srv.URL+path, reader)
	req.Header.Set("Origin", siteOrigin)
	req.Header.Set("X-Forwarded-For", c.ip)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.csrf != "" {
		req.Header.Set("X-CSRF-Token", c.csrf)
	}
	for _, o := range opts {
		o(req)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		c.h.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	out := response{Status: resp.StatusCode, Header: resp.Header, Raw: string(raw)}
	_ = json.Unmarshal(raw, &out.Body)
	return out
}

// refreshCSRF fetches the token for the client's current security context.
func (c *client) refreshCSRF() {
	c.h.t.Helper()
	r := c.do(http.MethodGet, "/api/v1/auth/csrf", nil)
	tok, _ := r.data()["csrf_token"].(string)
	if r.Status != 200 || tok == "" {
		c.h.t.Fatalf("csrf: %d %s", r.Status, r.Raw)
	}
	c.csrf = tok
}

func (c *client) post(path string, body any, opts ...reqOpt) response {
	c.h.t.Helper()
	if c.csrf == "" {
		c.refreshCSRF()
	}
	return c.do(http.MethodPost, path, body, opts...)
}

func (c *client) register(email, nickname, password string) response {
	return c.post("/api/v1/auth/register", map[string]string{"email": email, "nickname": nickname, "password": password})
}

func (c *client) login(email, password string) response {
	c.h.t.Helper()
	r := c.post("/api/v1/auth/login", map[string]string{"email": email, "password": password})
	if r.Status == 200 {
		c.refreshCSRF() // the context changed from anonymous to session
	}
	return r
}

func (c *client) me() response { return c.do(http.MethodGet, "/api/v1/auth/me", nil) }

var linkToken = regexp.MustCompile(`(/auth/[a-z-]+)\?token=([A-Za-z0-9_-]{43})`)

// mailTo waits for background deliveries and returns the path and token of the newest mail to addr.
func (h *harness) mailTo(addr string) (path, token string, count int) {
	h.t.Helper()
	h.svc.Wait()
	var last xmail.Message
	for _, m := range h.mailer.Messages() {
		if strings.EqualFold(m.To, addr) {
			last = m
			count++
		}
	}
	if count == 0 {
		return "", "", 0
	}
	m := linkToken.FindStringSubmatch(last.Text)
	if m == nil || !strings.Contains(last.Text, siteOrigin+m[1]) {
		h.t.Fatalf("mail without a site link: %q", last.Text)
	}
	return m[1], m[2], count
}

// verifiedUser registers, verifies and returns a signed-in client.
func (h *harness) verifiedUser(email string) *client {
	h.t.Helper()
	c := h.client()
	if r := c.register(email, "用户", goodPassword); r.Status != 201 {
		h.t.Fatalf("register %s: %d %s", email, r.Status, r.Raw)
	}
	_, token, _ := h.mailTo(email)
	if r := c.post("/api/v1/auth/verify-email", map[string]string{"token": token}); r.Status != 200 {
		h.t.Fatalf("verify: %d %s", r.Status, r.Raw)
	}
	if r := c.login(email, goodPassword); r.Status != 200 {
		h.t.Fatalf("login: %d %s", r.Status, r.Raw)
	}
	return c
}

func (h *harness) exec(sql string, args ...any) {
	h.t.Helper()
	if _, err := h.pool.Exec(context.Background(), sql, args...); err != nil {
		h.t.Fatal(err)
	}
}

func mustURL(raw string) *url.URL {
	u, err := url.Parse(raw)
	if err != nil {
		panic(err)
	}
	return u
}

func expect(t *testing.T, r response, status int, code string) {
	t.Helper()
	if r.Status != status || r.code() != code {
		t.Fatalf("got %d %q (%s), want %d %q", r.Status, r.code(), r.Raw, status, code)
	}
}
