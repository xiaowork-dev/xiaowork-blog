package auth

import (
	"context"
	"encoding/hex"
	"errors"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/xiaowork-dev/xiaowork-blog/apps/api/internal/config"
	"github.com/xiaowork-dev/xiaowork-blog/apps/api/internal/mail"
	"github.com/xiaowork-dev/xiaowork-blog/apps/api/internal/platform/httpx"
	"github.com/xiaowork-dev/xiaowork-blog/apps/api/internal/store"
)

// Service owns the authentication API and middlewares.
type Service struct {
	pool    *pgxpool.Pool
	q       *store.Queries
	cfg     *config.Config
	mailer  mail.Mailer
	hasher  *PasswordHasher
	limiter *rateLimiter
	logger  *slog.Logger
	now     func() time.Time
	bg      sync.WaitGroup
}

func NewService(pool *pgxpool.Pool, cfg *config.Config, mailer mail.Mailer, hasher *PasswordHasher, logger *slog.Logger) *Service {
	q := store.New(pool)
	return &Service{
		pool: pool, q: q, cfg: cfg, mailer: mailer, hasher: hasher, logger: logger, now: time.Now,
		limiter: &rateLimiter{q: q, key: cfg.SecretKey, now: time.Now},
	}
}

// Wait blocks until background e-mail deliveries have finished (shutdown and tests).
func (s *Service) Wait() { s.bg.Wait() }

// RateLimit counts one event for subject under action and returns RATE_LIMITED once more than limit
// events happen in the fixed window. Other modules (e.g. media uploads) share the persistent limiter.
func (s *Service) RateLimit(ctx context.Context, action string, limit int, window time.Duration, subject string) error {
	return s.limiter.hit(ctx, rateRule{Action: action, Limit: limit, Window: window}, subject)
}

// VisitorKey identifies the browser context of a request (session or anonymous CSRF cookie) for
// approximate de-duplication such as article views. It is only ever used as a hashed limiter subject.
func (s *Service) VisitorKey(r *http.Request) (string, bool) { return s.csrfContext(r) }

// ClientIP is the caller address after the trusted-proxy rules (TRUSTED_PROXY_CIDRS).
func (s *Service) ClientIP(r *http.Request) string { return s.clientIP(r) }

// ---- cookies ----

func (s *Service) sessionCookieName() string {
	if s.cfg.SecureCookies {
		return "__Host-blog_session" // API-DD §3.4: __Host- prefix, Secure, no Domain
	}
	return "blog_session" // http://localhost development only
}

func (s *Service) csrfCookieName() string {
	if s.cfg.SecureCookies {
		return "__Host-blog_csrf"
	}
	return "blog_csrf"
}

func (s *Service) setCookie(w http.ResponseWriter, name, value string, maxAge time.Duration) {
	c := &http.Cookie{
		Name: name, Value: value, Path: "/", HttpOnly: true, Secure: s.cfg.SecureCookies, SameSite: http.SameSiteLaxMode,
	}
	if maxAge > 0 {
		c.MaxAge = int(maxAge / time.Second)
	} else if maxAge < 0 {
		c.MaxAge = -1
	}
	http.SetCookie(w, c)
}

func cookieValue(r *http.Request, name string) string {
	if c, err := r.Cookie(name); err == nil {
		return c.Value
	}
	return ""
}

// ---- request context ----

// CurrentUser is the authenticated account attached to a request.
type CurrentUser struct {
	SessionID     uuid.UUID
	ID            uuid.UUID
	Email         string
	Nickname      string
	Role          string
	Status        string
	EmailVerified bool
	AvatarMediaID uuid.NullUUID
	CreatedAt     time.Time
}

type authState struct {
	user          *CurrentUser
	staleSession  bool  // a session cookie was sent but is expired/revoked/unknown
	err           error // dependency failure while loading the session
	sessionDigest []byte
}

type stateKey struct{}

func state(r *http.Request) *authState {
	if st, ok := r.Context().Value(stateKey{}).(*authState); ok {
		return st
	}
	return &authState{}
}

// User returns the authenticated user, if any.
func User(r *http.Request) *CurrentUser { return state(r).user }

// Authenticate loads the session (if a cookie is present). Every request re-checks expiry,
// revocation and the live account status in the database (DD §4.3).
func (s *Service) Authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		st := &authState{}
		if raw := cookieValue(r, s.sessionCookieName()); raw != "" {
			st.sessionDigest = tokenDigest(raw)
			row, err := s.q.GetSessionWithUser(r.Context(), st.sessionDigest)
			switch {
			case errors.Is(err, pgx.ErrNoRows):
				st.staleSession = true
			case err != nil:
				st.err = err
			case !row.IsValid:
				st.staleSession = true
			default:
				st.user = &CurrentUser{
					SessionID: row.SessionID, ID: row.UserID, Email: row.Email, Nickname: row.Nickname,
					Role: row.Role, Status: row.Status, EmailVerified: row.EmailVerifiedAt.Valid,
					AvatarMediaID: row.AvatarMediaID, CreatedAt: row.UserCreatedAt,
				}
				if err := s.q.TouchSession(r.Context(), row.SessionID); err != nil {
					httpx.LoggerFrom(r.Context()).Warn("session touch failed", "error", err.Error())
				}
			}
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), stateKey{}, st)))
	})
}

// CSRF enforces the Origin check and the X-CSRF-Token header on every state-changing request.
func (s *Service) CSRF(next http.Handler) http.Handler {
	origin := s.cfg.Origin()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isUnsafeMethod(r.Method) {
			ctxID, ok := s.csrfContext(r)
			if !ok || !sameOrigin(r, origin) || !httpEqual(r.Header.Get("X-CSRF-Token"), csrfToken(s.cfg.SecretKey, ctxID)) {
				httpx.WriteError(w, r, httpx.ErrCSRFInvalid)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// csrfContext is "s:<session digest>" when a session cookie is present, else "a:<anonymous csrf cookie>".
func (s *Service) csrfContext(r *http.Request) (string, bool) {
	if raw := cookieValue(r, s.sessionCookieName()); raw != "" {
		return "s:" + hex.EncodeToString(tokenDigest(raw)), true
	}
	if anon := cookieValue(r, s.csrfCookieName()); validTokenFormat(anon) {
		return "a:" + anon, true
	}
	return "", false
}

// require returns the middleware for one permission level of API-DD §3.5.
func (s *Service) require(verified, admin bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			st := state(r)
			switch {
			case st.err != nil:
				httpx.WriteError(w, r, st.err)
			case st.user == nil && st.staleSession:
				s.setCookie(w, s.sessionCookieName(), "", -1)
				httpx.WriteError(w, r, httpx.ErrSessionExpired)
			case st.user == nil:
				httpx.WriteError(w, r, httpx.ErrAuthRequired)
			case st.user.Status != "ACTIVE":
				httpx.WriteError(w, r, httpx.ErrAccountDisabled)
			case verified && !st.user.EmailVerified:
				httpx.WriteError(w, r, httpx.ErrEmailNotVerified)
			case admin && st.user.Role != "ADMIN":
				httpx.WriteError(w, r, httpx.ErrForbidden)
			default:
				next.ServeHTTP(w, r)
			}
		})
	}
}

// RequireUser is permission S (a valid session of an active account).
func (s *Service) RequireUser(next http.Handler) http.Handler { return s.require(false, false)(next) }

// RequireVerified is permission U (also requires a verified e-mail), e.g. for commenting (P6).
func (s *Service) RequireVerified(next http.Handler) http.Handler {
	return s.require(true, false)(next)
}

// RequireAdmin is permission A; enforced in Go, never only in Nuxt (API-DD §7).
func (s *Service) RequireAdmin(next http.Handler) http.Handler { return s.require(true, true)(next) }

// ---- background mail ----

// sendAsync delivers mail after the response so that "account exists" and "no such account" take the
// same time (anti-enumeration for resend/forgot). Failures are logged as operational events.
func (s *Service) sendAsync(kind string, msg mail.Message, logger *slog.Logger) {
	s.bg.Add(1)
	go func() {
		defer s.bg.Done()
		ctx, cancel := context.WithTimeout(context.Background(), 2*s.cfg.SMTP.Timeout)
		defer cancel()
		if err := s.mailer.Send(ctx, msg); err != nil {
			logger.Error("mail delivery failed", "kind", kind, "error", err.Error())
			return
		}
		logger.Info("mail delivered", "kind", kind)
	}()
}

// sendNow delivers synchronously and reports whether the SMTP server accepted the message.
func (s *Service) sendNow(ctx context.Context, kind string, msg mail.Message, logger *slog.Logger) bool {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*s.cfg.SMTP.Timeout)
	defer cancel()
	if err := s.mailer.Send(ctx, msg); err != nil {
		logger.Error("mail delivery failed", "kind", kind, "error", err.Error())
		return false
	}
	logger.Info("mail delivered", "kind", kind)
	return true
}

// ---- maintenance ----

// RunJanitor periodically removes expired rate-limit windows, stale tokens and sessions (DD §11.2).
// Expiry is always also checked at use time; this only bounds table growth.
func (s *Service) RunJanitor(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		s.cleanup(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (s *Service) cleanup(ctx context.Context) {
	windows, err1 := s.q.DeleteExpiredRateWindows(ctx)
	tokens, err2 := s.q.DeleteStaleAuthTokens(ctx)
	sessions, err3 := s.q.DeleteStaleSessions(ctx)
	if err := errors.Join(err1, err2, err3); err != nil {
		s.logger.Warn("auth cleanup failed", "error", err.Error())
		return
	}
	s.logger.Info("auth cleanup", "rate_windows", windows, "tokens", tokens, "sessions", sessions)
}
