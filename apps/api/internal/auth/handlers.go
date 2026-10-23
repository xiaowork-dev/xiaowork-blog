package auth

import (
	"context"
	"crypto/subtle"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/xiaowork-dev/xiaowork-blog/apps/api/internal/mail"
	"github.com/xiaowork-dev/xiaowork-blog/apps/api/internal/platform/httpx"
	"github.com/xiaowork-dev/xiaowork-blog/apps/api/internal/store"
)

const (
	purposeVerify = "VERIFY_EMAIL"
	purposeReset  = "RESET_PASSWORD"
	purposeChange = "CHANGE_EMAIL"
)

var errTokenInvalid = httpx.NewError(http.StatusUnprocessableEntity, "TOKEN_INVALID_OR_EXPIRED", "链接无效或已过期，请重新申请。")
var errEmailUsed = httpx.NewError(http.StatusConflict, "EMAIL_ALREADY_USED", "该邮箱已被注册。")
var errInvalidCredentials = httpx.NewError(http.StatusUnauthorized, "INVALID_CREDENTIALS", "邮箱或密码不正确。")
var errMailUnavailable = httpx.NewError(http.StatusServiceUnavailable, "MAIL_UNAVAILABLE", "邮件暂时无法发送，请稍后重试。")

func httpEqual(got, want string) bool {
	return got != "" && subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}

// Routes mounts the P2 endpoints of API-DD §4.2 (permissions per §3.5).
func (s *Service) Routes(r chi.Router) {
	r.Get("/auth/csrf", s.handleCSRF)
	r.Post("/auth/register", s.handle(s.register))
	r.Post("/auth/resend-verification", s.handle(s.resendVerification))
	r.Post("/auth/verify-email", s.handle(s.verifyEmail))
	r.Post("/auth/login", s.handle(s.login))
	r.Post("/auth/forgot-password", s.handle(s.forgotPassword))
	r.Post("/auth/reset-password", s.handle(s.resetPassword))
	r.Group(func(r chi.Router) {
		r.Use(s.RequireUser)
		r.Post("/auth/logout", s.handle(s.logout))
		r.Post("/auth/logout-all", s.handle(s.logoutAll))
		r.Get("/auth/me", s.handle(s.me))
		r.Post("/me/change-password", s.handle(s.changePassword))
		r.Post("/me/change-email/request", s.handle(s.requestEmailChange))
		r.Post("/me/change-email/confirm", s.handle(s.confirmEmailChange))
	})
}

// handle adapts func(w, r) error handlers to the error envelope.
func (s *Service) handle(fn func(http.ResponseWriter, *http.Request) error) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := fn(w, r); err != nil {
			httpx.WriteError(w, r, err)
		}
	}
}

func (s *Service) clientIP(r *http.Request) string {
	return httpx.ClientIP(r, s.cfg.TrustedProxies).String()
}

func (s *Service) link(path, token string) string {
	return s.cfg.Origin() + path + "?token=" + token
}

// GET /auth/csrf — issues the token for the caller's current context, creating the anonymous cookie if needed.
func (s *Service) handleCSRF(w http.ResponseWriter, r *http.Request) {
	ctxID, ok := s.csrfContext(r)
	if !ok {
		anon, _ := newToken()
		s.setCookie(w, s.csrfCookieName(), anon, 0)
		ctxID = "a:" + anon
	}
	httpx.WriteData(w, http.StatusOK, map[string]any{"csrf_token": csrfToken(s.cfg.SecretKey, ctxID)})
}

// ---- registration and e-mail verification (DD T01) ----

func (s *Service) register(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Email    string `json:"email"`
		Nickname string `json:"nickname"`
		Password string `json:"password"`
	}
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	fields := map[string][]string{}
	email, normalized, ok := normalizeEmail(in.Email)
	if !ok {
		fields["email"] = []string{"INVALID"}
	}
	nickname, ok := validNickname(in.Nickname)
	if !ok {
		fields["nickname"] = []string{"INVALID_LENGTH"}
	}
	if p := passwordProblem(in.Password); p != "" {
		fields["password"] = []string{p}
	}
	if len(fields) > 0 {
		return httpx.FieldErrors(fields)
	}
	ctx := r.Context()
	if err := s.limiter.hit(ctx, ruleRegisterIP, s.clientIP(r)); err != nil {
		return err
	}
	hash, err := s.hasher.Hash(ctx, in.Password)
	if err != nil {
		return err
	}
	raw, digest := newToken()
	userID := uuid.New()
	err = store.ExecTx(ctx, s.pool, func(q *store.Queries) error {
		// Public registration always creates role USER (DD §4.1, API-05).
		if _, err := q.CreateUser(ctx, store.CreateUserParams{
			ID: userID, Email: email, EmailNormalized: normalized, PasswordHash: hash, Nickname: nickname, Role: "USER",
		}); err != nil {
			return err
		}
		if err := q.CreateUserProfile(ctx, userID); err != nil {
			return err
		}
		return q.CreateAuthToken(ctx, store.CreateAuthTokenParams{
			ID: uuid.New(), UserID: userID, Purpose: purposeVerify, TokenDigest: digest,
			TargetEmail: pgtype.Text{String: email, Valid: true}, TargetEmailNormalized: pgtype.Text{String: normalized, Valid: true},
			ExpiresAt: s.now().Add(s.cfg.VerifyTokenTTL),
		})
	})
	if isUniqueViolation(err, "users_email_normalized_key") {
		return errEmailUsed
	}
	if err != nil {
		return err
	}
	logger := httpx.LoggerFrom(ctx)
	logger.Info("user registered", "user_id", userID)
	delivery := "accepted"
	if !s.sendNow(ctx, "verify_email", verifyEmailMessage(email, nickname, s.link("/auth/verify", raw), s.cfg.VerifyTokenTTL), logger) {
		// The account exists; the user can request a new e-mail later (API-DD §5.6).
		delivery = "unavailable"
	}
	httpx.WriteData(w, http.StatusCreated, map[string]any{
		"registration_status": "PENDING_EMAIL_VERIFICATION", "email_delivery": delivery,
	})
	return nil
}

// emailRequest is the body of resend-verification and forgot-password.
func decodeEmailRequest(w http.ResponseWriter, r *http.Request) (email, normalized string, err error) {
	var in struct {
		Email string `json:"email"`
	}
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return "", "", err
	}
	email, normalized, ok := normalizeEmail(in.Email)
	if !ok {
		return "", "", httpx.FieldErrors(map[string][]string{"email": {"INVALID"}})
	}
	return email, normalized, nil
}

// accepted is the uniform answer for mail requests, whether or not the account exists.
func accepted(w http.ResponseWriter) {
	httpx.WriteData(w, http.StatusAccepted, map[string]any{"accepted": true})
}

func (s *Service) resendVerification(w http.ResponseWriter, r *http.Request) error {
	_, normalized, err := decodeEmailRequest(w, r)
	if err != nil {
		return err
	}
	ctx := r.Context()
	for _, check := range []struct {
		rule    rateRule
		subject string
	}{{ruleMailIP, s.clientIP(r)}, {ruleVerifyMailMin, normalized}, {ruleVerifyMailDay, normalized}} {
		if err := s.limiter.hit(ctx, check.rule, check.subject); err != nil {
			return err
		}
	}
	user, err := s.q.GetUserByEmailNormalized(ctx, normalized)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && (user.Status != "ACTIVE" || user.EmailVerifiedAt.Valid) {
		accepted(w)
		return nil
	}
	if err != nil {
		return err
	}
	raw, digest := newToken()
	err = store.ExecTx(ctx, s.pool, func(q *store.Queries) error {
		// A new e-mail invalidates older verification links (API-DD §5.6).
		if _, err := q.RevokeUnusedTokens(ctx, store.RevokeUnusedTokensParams{UserID: user.ID, Purpose: purposeVerify}); err != nil {
			return err
		}
		return q.CreateAuthToken(ctx, store.CreateAuthTokenParams{
			ID: uuid.New(), UserID: user.ID, Purpose: purposeVerify, TokenDigest: digest,
			TargetEmail: pgtype.Text{String: user.Email, Valid: true}, TargetEmailNormalized: pgtype.Text{String: user.EmailNormalized, Valid: true},
			ExpiresAt: s.now().Add(s.cfg.VerifyTokenTTL),
		})
	})
	if err != nil {
		return err
	}
	s.sendAsync("verify_email", verifyEmailMessage(user.Email, user.Nickname, s.link("/auth/verify", raw), s.cfg.VerifyTokenTTL), httpx.LoggerFrom(ctx))
	accepted(w)
	return nil
}

func decodeToken(w http.ResponseWriter, r *http.Request, extra ...*string) (string, error) {
	var in struct {
		Token       string `json:"token"`
		NewPassword string `json:"new_password"`
	}
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return "", err
	}
	if len(extra) > 0 {
		*extra[0] = in.NewPassword
	}
	return in.Token, nil
}

func (s *Service) verifyEmail(w http.ResponseWriter, r *http.Request) error {
	raw, err := decodeToken(w, r)
	if err != nil {
		return err
	}
	ctx := r.Context()
	if err := s.limiter.hit(ctx, ruleTokenIP, s.clientIP(r)); err != nil {
		return err
	}
	if !validTokenFormat(raw) {
		return errTokenInvalid
	}
	err = store.ExecTx(ctx, s.pool, func(q *store.Queries) error {
		tok, err := q.ConsumeAuthToken(ctx, store.ConsumeAuthTokenParams{TokenDigest: tokenDigest(raw), Purpose: purposeVerify})
		if errors.Is(err, pgx.ErrNoRows) {
			return errTokenInvalid
		}
		if err != nil {
			return err
		}
		user, err := q.GetUserByID(ctx, tok.UserID)
		if err != nil {
			return err
		}
		// The link must still match the account's address and the account must be usable.
		if user.Status != "ACTIVE" || tok.TargetEmailNormalized.String != user.EmailNormalized {
			return errTokenInvalid
		}
		if _, err := q.MarkEmailVerified(ctx, user.ID); err != nil {
			return err
		}
		_, err = q.RevokeUnusedTokens(ctx, store.RevokeUnusedTokensParams{UserID: user.ID, Purpose: purposeVerify})
		return err
	})
	if err != nil {
		return err
	}
	httpx.WriteData(w, http.StatusOK, map[string]any{"email_verified": true})
	return nil
}

// ---- login / logout (DD T01 step 5, API-DD §5.6) ----

func (s *Service) login(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	ctx := r.Context()
	if err := s.limiter.hit(ctx, ruleLoginIP, s.clientIP(r)); err != nil {
		return err
	}
	_, normalized, ok := normalizeEmail(in.Email)
	if !ok || in.Password == "" || len(in.Password) > 4*maxPasswordLen {
		return errInvalidCredentials
	}
	if err := s.limiter.blocked(ctx, ruleLoginFailEmail, normalized); err != nil {
		return err
	}
	user, err := s.q.GetUserByEmailNormalized(ctx, normalized)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	match := false
	if err == nil {
		match, err = s.hasher.Verify(ctx, in.Password, user.PasswordHash)
		if err != nil {
			return err
		}
	} else {
		s.hasher.VerifyDummy(ctx, in.Password) // same cost whether or not the account exists
	}
	if !match {
		if err := s.limiter.hit(ctx, ruleLoginFailEmail, normalized); err != nil {
			return err
		}
		return errInvalidCredentials
	}
	if user.Status != "ACTIVE" {
		return httpx.ErrAccountDisabled
	}
	if !user.EmailVerifiedAt.Valid {
		return httpx.ErrEmailNotVerified
	}

	raw, digest := newToken()
	err = store.ExecTx(ctx, s.pool, func(q *store.Queries) error {
		// Rotate: a session cookie this browser already had is revoked.
		if st := state(r); st.sessionDigest != nil {
			if old, err := q.GetSessionWithUser(ctx, st.sessionDigest); err == nil {
				if _, err := q.RevokeSession(ctx, old.SessionID); err != nil {
					return err
				}
			}
		}
		return q.CreateSession(ctx, store.CreateSessionParams{
			ID: uuid.New(), UserID: user.ID, TokenDigest: digest, ExpiresAt: s.now().Add(s.cfg.SessionTTL),
		})
	})
	if err != nil {
		return err
	}
	s.setCookie(w, s.sessionCookieName(), raw, s.cfg.SessionTTL)
	httpx.LoggerFrom(ctx).Info("user logged in", "user_id", user.ID)
	httpx.WriteData(w, http.StatusOK, map[string]any{"user": map[string]any{
		"id": user.ID, "nickname": user.Nickname, "role": user.Role, "email_verified": true,
	}})
	return nil
}

func (s *Service) logout(w http.ResponseWriter, r *http.Request) error {
	if _, err := s.q.RevokeSession(r.Context(), User(r).SessionID); err != nil {
		return err
	}
	s.setCookie(w, s.sessionCookieName(), "", -1)
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *Service) logoutAll(w http.ResponseWriter, r *http.Request) error {
	if _, err := s.q.RevokeUserSessions(r.Context(), User(r).ID); err != nil {
		return err
	}
	s.setCookie(w, s.sessionCookieName(), "", -1)
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *Service) me(w http.ResponseWriter, r *http.Request) error {
	u := User(r)
	var avatar any
	if u.AvatarMediaID.Valid {
		avatar = "/media/" + u.AvatarMediaID.UUID.String() + "/file"
	}
	httpx.WriteData(w, http.StatusOK, map[string]any{
		"id": u.ID, "email": u.Email, "nickname": u.Nickname, "avatar_url": avatar, "role": u.Role,
		"email_verified": u.EmailVerified, "status": u.Status, "created_at": u.CreatedAt.UTC(),
	})
	return nil
}

// ---- password reset (DD T02) ----

func (s *Service) forgotPassword(w http.ResponseWriter, r *http.Request) error {
	_, normalized, err := decodeEmailRequest(w, r)
	if err != nil {
		return err
	}
	ctx := r.Context()
	for _, check := range []struct {
		rule    rateRule
		subject string
	}{{ruleMailIP, s.clientIP(r)}, {ruleResetMailMin, normalized}, {ruleResetMailDay, normalized}} {
		if err := s.limiter.hit(ctx, check.rule, check.subject); err != nil {
			return err
		}
	}
	user, err := s.q.GetUserByEmailNormalized(ctx, normalized)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && user.Status != "ACTIVE" {
		accepted(w) // identical answer: no account enumeration (PRD §8.1)
		return nil
	}
	if err != nil {
		return err
	}
	raw, digest := newToken()
	err = store.ExecTx(ctx, s.pool, func(q *store.Queries) error {
		if _, err := q.RevokeUnusedTokens(ctx, store.RevokeUnusedTokensParams{UserID: user.ID, Purpose: purposeReset}); err != nil {
			return err
		}
		return q.CreateAuthToken(ctx, store.CreateAuthTokenParams{
			ID: uuid.New(), UserID: user.ID, Purpose: purposeReset, TokenDigest: digest,
			TargetEmail: pgtype.Text{String: user.Email, Valid: true}, TargetEmailNormalized: pgtype.Text{String: user.EmailNormalized, Valid: true},
			ExpiresAt: s.now().Add(s.cfg.ResetTokenTTL),
		})
	})
	if err != nil {
		return err
	}
	s.sendAsync("reset_password", resetPasswordMessage(user.Email, user.Nickname, s.link("/auth/reset-password", raw), s.cfg.ResetTokenTTL), httpx.LoggerFrom(ctx))
	accepted(w)
	return nil
}

func (s *Service) resetPassword(w http.ResponseWriter, r *http.Request) error {
	var newPassword string
	raw, err := decodeToken(w, r, &newPassword)
	if err != nil {
		return err
	}
	ctx := r.Context()
	if err := s.limiter.hit(ctx, ruleTokenIP, s.clientIP(r)); err != nil {
		return err
	}
	if p := passwordProblem(newPassword); p != "" {
		return httpx.FieldErrors(map[string][]string{"new_password": {p}})
	}
	if !validTokenFormat(raw) {
		return errTokenInvalid
	}
	hash, err := s.hasher.Hash(ctx, newPassword)
	if err != nil {
		return err
	}
	var userID uuid.UUID
	err = store.ExecTx(ctx, s.pool, func(q *store.Queries) error {
		tok, err := q.ConsumeAuthToken(ctx, store.ConsumeAuthTokenParams{TokenDigest: tokenDigest(raw), Purpose: purposeReset})
		if errors.Is(err, pgx.ErrNoRows) {
			return errTokenInvalid
		}
		if err != nil {
			return err
		}
		user, err := q.GetUserByID(ctx, tok.UserID)
		if err != nil {
			return err
		}
		if user.Status != "ACTIVE" || tok.TargetEmailNormalized.String != user.EmailNormalized {
			return errTokenInvalid
		}
		userID = user.ID
		// Password, one-time token and every session change together (DB-04, API-08).
		if err := q.UpdatePasswordHash(ctx, store.UpdatePasswordHashParams{ID: user.ID, PasswordHash: hash}); err != nil {
			return err
		}
		if _, err := q.RevokeUserSessions(ctx, user.ID); err != nil {
			return err
		}
		_, err = q.RevokeUnusedTokens(ctx, store.RevokeUnusedTokensParams{UserID: user.ID, Purpose: purposeReset})
		return err
	})
	if err != nil {
		return err
	}
	httpx.LoggerFrom(ctx).Info("password reset", "user_id", userID)
	httpx.WriteData(w, http.StatusOK, map[string]any{"password_reset": true})
	return nil
}

// ---- signed-in account security ----

func (s *Service) changePassword(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		CurrentPassword string `json:"current_password"`
		NewPassword     string `json:"new_password"`
	}
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	ctx, u := r.Context(), User(r)
	if err := s.limiter.hit(ctx, rulePasswordChange, u.ID.String()); err != nil {
		return err
	}
	if p := passwordProblem(in.NewPassword); p != "" {
		return httpx.FieldErrors(map[string][]string{"new_password": {p}})
	}
	user, err := s.q.GetUserByID(ctx, u.ID)
	if err != nil {
		return err
	}
	ok, err := s.hasher.Verify(ctx, in.CurrentPassword, user.PasswordHash)
	if err != nil {
		return err
	}
	if !ok {
		return httpx.FieldErrors(map[string][]string{"current_password": {"INCORRECT"}})
	}
	if in.NewPassword == in.CurrentPassword {
		return httpx.FieldErrors(map[string][]string{"new_password": {"SAME_AS_CURRENT"}})
	}
	hash, err := s.hasher.Hash(ctx, in.NewPassword)
	if err != nil {
		return err
	}
	// API-DD §5.6 recommends re-login: every session (this one included) is revoked.
	err = store.ExecTx(ctx, s.pool, func(q *store.Queries) error {
		if err := q.UpdatePasswordHash(ctx, store.UpdatePasswordHashParams{ID: u.ID, PasswordHash: hash}); err != nil {
			return err
		}
		if _, err := q.RevokeUserSessions(ctx, u.ID); err != nil {
			return err
		}
		_, err := q.RevokeUnusedTokens(ctx, store.RevokeUnusedTokensParams{UserID: u.ID, Purpose: purposeReset})
		return err
	})
	if err != nil {
		return err
	}
	s.setCookie(w, s.sessionCookieName(), "", -1)
	httpx.LoggerFrom(ctx).Info("password changed", "user_id", u.ID)
	httpx.WriteData(w, http.StatusOK, map[string]any{"reauthentication_required": true})
	return nil
}

func (s *Service) requestEmailChange(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		NewEmail string `json:"new_email"`
	}
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	ctx, u := r.Context(), User(r)
	if !u.EmailVerified {
		return httpx.ErrEmailNotVerified
	}
	email, normalized, ok := normalizeEmail(in.NewEmail)
	if !ok {
		return httpx.FieldErrors(map[string][]string{"new_email": {"INVALID"}})
	}
	if normalized == strings.ToLower(u.Email) {
		return httpx.FieldErrors(map[string][]string{"new_email": {"SAME_AS_CURRENT"}})
	}
	if _, err := s.q.GetUserByEmailNormalized(ctx, normalized); err == nil {
		return errEmailUsed
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	// Only requests that will send a mail are counted, so a typo'd/taken address does not lock the user out.
	for _, rule := range []rateRule{ruleEmailChangeMin, ruleEmailChangeDay} {
		if err := s.limiter.hit(ctx, rule, u.ID.String()); err != nil {
			return err
		}
	}
	raw, digest := newToken()
	tokenID := uuid.New()
	err := store.ExecTx(ctx, s.pool, func(q *store.Queries) error {
		if _, err := q.RevokeUnusedTokens(ctx, store.RevokeUnusedTokensParams{UserID: u.ID, Purpose: purposeChange}); err != nil {
			return err
		}
		return q.CreateAuthToken(ctx, store.CreateAuthTokenParams{
			ID: tokenID, UserID: u.ID, Purpose: purposeChange, TokenDigest: digest,
			TargetEmail: pgtype.Text{String: email, Valid: true}, TargetEmailNormalized: pgtype.Text{String: normalized, Valid: true},
			ExpiresAt: s.now().Add(s.cfg.EmailTokenTTL),
		})
	})
	if err != nil {
		return err
	}
	logger := httpx.LoggerFrom(ctx)
	if !s.sendNow(ctx, "change_email", changeEmailMessage(email, u.Nickname, s.link("/auth/confirm-email", raw), s.cfg.EmailTokenTTL), logger) {
		// Do not leave a live token behind for a mail that was never sent; report honestly (UI-UX §8).
		if _, err := s.q.RevokeUnusedTokens(context.WithoutCancel(ctx), store.RevokeUnusedTokensParams{UserID: u.ID, Purpose: purposeChange}); err != nil {
			logger.Error("revoking unsent change-email token failed", "error", err.Error())
		}
		return errMailUnavailable
	}
	accepted(w)
	return nil
}

// confirmEmailChange needs the signed-in session of the same account, so a leaked link alone cannot
// take over an account (API-DD §5.6).
func (s *Service) confirmEmailChange(w http.ResponseWriter, r *http.Request) error {
	raw, err := decodeToken(w, r)
	if err != nil {
		return err
	}
	ctx, u := r.Context(), User(r)
	if err := s.limiter.hit(ctx, ruleTokenIP, s.clientIP(r)); err != nil {
		return err
	}
	if !validTokenFormat(raw) {
		return errTokenInvalid
	}
	var newEmail string
	err = store.ExecTx(ctx, s.pool, func(q *store.Queries) error {
		tok, err := q.ConsumeAuthToken(ctx, store.ConsumeAuthTokenParams{TokenDigest: tokenDigest(raw), Purpose: purposeChange})
		if errors.Is(err, pgx.ErrNoRows) || err == nil && tok.UserID != u.ID {
			return errTokenInvalid // a mismatching account rolls back, leaving the token unused
		}
		if err != nil {
			return err
		}
		newEmail = tok.TargetEmail.String
		err = q.UpdateUserEmail(ctx, store.UpdateUserEmailParams{ID: u.ID, Email: tok.TargetEmail.String, EmailNormalized: tok.TargetEmailNormalized.String})
		if isUniqueViolation(err, "users_email_normalized_key") {
			return errEmailUsed // taken meanwhile; the unique key decides (DD T02)
		}
		if err != nil {
			return err
		}
		// Old-address reset links die, and other devices are signed out.
		if _, err := q.RevokeUnusedTokens(ctx, store.RevokeUnusedTokensParams{UserID: u.ID, Purpose: purposeReset}); err != nil {
			return err
		}
		_, err = q.RevokeOtherSessions(ctx, store.RevokeOtherSessionsParams{UserID: u.ID, KeepSessionID: u.SessionID})
		return err
	})
	if err != nil {
		return err
	}
	httpx.LoggerFrom(ctx).Info("email changed", "user_id", u.ID)
	httpx.WriteData(w, http.StatusOK, map[string]any{"email": newEmail, "email_verified": true})
	return nil
}

// ---- e-mail content ----

func humanTTL(d time.Duration) string {
	if d >= time.Hour && d%time.Hour == 0 {
		return strconv.Itoa(int(d/time.Hour)) + " 小时"
	}
	return strconv.Itoa(int(d/time.Minute)) + " 分钟"
}

const mailFooter = "\n\n如果这不是你本人的操作，请忽略本邮件，你的账号不会发生变化。\n—— XiaoWork Blog（此邮件由系统自动发送，请勿回复）\n"

func verifyEmailMessage(to, nickname, link string, ttl time.Duration) mail.Message {
	return mail.Message{To: to, Subject: "验证你的 XiaoWork Blog 邮箱", Text: nickname + "，你好：\n\n" +
		"感谢注册 XiaoWork Blog。请在 " + humanTTL(ttl) + " 内打开下面的链接，并在页面中点击“确认验证”：\n\n" + link + mailFooter}
}

func resetPasswordMessage(to, nickname, link string, ttl time.Duration) mail.Message {
	return mail.Message{To: to, Subject: "重置你的 XiaoWork Blog 密码", Text: nickname + "，你好：\n\n" +
		"我们收到了重置密码的请求。请在 " + humanTTL(ttl) + " 内打开下面的链接设置新密码（链接只能使用一次）：\n\n" + link +
		"\n\n重置成功后，所有设备上的登录都会失效。" + mailFooter}
}

func changeEmailMessage(to, nickname, link string, ttl time.Duration) mail.Message {
	return mail.Message{To: to, Subject: "确认更换 XiaoWork Blog 登录邮箱", Text: nickname + "，你好：\n\n" +
		"你正在把 XiaoWork Blog 的登录邮箱更换为本邮箱。请在 " + humanTTL(ttl) + " 内登录原账号后打开下面的链接确认：\n\n" + link + mailFooter}
}
