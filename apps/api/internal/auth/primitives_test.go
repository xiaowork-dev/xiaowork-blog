package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPasswordHashing(t *testing.T) {
	h := NewPasswordHasher(DefaultArgon2, 2)
	ctx := context.Background()
	hash, err := h.Hash(ctx, "correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(hash, "$argon2id$v=19$m=19456,t=2,p=1$") || strings.Contains(hash, "correct horse") {
		t.Fatalf("unexpected hash format: %s", hash)
	}
	other, _ := h.Hash(ctx, "correct horse battery staple")
	if other == hash {
		t.Fatal("hashes must be salted")
	}
	if ok, err := h.Verify(ctx, "correct horse battery staple", hash); err != nil || !ok {
		t.Fatalf("verify correct = %v, %v", ok, err)
	}
	if ok, _ := h.Verify(ctx, "wrong password!!", hash); ok {
		t.Fatal("wrong password verified")
	}
	for _, bad := range []string{"", "$2a$10$bcrypt", "$argon2id$v=19$m=999999999,t=1,p=1$AAAA$AAAA"} {
		if _, err := h.Verify(ctx, "x", bad); err == nil {
			t.Fatalf("malformed hash %q accepted", bad)
		}
	}
}

func TestEmailNormalization(t *testing.T) {
	ok := map[string]string{
		" Alice@Example.COM ":       "alice@example.com",
		"first.last+tag@sub.Ex.org": "first.last+tag@sub.ex.org", // no provider-specific folding
	}
	for in, want := range ok {
		display, normalized, valid := normalizeEmail(in)
		if !valid || normalized != want || display != strings.TrimSpace(in) {
			t.Errorf("%q -> %q %q %v", in, display, normalized, valid)
		}
	}
	for _, in := range []string{"", "plain", "a@b", "Name <a@example.com>", "a b@example.com", "用户@example.com",
		"a@-bad-.com", "a@example..com", strings.Repeat("a", 65) + "@example.com", "a@example.com\r\nBcc: x@y.z"} {
		if _, _, valid := normalizeEmail(in); valid {
			t.Errorf("%q accepted", in)
		}
	}
}

func TestNicknameAndPasswordRules(t *testing.T) {
	if n, ok := validNickname("  小明  "); !ok || n != "小明" {
		t.Fatalf("nickname = %q %v", n, ok)
	}
	for _, bad := range []string{"", "   ", strings.Repeat("长", 41), "a\u0000b", "a‮b"} {
		if _, ok := validNickname(bad); ok {
			t.Errorf("nickname %q accepted", bad)
		}
	}
	cases := map[string]string{"short": "TOO_SHORT", strings.Repeat("密", 12): "", strings.Repeat("x", 129): "TOO_LONG"}
	for pw, want := range cases {
		if got := passwordProblem(pw); got != want {
			t.Errorf("passwordProblem(len %d) = %q want %q", len(pw), got, want)
		}
	}
}

func TestTokensAndCSRF(t *testing.T) {
	raw, digest := newToken()
	if !validTokenFormat(raw) || len(digest) != 32 || string(tokenDigest(raw)) != string(digest) {
		t.Fatal("token/digest mismatch")
	}
	if validTokenFormat(raw+"x") || validTokenFormat("../../etc") {
		t.Fatal("bad token format accepted")
	}
	key := []byte(strings.Repeat("k", 32))
	if csrfToken(key, "a:1") == csrfToken(key, "s:1") || csrfToken(key, "a:1") != csrfToken(key, "a:1") {
		t.Fatal("csrf tokens must be deterministic per context and differ across contexts")
	}

	r := httptest.NewRequest(http.MethodPost, "/", nil)
	if sameOrigin(r, "https://blog.example.com") {
		t.Fatal("request without Origin or Fetch Metadata accepted")
	}
	r.Header.Set("Sec-Fetch-Site", "same-origin")
	if !sameOrigin(r, "https://blog.example.com") {
		t.Fatal("same-origin fetch metadata rejected")
	}
	r.Header.Set("Origin", "https://evil.example.com")
	if sameOrigin(r, "https://blog.example.com") {
		t.Fatal("foreign Origin accepted")
	}
}
