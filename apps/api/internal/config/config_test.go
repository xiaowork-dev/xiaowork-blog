package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func env(overrides map[string]string) func(string) string {
	base := map[string]string{
		"DATABASE_URL":    "postgres://u:p@db:5432/blog",
		"PUBLIC_BASE_URL": "https://blog.example.com",
		"APP_SECRET_KEY":  strings.Repeat("k", 40),
		"SMTP_HOST":       "smtp.example.com",
		"SMTP_FROM":       "XiaoWork Blog <no-reply@example.com>",
	}
	for k, v := range overrides {
		base[k] = v
	}
	return func(k string) string { return base[k] }
}

func TestLoadDefaults(t *testing.T) {
	c, err := LoadFrom(env(nil))
	if err != nil {
		t.Fatal(err)
	}
	if !c.SecureCookies || c.Origin() != "https://blog.example.com" {
		t.Fatalf("secure=%t origin=%s", c.SecureCookies, c.Origin())
	}
	if c.SessionTTL != 14*24*time.Hour || c.VerifyTokenTTL != 24*time.Hour || c.ResetTokenTTL != 30*time.Minute {
		t.Fatalf("unexpected TTL defaults: %v %v %v", c.SessionTTL, c.VerifyTokenTTL, c.ResetTokenTTL)
	}
	if c.SMTP.TLS != SMTPTLSStartTLS || c.SMTP.Port != 587 {
		t.Fatalf("smtp defaults: %s %d", c.SMTP.TLS, c.SMTP.Port)
	}
}

func TestLoadRejectsUnsafeSettings(t *testing.T) {
	cases := map[string]map[string]string{
		"missing database":        {"DATABASE_URL": ""},
		"http on public host":     {"PUBLIC_BASE_URL": "http://blog.example.com"},
		"base url with path":      {"PUBLIC_BASE_URL": "https://blog.example.com/app"},
		"short secret":            {"APP_SECRET_KEY": "short"},
		"example secret in prod":  {"APP_SECRET_KEY": "local-dev-only-secret-change-me-0123456789"},
		"plain smtp in prod":      {"SMTP_TLS": "none"},
		"bad smtp tls":            {"SMTP_TLS": "ssl3"},
		"user without password":   {"SMTP_USERNAME": "mailer"},
		"bad from":                {"SMTP_FROM": "not an address"},
		"bad proxy cidr":          {"TRUSTED_PROXY_CIDRS": "10.0.0.0/33"},
		"session ttl too long":    {"SESSION_TTL": "9000h"},
		"value and file together": {"SMTP_PASSWORD": "x", "SMTP_PASSWORD_FILE": "/run/secrets/smtp"},
	}
	for name, overrides := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := LoadFrom(env(overrides)); err == nil {
				t.Fatal("expected a validation error")
			}
		})
	}
}

func TestLoopbackDevelopmentSite(t *testing.T) {
	c, err := LoadFrom(env(map[string]string{
		"PUBLIC_BASE_URL": "http://127.0.0.1:3000",
		"APP_SECRET_KEY":  "local-dev-only-secret-change-me-0123456789",
		"SMTP_TLS":        "none",
		"SMTP_PORT":       "1025",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if c.SecureCookies {
		t.Fatal("http loopback site must not use Secure cookies")
	}
}

func TestSecretFileAndStringRedaction(t *testing.T) {
	path := filepath.Join(t.TempDir(), "smtp_password")
	if err := os.WriteFile(path, []byte("s3cr3t-from-file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := LoadFrom(env(map[string]string{"SMTP_USERNAME": "mailer", "SMTP_PASSWORD_FILE": path}))
	if err != nil {
		t.Fatal(err)
	}
	if c.SMTP.Password != "s3cr3t-from-file" {
		t.Fatalf("password from file = %q", c.SMTP.Password)
	}
	s := c.String()
	for _, secret := range []string{"s3cr3t-from-file", strings.Repeat("k", 40), "postgres://"} {
		if strings.Contains(s, secret) {
			t.Fatalf("String() leaks a secret: %s", s)
		}
	}
}
