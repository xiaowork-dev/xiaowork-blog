// Package config loads and validates the API runtime configuration from the environment.
//
// Secrets (database URL, SMTP password, application secret key) may be given directly or through
// a *_FILE variable pointing at a mounted secret file. Secret values are never part of String()/logs.
package config

import (
	"errors"
	"fmt"
	"net"
	"net/mail"
	"net/netip"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// SMTPTLS selects how the SMTP connection is secured.
type SMTPTLS string

const (
	SMTPTLSNone     SMTPTLS = "none"     // plain text; only allowed for a loopback development site
	SMTPTLSStartTLS SMTPTLS = "starttls" // upgrade with STARTTLS (typically port 587)
	SMTPTLSImplicit SMTPTLS = "tls"      // implicit TLS (typically port 465)
)

// Values used in .env.example; rejected for any non-loopback deployment.
var knownExampleSecrets = []string{
	"local-dev-only-secret-change-me-0123456789",
}

type SMTP struct {
	Host     string
	Port     int
	Username string
	Password string
	From     *mail.Address
	TLS      SMTPTLS
	Timeout  time.Duration
}

type Config struct {
	HTTPAddr    string
	DatabaseURL string
	MediaRoot   string

	// PublicBaseURL is the browser-facing origin (scheme://host[:port]); used for e-mail links and Origin checks.
	PublicBaseURL *url.URL
	// SecureCookies is true for https sites; cookies then use the __Host- prefix and the Secure attribute.
	SecureCookies bool
	// SecretKey keys the CSRF HMAC and the rate-limit subject digests. At least 32 bytes.
	SecretKey []byte

	SessionTTL     time.Duration
	VerifyTokenTTL time.Duration
	ResetTokenTTL  time.Duration
	EmailTokenTTL  time.Duration

	// TrustedProxies are the networks whose X-Forwarded-For entries are believed (Nuxt / Nginx Proxy Manager).
	TrustedProxies []netip.Prefix

	SMTP SMTP
}

// Origin returns scheme://host[:port] of the public site.
func (c *Config) Origin() string {
	return c.PublicBaseURL.Scheme + "://" + c.PublicBaseURL.Host
}

// String is safe to log: it omits every secret.
func (c *Config) String() string {
	return fmt.Sprintf("addr=%s public=%s secure_cookies=%t smtp=%s:%d tls=%s trusted_proxies=%d",
		c.HTTPAddr, c.Origin(), c.SecureCookies, c.SMTP.Host, c.SMTP.Port, c.SMTP.TLS, len(c.TrustedProxies))
}

// Load reads the configuration from the process environment.
func Load() (*Config, error) { return LoadFrom(os.Getenv) }

// LoadFrom reads the configuration through getenv (tests pass a map lookup) and validates it.
// All problems are reported together.
func LoadFrom(getenv func(string) string) (*Config, error) {
	var errs []error
	fail := func(format string, args ...any) { errs = append(errs, fmt.Errorf(format, args...)) }

	secret := func(name string) string {
		value, file := getenv(name), getenv(name+"_FILE")
		if value != "" && file != "" {
			fail("%s and %s_FILE are mutually exclusive", name, name)
			return ""
		}
		if file != "" {
			data, err := os.ReadFile(file)
			if err != nil {
				fail("%s_FILE: cannot read secret file", name) // path and content are not echoed
				return ""
			}
			return strings.TrimRight(string(data), "\r\n")
		}
		return value
	}
	duration := func(name string, def time.Duration, min, max time.Duration) time.Duration {
		raw := getenv(name)
		if raw == "" {
			return def
		}
		d, err := time.ParseDuration(raw)
		if err != nil || d < min || d > max {
			fail("%s must be a duration between %s and %s", name, min, max)
			return def
		}
		return d
	}

	c := &Config{
		HTTPAddr:  orDefault(getenv("HTTP_ADDR"), ":8080"),
		MediaRoot: orDefault(getenv("MEDIA_ROOT"), "/data/media"),
	}

	c.DatabaseURL = secret("DATABASE_URL")
	if c.DatabaseURL == "" {
		fail("DATABASE_URL is required")
	}

	loopbackSite := false
	if raw := getenv("PUBLIC_BASE_URL"); raw == "" {
		fail("PUBLIC_BASE_URL is required (e.g. https://blog.example.com)")
	} else if u, err := url.Parse(raw); err != nil || u.Host == "" || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.User != nil {
		fail("PUBLIC_BASE_URL must be an origin like https://blog.example.com (no path, query or credentials)")
	} else {
		u.Path = ""
		c.PublicBaseURL = u
		loopbackSite = isLoopbackHost(u.Hostname())
		switch {
		case u.Scheme == "https":
			c.SecureCookies = true
		case u.Scheme == "http" && loopbackSite:
			c.SecureCookies = false // local development only
		default:
			fail("PUBLIC_BASE_URL must use https (plain http is only allowed for localhost/127.0.0.1)")
		}
	}

	key := secret("APP_SECRET_KEY")
	switch {
	case len(key) < 32:
		fail("APP_SECRET_KEY must be at least 32 bytes of random data")
	case !loopbackSite && contains(knownExampleSecrets, key):
		fail("APP_SECRET_KEY still has the .env.example value; generate a random one")
	default:
		c.SecretKey = []byte(key)
	}

	c.SessionTTL = duration("SESSION_TTL", 14*24*time.Hour, time.Hour, 90*24*time.Hour)
	c.VerifyTokenTTL = duration("VERIFY_EMAIL_TOKEN_TTL", 24*time.Hour, 10*time.Minute, 7*24*time.Hour)
	c.ResetTokenTTL = duration("RESET_PASSWORD_TOKEN_TTL", 30*time.Minute, 5*time.Minute, 24*time.Hour)
	c.EmailTokenTTL = duration("CHANGE_EMAIL_TOKEN_TTL", 24*time.Hour, 10*time.Minute, 7*24*time.Hour)

	for _, part := range strings.Split(getenv("TRUSTED_PROXY_CIDRS"), ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		p, err := netip.ParsePrefix(part)
		if err != nil {
			fail("TRUSTED_PROXY_CIDRS: invalid CIDR %q", part)
			continue
		}
		c.TrustedProxies = append(c.TrustedProxies, p.Masked())
	}

	c.SMTP.Host = getenv("SMTP_HOST")
	if c.SMTP.Host == "" {
		fail("SMTP_HOST is required")
	}
	port, err := strconv.Atoi(orDefault(getenv("SMTP_PORT"), "587"))
	if err != nil || port < 1 || port > 65535 {
		fail("SMTP_PORT must be a TCP port")
	}
	c.SMTP.Port = port
	c.SMTP.Username = getenv("SMTP_USERNAME")
	c.SMTP.Password = secret("SMTP_PASSWORD")
	if (c.SMTP.Username == "") != (c.SMTP.Password == "") {
		fail("SMTP_USERNAME and SMTP_PASSWORD must be set together")
	}
	if from, err := mail.ParseAddress(getenv("SMTP_FROM")); err != nil {
		fail("SMTP_FROM must be an address like \"XiaoWork Blog <no-reply@example.com>\"")
	} else {
		c.SMTP.From = from
	}
	c.SMTP.TLS = SMTPTLS(orDefault(getenv("SMTP_TLS"), string(SMTPTLSStartTLS)))
	switch c.SMTP.TLS {
	case SMTPTLSStartTLS, SMTPTLSImplicit:
	case SMTPTLSNone:
		if !loopbackSite {
			fail("SMTP_TLS=none is only allowed for a localhost development site")
		}
	default:
		fail("SMTP_TLS must be none, starttls or tls")
	}
	c.SMTP.Timeout = duration("SMTP_TIMEOUT", 10*time.Second, time.Second, time.Minute)

	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	return c, nil
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

func contains(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
