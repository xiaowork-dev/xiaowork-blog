package auth_test

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/mail"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/xiaowork-dev/xiaowork-blog/apps/api/internal/auth"
	"github.com/xiaowork-dev/xiaowork-blog/apps/api/internal/config"
	xmail "github.com/xiaowork-dev/xiaowork-blog/apps/api/internal/mail"
)

// Registration, duplicate e-mail, unverified login refusal, one-time verification, login, me, logout.
func TestRegisterVerifyLoginLogout(t *testing.T) {
	h := newHarness(t, nil)
	c := h.client()

	r := c.register("Reader@Example.com", "读者", goodPassword)
	if r.Status != 201 || r.data()["registration_status"] != "PENDING_EMAIL_VERIFICATION" || r.data()["email_delivery"] != "accepted" {
		t.Fatalf("register: %d %s", r.Status, r.Raw)
	}
	path, token, n := h.mailTo("Reader@Example.com")
	if path != "/auth/verify" || n != 1 {
		t.Fatalf("verification mail path=%q count=%d", path, n)
	}

	// Duplicate (different case) and malformed registrations.
	expect(t, h.client().register("reader@EXAMPLE.com", "另一个", goodPassword), 409, "EMAIL_ALREADY_USED")
	r = h.client().register("bad-email", "", "short")
	expect(t, r, 422, "VALIDATION_FAILED")
	fields := r.Body["error"].(map[string]any)["details"].(map[string]any)["fields"].(map[string]any)
	if len(fields) != 3 {
		t.Fatalf("field errors = %v", fields)
	}
	// A public registration cannot ask for a role.
	other := h.client()
	expect(t, other.post("/api/v1/auth/register", map[string]string{
		"email": "x@example.com", "nickname": "x", "password": goodPassword, "role": "ADMIN"}), 400, "INVALID_REQUEST")

	// Unverified accounts cannot log in (API-DD §3.5) ...
	expect(t, c.login("reader@example.com", goodPassword), 403, "EMAIL_NOT_VERIFIED")
	// ... nor pass the U permission (commenting, P6) even with a session.
	h.exec(`INSERT INTO auth_sessions (id, user_id, token_digest, expires_at)
		SELECT gen_random_uuid(), id, sha256('probe'::bytea), now() + interval '1 hour' FROM users WHERE email_normalized = 'reader@example.com'`)
	probe := h.client()
	probe.http.Jar.SetCookies(mustURL(h.srv.URL), []*http.Cookie{{Name: "blog_session", Value: "probe"}})
	expect(t, probe.do(http.MethodGet, "/probe/verified", nil), 403, "EMAIL_NOT_VERIFIED")

	expect(t, c.post("/api/v1/auth/verify-email", map[string]string{"token": token}), 200, "")
	expect(t, c.post("/api/v1/auth/verify-email", map[string]string{"token": token}), 422, "TOKEN_INVALID_OR_EXPIRED") // reuse
	expect(t, c.post("/api/v1/auth/verify-email", map[string]string{"token": "not-a-token"}), 422, "TOKEN_INVALID_OR_EXPIRED")

	anonCSRF := c.csrf
	r = c.login("READER@example.com", goodPassword)
	if r.Status != 200 || r.data()["user"].(map[string]any)["role"] != "USER" {
		t.Fatalf("login: %d %s", r.Status, r.Raw)
	}
	cookie := r.Header.Get("Set-Cookie")
	if !strings.Contains(cookie, "HttpOnly") || !strings.Contains(cookie, "SameSite=Lax") || !strings.Contains(cookie, "Path=/") {
		t.Fatalf("session cookie attributes: %s", cookie)
	}
	me := c.me()
	if me.Status != 200 || me.data()["email"] != "Reader@Example.com" || me.data()["email_verified"] != true ||
		strings.Contains(me.Raw, "password") || me.Header.Get("Cache-Control") != "private, no-store" {
		t.Fatalf("me: %d %s %v", me.Status, me.Raw, me.Header)
	}
	expect(t, h.client().do(http.MethodGet, "/probe/verified", nil), 401, "AUTH_REQUIRED")
	if pr := c.do(http.MethodGet, "/probe/verified", nil); pr.Status != 204 {
		t.Fatalf("verified probe: %d %s", pr.Status, pr.Raw)
	}

	// The anonymous CSRF token stops working once the context becomes a session (rotation).
	saved := c.csrf
	c.csrf = anonCSRF
	expect(t, c.do(http.MethodPost, "/api/v1/auth/logout", nil), 403, "CSRF_INVALID")
	c.csrf = saved

	if r := c.post("/api/v1/auth/logout", nil); r.Status != 204 {
		t.Fatalf("logout: %d %s", r.Status, r.Raw)
	}
	expect(t, c.me(), 401, "AUTH_REQUIRED")
}

// Expired, reused and superseded verification links; resend behaviour and its rate limit.
func TestVerificationExpiryAndResend(t *testing.T) {
	h := newHarness(t, nil)
	c := h.client()
	c.register("late@example.com", "late", goodPassword)
	_, first, _ := h.mailTo("late@example.com")

	h.exec(`UPDATE auth_tokens SET created_at = now() - interval '2 days', expires_at = now() - interval '1 day'`)
	expect(t, c.post("/api/v1/auth/verify-email", map[string]string{"token": first}), 422, "TOKEN_INVALID_OR_EXPIRED")

	resend := func(email string) response {
		return c.post("/api/v1/auth/resend-verification", map[string]string{"email": email})
	}
	if r := resend("late@example.com"); r.Status != 202 {
		t.Fatalf("resend: %d %s", r.Status, r.Raw)
	}
	_, second, n := h.mailTo("late@example.com")
	if n != 2 || second == first {
		t.Fatalf("resend mail count=%d", n)
	}
	limited := resend("late@example.com") // 1 per minute per address
	expect(t, limited, 429, "RATE_LIMITED")
	if ra, err := strconv.Atoi(limited.Header.Get("Retry-After")); err != nil || ra < 1 || ra > 60 {
		t.Fatalf("Retry-After = %q", limited.Header.Get("Retry-After"))
	}
	// Unknown address: same 202, no mail (no enumeration).
	if r := resend("nobody@example.com"); r.Status != 202 {
		t.Fatalf("resend unknown: %d", r.Status)
	}
	if _, _, n := h.mailTo("nobody@example.com"); n != 0 {
		t.Fatal("mail sent to an unknown address")
	}

	expect(t, c.post("/api/v1/auth/verify-email", map[string]string{"token": second}), 200, "")
	expect(t, c.post("/api/v1/auth/verify-email", map[string]string{"token": second}), 422, "TOKEN_INVALID_OR_EXPIRED")
	// Already verified: still 202, but nothing is sent.
	h.exec(`DELETE FROM auth_rate_windows`)
	if r := resend("late@example.com"); r.Status != 202 {
		t.Fatalf("resend verified: %d", r.Status)
	}
	if _, _, n := h.mailTo("late@example.com"); n != 2 {
		t.Fatalf("verified account received another mail (%d)", n)
	}
}

// Wrong password, unknown account, per-address failure limit, per-IP limit, disabled account.
func TestLoginFailuresAndRateLimits(t *testing.T) {
	h := newHarness(t, nil)
	h.verifiedUser("owner@example.com")

	c := h.client()
	wrong := c.login("owner@example.com", "wrong-password-123")
	unknown := c.login("ghost@example.com", "wrong-password-123")
	expect(t, wrong, 401, "INVALID_CREDENTIALS")
	expect(t, unknown, 401, "INVALID_CREDENTIALS")
	if wrong.Body["error"].(map[string]any)["message"] != unknown.Body["error"].(map[string]any)["message"] {
		t.Fatal("wrong password and unknown account must look identical")
	}
	for i := 0; i < 4; i++ { // failures 2..5 for this address (from fresh IPs)
		expect(t, h.client().login("owner@example.com", "wrong-password-123"), 401, "INVALID_CREDENTIALS")
	}
	r := h.client().login("owner@example.com", goodPassword) // even the right password is blocked now
	expect(t, r, 429, "RATE_LIMITED")
	if ra, err := strconv.Atoi(r.Header.Get("Retry-After")); err != nil || ra < 1 || ra > 600 {
		t.Fatalf("Retry-After = %q", r.Header.Get("Retry-After"))
	}

	// 10 attempts per IP per 10 minutes, whatever the address.
	ip := h.client()
	for i := 0; i < 10; i++ {
		expect(t, ip.login("ip"+strconv.Itoa(i)+"@example.com", "wrong-password-123"), 401, "INVALID_CREDENTIALS")
	}
	expect(t, ip.login("another@example.com", "wrong-password-123"), 429, "RATE_LIMITED")

	h.exec(`DELETE FROM auth_rate_windows`)
	h.exec(`UPDATE users SET status = 'DISABLED' WHERE email_normalized = 'owner@example.com'`)
	expect(t, h.client().login("owner@example.com", goodPassword), 403, "ACCOUNT_DISABLED")
}

// Session expiry, logout-all across devices, disabling a signed-in account.
func TestSessionExpiryRevocationAndDisable(t *testing.T) {
	h := newHarness(t, nil)
	a := h.verifiedUser("multi@example.com")
	b := h.client()
	if r := b.login("multi@example.com", goodPassword); r.Status != 200 {
		t.Fatal(r.Raw)
	}

	h.exec(`UPDATE auth_sessions SET created_at = now() - interval '15 days', expires_at = now() - interval '1 day'
		WHERE id = (SELECT id FROM auth_sessions ORDER BY created_at LIMIT 1)`)
	expect(t, a.me(), 401, "SESSION_EXPIRED")
	expect(t, a.me(), 401, "AUTH_REQUIRED") // the stale cookie was cleared
	if b.me().Status != 200 {
		t.Fatal("other device affected by one session expiring")
	}

	a.csrf = ""
	if r := a.login("multi@example.com", goodPassword); r.Status != 200 {
		t.Fatal(r.Raw)
	}
	if r := b.post("/api/v1/auth/logout-all", nil); r.Status != 204 {
		t.Fatalf("logout-all: %d %s", r.Status, r.Raw)
	}
	expect(t, a.me(), 401, "SESSION_EXPIRED")
	expect(t, b.me(), 401, "AUTH_REQUIRED")

	a.csrf = ""
	a.login("multi@example.com", goodPassword)
	h.exec(`UPDATE users SET status = 'DISABLED' WHERE email_normalized = 'multi@example.com'`)
	expect(t, a.me(), 403, "ACCOUNT_DISABLED")
}

// Forgot/reset: uniform answers, rate limit, one-time token, old password and sessions invalidated.
func TestPasswordResetFlow(t *testing.T) {
	h := newHarness(t, nil)
	a := h.verifiedUser("forgot@example.com")
	c := h.client()

	forgot := func(email string) response {
		return c.post("/api/v1/auth/forgot-password", map[string]string{"email": email})
	}
	if r := forgot("nobody@example.com"); r.Status != 202 {
		t.Fatal(r.Raw)
	}
	if r := forgot("Forgot@Example.com"); r.Status != 202 {
		t.Fatal(r.Raw)
	}
	path, token, n := h.mailTo("forgot@example.com")
	if path != "/auth/reset-password" || n != 2 { // verification mail + reset mail
		t.Fatalf("reset mail path=%q count=%d", path, n)
	}
	if _, _, n := h.mailTo("nobody@example.com"); n != 0 {
		t.Fatal("reset mail sent to unknown address")
	}
	expect(t, forgot("forgot@example.com"), 429, "RATE_LIMITED")

	reset := func(token, pw string) response {
		return c.post("/api/v1/auth/reset-password", map[string]string{"token": token, "new_password": pw})
	}
	expect(t, reset(token, "short"), 422, "VALIDATION_FAILED")
	expect(t, reset(token, "brand-new-password-42"), 200, "")
	expect(t, reset(token, "another-password-42"), 422, "TOKEN_INVALID_OR_EXPIRED")

	expect(t, a.me(), 401, "SESSION_EXPIRED") // all sessions revoked
	expect(t, h.client().login("forgot@example.com", goodPassword), 401, "INVALID_CREDENTIALS")
	if r := h.client().login("forgot@example.com", "brand-new-password-42"); r.Status != 200 {
		t.Fatalf("login with new password: %d %s", r.Status, r.Raw)
	}

	// An expired reset link is refused.
	h.exec(`DELETE FROM auth_rate_windows`)
	forgot("forgot@example.com")
	_, token, _ = h.mailTo("forgot@example.com")
	h.exec(`UPDATE auth_tokens SET created_at = now() - interval '2 hours', expires_at = now() - interval '1 hour' WHERE purpose = 'RESET_PASSWORD'`)
	expect(t, reset(token, "yet-another-password-1"), 422, "TOKEN_INVALID_OR_EXPIRED")
}

// Signed-in password change and e-mail change (verified via the new address).
func TestChangePasswordAndEmail(t *testing.T) {
	h := newHarness(t, nil)
	a := h.verifiedUser("me@example.com")
	b := h.client()
	b.login("me@example.com", goodPassword)
	h.verifiedUser("taken@example.com")

	r := a.post("/api/v1/me/change-password", map[string]string{"current_password": "wrong-password-0", "new_password": "next-password-123"})
	expect(t, r, 422, "VALIDATION_FAILED")
	r = a.post("/api/v1/me/change-password", map[string]string{"current_password": goodPassword, "new_password": "next-password-123"})
	if r.Status != 200 || r.data()["reauthentication_required"] != true {
		t.Fatalf("change password: %d %s", r.Status, r.Raw)
	}
	expect(t, a.me(), 401, "AUTH_REQUIRED")
	expect(t, b.me(), 401, "SESSION_EXPIRED")
	a.csrf, b.csrf = "", ""
	if r := a.login("me@example.com", "next-password-123"); r.Status != 200 {
		t.Fatal(r.Raw)
	}
	b.login("me@example.com", "next-password-123")

	expect(t, a.post("/api/v1/me/change-email/request", map[string]string{"new_email": "TAKEN@example.com"}), 409, "EMAIL_ALREADY_USED")
	if r := a.post("/api/v1/me/change-email/request", map[string]string{"new_email": "New@Example.com"}); r.Status != 202 {
		t.Fatalf("change email request: %d %s", r.Status, r.Raw)
	}
	path, token, _ := h.mailTo("New@Example.com")
	if path != "/auth/confirm-email" {
		t.Fatalf("change email link %q", path)
	}
	// Another account cannot use the link; the token survives for its owner.
	stranger := h.verifiedUser("stranger@example.com")
	expect(t, stranger.post("/api/v1/me/change-email/confirm", map[string]string{"token": token}), 422, "TOKEN_INVALID_OR_EXPIRED")
	expect(t, h.client().post("/api/v1/me/change-email/confirm", map[string]string{"token": token}), 401, "AUTH_REQUIRED")

	r = a.post("/api/v1/me/change-email/confirm", map[string]string{"token": token})
	if r.Status != 200 || r.data()["email"] != "New@Example.com" {
		t.Fatalf("confirm: %d %s", r.Status, r.Raw)
	}
	if a.me().data()["email"] != "New@Example.com" {
		t.Fatal("me does not show the new e-mail")
	}
	expect(t, b.me(), 401, "SESSION_EXPIRED") // other devices signed out
	expect(t, h.client().login("me@example.com", "next-password-123"), 401, "INVALID_CREDENTIALS")
	if r := h.client().login("new@example.com", "next-password-123"); r.Status != 200 {
		t.Fatalf("login with new e-mail: %d %s", r.Status, r.Raw)
	}
}

// CSRF token and Origin are both required on state-changing requests.
func TestCSRFAndOrigin(t *testing.T) {
	h := newHarness(t, nil)
	c := h.client()
	body := map[string]string{"email": "csrf@example.com", "nickname": "c", "password": goodPassword}

	expect(t, c.do(http.MethodPost, "/api/v1/auth/register", body), 403, "CSRF_INVALID") // no cookie, no token
	c.refreshCSRF()
	expect(t, c.do(http.MethodPost, "/api/v1/auth/register", body, withoutCSRF()), 403, "CSRF_INVALID")
	expect(t, c.do(http.MethodPost, "/api/v1/auth/register", body, withOrigin("https://evil.example.com")), 403, "CSRF_INVALID")
	noOrigin := func(r *http.Request) { r.Header.Del("Origin") }
	expect(t, c.do(http.MethodPost, "/api/v1/auth/register", body, noOrigin), 403, "CSRF_INVALID")
	sameSite := func(r *http.Request) { r.Header.Del("Origin"); r.Header.Set("Sec-Fetch-Site", "same-origin") }
	if r := c.do(http.MethodPost, "/api/v1/auth/register", body, sameSite); r.Status != 201 {
		t.Fatalf("same-origin fetch metadata: %d %s", r.Status, r.Raw)
	}
	// A token issued to one browser does not work for another.
	other := h.client()
	other.refreshCSRF()
	other.csrf = c.csrf
	expect(t, other.do(http.MethodPost, "/api/v1/auth/login", map[string]string{"email": "csrf@example.com", "password": goodPassword}), 403, "CSRF_INVALID")
}

// Admin guard (API-04) and the protected first-admin bootstrap.
func TestAdminAccessAndBootstrap(t *testing.T) {
	h := newHarness(t, nil)
	ctx := context.Background()
	anon := h.client()
	expect(t, anon.do(http.MethodGet, "/api/v1/admin/overview", nil), 401, "AUTH_REQUIRED")
	user := h.verifiedUser("user@example.com")
	expect(t, user.do(http.MethodGet, "/api/v1/admin/overview", nil), 403, "FORBIDDEN")
	expect(t, user.post("/api/v1/admin/articles", map[string]string{}), 403, "FORBIDDEN")

	if _, err := auth.CreateFirstAdmin(ctx, h.pool, h.hasher, "owner@example.com", "XiaoWork", "short"); err == nil {
		t.Fatal("weak admin password accepted")
	}
	if _, err := auth.CreateFirstAdmin(ctx, h.pool, h.hasher, "Owner@Example.com", "XiaoWork", goodPassword); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.CreateFirstAdmin(ctx, h.pool, h.hasher, "second@example.com", "Second", goodPassword); !errors.Is(err, auth.ErrAdminExists) {
		t.Fatalf("second admin: %v", err)
	}
	admin := h.client()
	if r := admin.login("owner@example.com", goodPassword); r.Status != 200 || r.data()["user"].(map[string]any)["role"] != "ADMIN" {
		t.Fatalf("admin login: %d %s", r.Status, r.Raw)
	}
	// The guard passes; the admin features themselves are later phases.
	expect(t, admin.do(http.MethodGet, "/api/v1/admin/overview", nil), 404, "NOT_FOUND")
}

// SMTP failures: registration still succeeds honestly, anonymous requests stay uniform,
// e-mail change reports 503 and leaves no live token.
func TestMailFailures(t *testing.T) {
	failing := &xmail.Recorder{}
	h := newHarness(t, failing)
	h.verifiedUser("ok@example.com") // mail works for this one
	failing.SetErr(errors.New("smtp: 421 service not available"))

	c := h.client()
	r := c.register("down@example.com", "down", goodPassword)
	if r.Status != 201 || r.data()["email_delivery"] != "unavailable" {
		t.Fatalf("register with SMTP down: %d %s", r.Status, r.Raw)
	}
	if r := c.post("/api/v1/auth/resend-verification", map[string]string{"email": "down@example.com"}); r.Status != 202 {
		t.Fatalf("resend with SMTP down: %d", r.Status)
	}
	h.svc.Wait()
	if !strings.Contains(h.logs.String(), `"msg":"mail delivery failed"`) {
		t.Fatal("mail failure was not logged as an operational event")
	}

	failing.SetErr(nil)
	a := h.client()
	a.login("ok@example.com", goodPassword)
	failing.SetErr(errors.New("dial tcp: i/o timeout"))
	expect(t, a.post("/api/v1/me/change-email/request", map[string]string{"new_email": "fresh@example.com"}), 503, "MAIL_UNAVAILABLE")
	var live int
	if err := h.pool.QueryRow(context.Background(), `SELECT count(*) FROM auth_tokens WHERE purpose = 'CHANGE_EMAIL'
		AND consumed_at IS NULL AND revoked_at IS NULL`).Scan(&live); err != nil || live != 0 {
		t.Fatalf("live change-email tokens after failed send = %d (%v)", live, err)
	}
}

// Network failure: the real SMTP mailer against a closed port.
func TestRegisterWithUnreachableSMTP(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()
	smtp := xmail.NewSMTP(config.SMTP{Host: "127.0.0.1", Port: port, TLS: config.SMTPTLSNone, Timeout: 2 * time.Second,
		From: &mail.Address{Address: "no-reply@xiaowork.test"}})
	h := newHarness(t, smtp)
	start := time.Now()
	r := h.client().register("net@example.com", "net", goodPassword)
	if r.Status != 201 || r.data()["email_delivery"] != "unavailable" || time.Since(start) > 8*time.Second {
		t.Fatalf("register with unreachable SMTP: %d %s after %s", r.Status, r.Raw, time.Since(start))
	}
}

// Database outage: JSON 503 instead of panics or leaked error text.
func TestDatabaseUnavailable(t *testing.T) {
	h := newHarness(t, nil)
	c := h.verifiedUser("db@example.com")
	other := h.client()
	other.refreshCSRF()
	h.pool.Close()

	for _, r := range []response{
		c.me(),
		other.post("/api/v1/auth/register", map[string]string{"email": "z@example.com", "nickname": "z", "password": goodPassword}),
		other.post("/api/v1/auth/login", map[string]string{"email": "db@example.com", "password": goodPassword}),
	} {
		expect(t, r, 503, "SERVICE_UNAVAILABLE")
		if strings.Contains(r.Raw, "pool") || strings.Contains(r.Raw, "closed") {
			t.Fatalf("internal error text leaked: %s", r.Raw)
		}
	}
	if r := c.do(http.MethodGet, "/health/ready", nil); r.Status != 503 {
		t.Fatalf("readiness with DB down: %d", r.Status)
	}
}

// Passwords, raw tokens and session cookies never reach logs; hashes are Argon2id.
func TestNoSecretsInLogsOrStorage(t *testing.T) {
	h := newHarness(t, nil)
	c := h.client()
	c.register("secret@example.com", "secret", goodPassword)
	_, verifyToken, _ := h.mailTo("secret@example.com")
	c.post("/api/v1/auth/verify-email", map[string]string{"token": verifyToken})
	r := c.login("secret@example.com", goodPassword)
	session := ""
	for _, ck := range c.http.Jar.Cookies(mustURL(h.srv.URL)) {
		if ck.Name == "blog_session" {
			session = ck.Value
		}
	}
	if r.Status != 200 || session == "" {
		t.Fatalf("login: %d %s", r.Status, r.Raw)
	}
	c.post("/api/v1/auth/forgot-password", map[string]string{"email": "secret@example.com"})
	_, resetToken, _ := h.mailTo("secret@example.com")

	logs := h.logs.String()
	for _, secret := range []string{goodPassword, verifyToken, resetToken, session} {
		if strings.Contains(logs, secret) {
			t.Fatalf("secret found in logs")
		}
	}
	var hash string
	var digest []byte
	var target pgtype.Text
	ctx := context.Background()
	_ = h.pool.QueryRow(ctx, `SELECT password_hash FROM users WHERE email_normalized = 'secret@example.com'`).Scan(&hash)
	_ = h.pool.QueryRow(ctx, `SELECT token_digest FROM auth_sessions LIMIT 1`).Scan(&digest)
	_ = h.pool.QueryRow(ctx, `SELECT target_email FROM auth_tokens LIMIT 1`).Scan(&target)
	if !strings.HasPrefix(hash, "$argon2id$") || strings.Contains(hash, goodPassword) || len(digest) != 32 || string(digest) == session {
		t.Fatal("credentials not stored as hash/digest")
	}
}
