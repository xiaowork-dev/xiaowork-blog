package site

import "testing"

func TestSafeExternalURL(t *testing.T) {
	ok := []string{"https://github.com/xiaowork-dev", "https://example.com/a?b=1#c", "mailto:hello@example.test"}
	bad := []string{
		"", "http://example.com", "javascript:alert(1)", "JAVASCRIPT:alert(1)", "data:text/html,x", "vbscript:x",
		"https://", "https:///path", "//example.com", "https://user:pw@example.com", "https://exa mple.com",
		"mailto:", "mailto:a b@example.test", "mailto:x@y", "mailto:hello@example.test?subject=x", " https://example.com",
		"ftp://example.com",
	}
	for _, u := range ok {
		if !SafeExternalURL(u, true) {
			t.Errorf("rejected %q", u)
		}
	}
	for _, u := range bad {
		if SafeExternalURL(u, true) {
			t.Errorf("accepted %q", u)
		}
	}
	if SafeExternalURL("mailto:hello@example.test", false) {
		t.Error("mailto accepted where only https is allowed")
	}
}

func TestSafeInternalPath(t *testing.T) {
	for _, p := range []string{"/", "/about", "/categories/dev", "/archives?year=2026", "/search?q=go", "/administrator-notes"} {
		if !SafeInternalPath(p) {
			t.Errorf("rejected %q", p)
		}
	}
	for _, p := range []string{
		"", "about", "//evil.example", "/\\evil", "/admin", "/admin/articles", "/account/profile", "/auth/login",
		"/api/v1/site", "/media/x/file", "/a b", "https://example.com", "/admin?x=1",
	} {
		if SafeInternalPath(p) {
			t.Errorf("accepted %q", p)
		}
	}
}
