package auth

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/http"
	"net/mail"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/xiaowork-dev/xiaowork-blog/apps/api/internal/platform/httpx"
	"github.com/xiaowork-dev/xiaowork-blog/apps/api/internal/store"
)

// ---- One-time tokens and session tokens ----

// newToken returns a 256-bit random token for the user (cookie / e-mail link) and its SHA-256 digest,
// which is the only thing stored (DD §4.3/4.4).
func newToken() (raw string, digest []byte) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err) // crypto/rand failing is unrecoverable
	}
	raw = base64.RawURLEncoding.EncodeToString(b)
	return raw, tokenDigest(raw)
}

func tokenDigest(raw string) []byte {
	sum := sha256.Sum256([]byte(raw))
	return sum[:]
}

var tokenFormat = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)

// validTokenFormat rejects garbage before it reaches the database.
func validTokenFormat(raw string) bool { return tokenFormat.MatchString(raw) }

// ---- Input validation (API-DD §5.6 limits) ----

const (
	minPasswordLen = 12
	maxPasswordLen = 128
	maxNicknameLen = 40
	maxEmailLen    = 254
)

var domainLabel = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

// normalizeEmail validates an address and returns the stored display form and the normalized key.
// Policy (ADR-0002): ASCII addresses only; uniqueness is case-insensitive over the whole address
// (product rule, DD §4.1); no provider-specific dot/plus folding.
func normalizeEmail(input string) (display, normalized string, ok bool) {
	s := strings.TrimSpace(input)
	if len(s) < 3 || len(s) > maxEmailLen {
		return "", "", false
	}
	for _, r := range s {
		if r > unicode.MaxASCII || r <= ' ' || r == 0x7f {
			return "", "", false
		}
	}
	addr, err := mail.ParseAddress(s)
	if err != nil || addr.Name != "" || addr.Address != s {
		return "", "", false
	}
	at := strings.LastIndexByte(s, '@')
	local, domain := s[:at], strings.ToLower(s[at+1:])
	if local == "" || len(local) > 64 || !strings.Contains(domain, ".") {
		return "", "", false
	}
	for _, label := range strings.Split(domain, ".") {
		if !domainLabel.MatchString(label) {
			return "", "", false
		}
	}
	return s, strings.ToLower(s), true
}

func validNickname(s string) (string, bool) {
	s = strings.TrimSpace(s)
	n := utf8.RuneCountInString(s)
	if n < 1 || n > maxNicknameLen {
		return "", false
	}
	for _, r := range s {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return "", false
		}
	}
	return s, true
}

func passwordProblem(p string) string {
	n := utf8.RuneCountInString(p)
	switch {
	case n < minPasswordLen:
		return "TOO_SHORT"
	case n > maxPasswordLen:
		return "TOO_LONG"
	case strings.ContainsRune(p, 0):
		return "INVALID"
	}
	return ""
}

// ---- CSRF (API-DD §3.4 item 3: signed double submit bound to the session/anonymous context) ----

// csrfToken derives the token for a security context: "s:<session digest>" once logged in,
// "a:<anonymous cookie>" before. Logging in or out therefore rotates the token automatically.
func csrfToken(key []byte, context string) string {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte("xiaowork-csrf-v1\x00" + context))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func isUnsafeMethod(m string) bool {
	return m != http.MethodGet && m != http.MethodHead && m != http.MethodOptions
}

// sameOrigin implements the Origin check: Origin must equal the public origin; if the browser sent no
// Origin, Fetch Metadata must say same-origin. Requests carrying neither are refused.
func sameOrigin(r *http.Request, origin string) bool {
	if o := r.Header.Get("Origin"); o != "" {
		return o == origin
	}
	return r.Header.Get("Sec-Fetch-Site") == "same-origin"
}

// ---- Rate limiting backed by auth_rate_windows (survives restarts, shared by instances) ----

type rateRule struct {
	Action string
	Limit  int
	Window time.Duration
}

// Defaults from API-DD §7 (to be tuned with real traffic).
var (
	ruleLoginIP        = rateRule{"LOGIN_IP", 10, 10 * time.Minute}
	ruleLoginFailEmail = rateRule{"LOGIN_FAIL_EMAIL", 5, 10 * time.Minute}
	ruleRegisterIP     = rateRule{"REGISTER_IP", 3, time.Hour}
	ruleMailIP         = rateRule{"MAIL_IP", 10, time.Hour}
	ruleVerifyMailMin  = rateRule{"VERIFY_MAIL_MINUTE", 1, time.Minute}
	ruleVerifyMailDay  = rateRule{"VERIFY_MAIL_DAY", 5, 24 * time.Hour}
	ruleResetMailMin   = rateRule{"RESET_MAIL_MINUTE", 1, time.Minute}
	ruleResetMailDay   = rateRule{"RESET_MAIL_DAY", 5, 24 * time.Hour}
	ruleEmailChangeMin = rateRule{"EMAIL_CHANGE_MINUTE", 1, time.Minute}
	ruleEmailChangeDay = rateRule{"EMAIL_CHANGE_DAY", 5, 24 * time.Hour}
	ruleTokenIP        = rateRule{"TOKEN_IP", 30, 10 * time.Minute}
	rulePasswordChange = rateRule{"PASSWORD_CHANGE", 5, 10 * time.Minute}
)

type rateLimiter struct {
	q   *store.Queries
	key []byte
	now func() time.Time
}

// subject digests never store raw IPs or addresses (DD §4.5).
func (l *rateLimiter) subject(rule rateRule, subject string) []byte {
	mac := hmac.New(sha256.New, l.key)
	mac.Write([]byte("xiaowork-rate-v1\x00" + rule.Action + "\x00" + subject))
	return mac.Sum(nil)
}

func (l *rateLimiter) window(rule rateRule) (start time.Time, retry time.Duration) {
	now := l.now().UTC()
	start = now.Truncate(rule.Window)
	return start, start.Add(rule.Window).Sub(now)
}

// hit counts one event and returns RATE_LIMITED once the limit is exceeded.
func (l *rateLimiter) hit(ctx context.Context, rule rateRule, subject string) error {
	start, retry := l.window(rule)
	n, err := l.q.HitRateWindow(ctx, store.HitRateWindowParams{
		ActionKey: rule.Action, SubjectDigest: l.subject(rule, subject),
		WindowStartedAt: start, ExpiresAt: start.Add(rule.Window),
	})
	if err != nil {
		return err
	}
	if int(n) > rule.Limit {
		return httpx.RateLimited(retry)
	}
	return nil
}

// blocked checks without counting (used for "failed attempts" counters).
func (l *rateLimiter) blocked(ctx context.Context, rule rateRule, subject string) error {
	start, retry := l.window(rule)
	n, err := l.q.PeekRateWindow(ctx, store.PeekRateWindowParams{
		ActionKey: rule.Action, SubjectDigest: l.subject(rule, subject), WindowStartedAt: start,
	})
	if err != nil {
		return err
	}
	if int(n) >= rule.Limit {
		return httpx.RateLimited(retry)
	}
	return nil
}

// ---- helpers ----

func isUniqueViolation(err error, constraint string) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == constraint
}
