package auth_test

import (
	"context"
	"strings"
	"testing"
	"time"
)

// P8-B12 "remember me": omitted or true keeps the persistent SESSION_TTL login; false gives a browser-session
// cookie (no Max-Age) backed by a session that expires within 12 hours on the server.
func TestLoginRememberMe(t *testing.T) {
	h := newHarness(t, nil)
	h.verifiedUser("remember@example.com").do("POST", "/api/v1/auth/logout", nil)

	sessionCookie := func(r response) string {
		for _, c := range r.Header.Values("Set-Cookie") {
			if strings.HasPrefix(c, "blog_session=") {
				return c
			}
		}
		t.Fatalf("no session cookie in %v", r.Header.Values("Set-Cookie"))
		return ""
	}
	latestExpiry := func() time.Duration {
		var expires time.Time
		if err := h.pool.QueryRow(context.Background(), `SELECT s.expires_at FROM auth_sessions s JOIN users u ON u.id = s.user_id
			WHERE u.email = 'remember@example.com' ORDER BY s.created_at DESC LIMIT 1`).Scan(&expires); err != nil {
			t.Fatal(err)
		}
		return time.Until(expires)
	}

	for _, tc := range []struct {
		name     string
		body     map[string]any
		maxAge   bool
		min, max time.Duration
	}{
		{"omitted", map[string]any{"email": "remember@example.com", "password": goodPassword}, true, 13 * 24 * time.Hour, 15 * 24 * time.Hour},
		{"true", map[string]any{"email": "remember@example.com", "password": goodPassword, "remember_me": true}, true, 13 * 24 * time.Hour, 15 * 24 * time.Hour},
		{"false", map[string]any{"email": "remember@example.com", "password": goodPassword, "remember_me": false}, false, 11 * time.Hour, 12*time.Hour + time.Minute},
	} {
		c := h.client()
		r := c.post("/api/v1/auth/login", tc.body)
		if r.Status != 200 {
			t.Fatalf("%s: login %d %s", tc.name, r.Status, r.Raw)
		}
		if got := strings.Contains(sessionCookie(r), "Max-Age="); got != tc.maxAge {
			t.Errorf("%s: cookie %q, want Max-Age present = %v", tc.name, sessionCookie(r), tc.maxAge)
		}
		if d := latestExpiry(); d < tc.min || d > tc.max {
			t.Errorf("%s: session expires in %v, want %v–%v", tc.name, d, tc.min, tc.max)
		}
		c.refreshCSRF()
		if me := c.me(); me.Status != 200 {
			t.Errorf("%s: /auth/me after login = %d", tc.name, me.Status)
		}
	}
}
