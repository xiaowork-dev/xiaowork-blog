// Package admin serves the P7 admin console overview and user management (API-DD §4.3 /admin/overview,
// §4.4 /admin/users*, §5.8). Every route sits behind RequireAdmin in the router.
package admin

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/xiaowork-dev/xiaowork-blog/apps/api/internal/auth"
	"github.com/xiaowork-dev/xiaowork-blog/apps/api/internal/platform/httpx"
	"github.com/xiaowork-dev/xiaowork-blog/apps/api/internal/store"
)

var (
	errProtectedAdmin = httpx.NewError(http.StatusConflict, "ADMIN_PROTECTED", "管理员账号不能在后台禁用或修改角色。")
	errSelf           = httpx.NewError(http.StatusConflict, "CANNOT_DISABLE_SELF", "不能禁用当前登录的账号。")
)

type Service struct {
	pool   *pgxpool.Pool
	q      *store.Queries
	logger *slog.Logger
}

func NewService(pool *pgxpool.Pool, logger *slog.Logger) *Service {
	return &Service{pool: pool, q: store.New(pool), logger: logger}
}

// AdminRoutes mounts the endpoints; the caller has applied RequireAdmin.
func (s *Service) AdminRoutes(r chi.Router) {
	r.Get("/overview", s.handle(s.overview))
	r.Get("/users", s.handle(s.listUsers))
	r.Get("/users/{id}", s.handle(s.getUser))
	r.Post("/users/{id}/disable", s.handle(s.disable))
	r.Post("/users/{id}/enable", s.handle(s.enable))
}

func (s *Service) handle(fn func(http.ResponseWriter, *http.Request) error) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := fn(w, r); err != nil {
			httpx.WriteError(w, r, err)
		}
	}
}

func timeOrNil(t pgtype.Timestamptz) any {
	if !t.Valid {
		return nil
	}
	return t.Time.UTC()
}

func avatarURL(id uuid.NullUUID) any {
	if !id.Valid {
		return nil
	}
	return "/media/" + id.UUID.String() + "/file"
}

// ---- overview (ADMIN-01, UI/UX §7.2): real counts, recent articles and admin events; no charts ----

// GET /admin/overview
func (s *Service) overview(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	c, err := s.q.AdminOverviewCounts(ctx)
	if err != nil {
		return err
	}
	articles, err := s.q.AdminRecentArticles(ctx, 6)
	if err != nil {
		return err
	}
	events, err := s.q.AdminRecentEvents(ctx, 10)
	if err != nil {
		return err
	}
	recent := make([]map[string]any, 0, len(articles))
	for _, a := range articles {
		var path any
		if a.Status == "PUBLISHED" && a.PublicSlug.Valid {
			path = "/posts/" + a.PublicSlug.String
		}
		recent = append(recent, map[string]any{
			"id": a.ID, "status": a.Status, "title": a.DraftTitle, "public_title": a.PublicTitle.String,
			"updated_at": a.UpdatedAt.UTC(), "public_updated_at": timeOrNil(a.PublicUpdatedAt),
			"has_unpublished_changes": a.HasUnpublishedChanges, "canonical_path": path,
		})
	}
	evs := make([]map[string]any, 0, len(events))
	for _, e := range events {
		evs = append(evs, map[string]any{
			"id": e.ID, "action": e.Action, "object_type": e.ObjectType, "object_key": e.ObjectKey,
			"actor_nickname": e.ActorNickname, "created_at": e.CreatedAt.UTC(),
		})
	}
	// Notices are derived from real state only (UI/UX §7.2 "异常/安全提示"); nothing about review queues.
	notices := []map[string]any{}
	if c.StuckMediaCount > 0 {
		notices = append(notices, map[string]any{"code": "MEDIA_PENDING_DELETE", "count": c.StuckMediaCount})
	}
	if c.AdminCount < 2 {
		notices = append(notices, map[string]any{"code": "SINGLE_ADMIN", "count": c.AdminCount})
	}
	httpx.WriteData(w, http.StatusOK, map[string]any{
		"article_count": c.ArticleCount, "published_count": c.PublishedCount, "draft_count": c.DraftCount,
		"withdrawn_count": c.WithdrawnCount, "trashed_count": c.TrashedCount, "changed_count": c.ChangedCount,
		"user_count": c.UserCount, "unverified_user_count": c.UnverifiedUserCount, "disabled_user_count": c.DisabledUserCount,
		"new_user_count": c.NewUserCount, "comment_count": c.CommentCount, "hidden_comment_count": c.HiddenCommentCount,
		"new_comment_count": c.NewCommentCount, "media_count": c.MediaCount,
		"recent_articles": recent, "recent_events": evs, "notices": notices,
	})
	return nil
}

// ---- users (ADMIN-05, UI/UX §7.6) ----

func pageParams(r *http.Request) (int, int, error) {
	qs := r.URL.Query()
	page, size := 1, 20
	if v := qs.Get("page"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 100000 {
			return 0, 0, httpx.FieldErrors(map[string][]string{"page": {"INVALID"}})
		}
		page = n
	}
	if v := qs.Get("page_size"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 100 {
			return 0, 0, httpx.FieldErrors(map[string][]string{"page_size": {"INVALID"}})
		}
		size = n
	}
	return page, size, nil
}

// GET /admin/users?q=&status=ACTIVE|DISABLED&verified=true|false&role=USER|ADMIN&page=&page_size=
func (s *Service) listUsers(w http.ResponseWriter, r *http.Request) error {
	qs := r.URL.Query()
	page, size, err := pageParams(r)
	if err != nil {
		return err
	}
	p := store.AdminListUsersParams{PageLimit: int32(size), PageOffset: int32((page - 1) * size)}
	fields := map[string][]string{}
	if v := strings.TrimSpace(qs.Get("q")); v != "" {
		if utf8.RuneCountInString(v) > 100 {
			fields["q"] = []string{"TOO_LONG"}
		}
		p.Pattern = pgtype.Text{String: "%" + strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(v) + "%", Valid: true}
	}
	switch v := qs.Get("status"); v {
	case "":
	case "ACTIVE", "DISABLED":
		p.Status = pgtype.Text{String: v, Valid: true}
	default:
		fields["status"] = []string{"INVALID"}
	}
	switch v := qs.Get("verified"); v {
	case "":
	case "true", "false":
		p.Verified = pgtype.Bool{Bool: v == "true", Valid: true}
	default:
		fields["verified"] = []string{"INVALID"}
	}
	switch v := qs.Get("role"); v {
	case "":
	case "USER", "ADMIN":
		p.Role = pgtype.Text{String: v, Valid: true}
	default:
		fields["role"] = []string{"INVALID"}
	}
	if len(fields) > 0 {
		return httpx.FieldErrors(fields)
	}
	rows, err := s.q.AdminListUsers(r.Context(), p)
	if err != nil {
		return err
	}
	var total int64
	items := make([]map[string]any, 0, len(rows))
	for _, u := range rows {
		total = u.TotalCount
		items = append(items, map[string]any{
			"id": u.ID, "email": u.Email, "nickname": u.Nickname, "role": u.Role, "status": u.Status,
			"email_verified": u.EmailVerifiedAt.Valid, "created_at": u.CreatedAt.UTC(), "avatar_url": avatarURL(u.AvatarMediaID),
			"comment_count": u.CommentCount,
		})
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"data": items, "meta": map[string]any{
		"page": page, "page_size": size, "total": total, "total_pages": (total + int64(size) - 1) / int64(size),
	}})
	return nil
}

func pathID(r *http.Request) (uuid.UUID, error) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		return uuid.Nil, httpx.ErrNotFound
	}
	return id, nil
}

// GET /admin/users/{id} — account details and a comment summary (API-DD §4.4).
func (s *Service) getUser(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return err
	}
	ctx := r.Context()
	u, err := s.q.AdminGetUser(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return httpx.ErrNotFound
	}
	if err != nil {
		return err
	}
	comments, err := s.q.AdminUserRecentComments(ctx, store.AdminUserRecentCommentsParams{AuthorID: id, RowLimit: 10})
	if err != nil {
		return err
	}
	recent := make([]map[string]any, 0, len(comments))
	for _, c := range comments {
		var body, path any
		if c.BodyText.Valid {
			body = c.BodyText.String
		}
		if c.ArticlePublic && c.ArticleSlug.Valid {
			path = "/posts/" + c.ArticleSlug.String
		}
		recent = append(recent, map[string]any{
			"id": c.ID, "status": c.Status, "body_text": body, "created_at": c.CreatedAt.UTC(),
			"article": map[string]any{"id": c.ArticleID, "title": c.ArticleTitle, "canonical_path": path},
		})
	}
	self := auth.User(r).ID == u.ID
	httpx.WriteData(w, http.StatusOK, map[string]any{
		"id": u.ID, "email": u.Email, "nickname": u.Nickname, "role": u.Role, "status": u.Status,
		"email_verified": u.EmailVerifiedAt.Valid, "email_verified_at": timeOrNil(u.EmailVerifiedAt),
		"created_at": u.CreatedAt.UTC(), "updated_at": u.UpdatedAt.UTC(), "avatar_url": avatarURL(u.AvatarMediaID),
		"bio": u.Bio.String, "comment_count": u.CommentCount, "visible_comment_count": u.VisibleCommentCount,
		"hidden_comment_count": u.HiddenCommentCount, "active_session_count": u.ActiveSessionCount,
		"recent_comments": recent, "is_self": self,
		// Only ordinary accounts can be disabled here; admins are never changed from the UI (UI/UX §7.6).
		"can_disable": u.Role == "USER" && !self && u.Status == "ACTIVE",
		"can_enable":  u.Role == "USER" && u.Status == "DISABLED",
	})
	return nil
}

// POST /admin/users/{id}/disable — ordinary users only. Every session and unused one-time token is revoked
// in the same transaction, so the account is signed out at once and cannot log in, comment or reset its
// password while disabled (API-DD §5.8, §6.3). Comments are kept (no automatic deletion).
func (s *Service) disable(w http.ResponseWriter, r *http.Request) error {
	return s.setStatus(w, r, "DISABLED")
}

// POST /admin/users/{id}/enable
func (s *Service) enable(w http.ResponseWriter, r *http.Request) error {
	return s.setStatus(w, r, "ACTIVE")
}

func (s *Service) setStatus(w http.ResponseWriter, r *http.Request, status string) error {
	id, err := pathID(r)
	if err != nil {
		return err
	}
	ctx, actor := r.Context(), auth.User(r)
	if id == actor.ID {
		return errSelf
	}
	var revoked int64
	changed := false
	err = store.ExecTx(ctx, s.pool, func(q *store.Queries) error {
		u, err := q.LockUser(ctx, id)
		if errors.Is(err, pgx.ErrNoRows) {
			return httpx.ErrNotFound
		}
		if err != nil {
			return err
		}
		if u.Role != "USER" {
			return errProtectedAdmin // also guarantees the last admin can never be disabled
		}
		if u.Status == status {
			return nil // repeat: report the current state
		}
		if _, err := q.SetUserStatus(ctx, store.SetUserStatusParams{ID: id, Status: status}); err != nil {
			return err
		}
		if status == "DISABLED" {
			if revoked, err = q.RevokeUserSessions(ctx, id); err != nil {
				return err
			}
			if _, err := q.RevokeAllUnusedTokens(ctx, id); err != nil {
				return err
			}
		}
		changed = true
		details, _ := json.Marshal(map[string]any{"status": status, "revoked_sessions": revoked})
		reqID := httpx.RequestID(ctx)
		action := "user.enable"
		if status == "DISABLED" {
			action = "user.disable"
		}
		return q.InsertAuditLog(ctx, store.InsertAuditLogParams{
			ID: uuid.New(), ActorUserID: actor.ID, Action: action, ObjectType: "user", ObjectKey: id.String(),
			RequestID: pgtype.Text{String: reqID, Valid: reqID != ""}, Details: details,
		})
	})
	if err != nil {
		return err
	}
	if changed {
		httpx.LoggerFrom(ctx).Info("user status changed", "user_id", id, "status", status, "revoked_sessions", revoked)
	}
	httpx.WriteData(w, http.StatusOK, map[string]any{"id": id, "status": status, "revoked_sessions": revoked})
	return nil
}
