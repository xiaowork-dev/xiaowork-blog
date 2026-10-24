// Package comment implements article comments (P6): public reading, posting and replying by verified
// users (published immediately, no review queue), author self-deletion with tombstones, "my comments"
// and admin moderation (hide / unhide / delete). API-DD §4.1, §4.2, §4.4, §5.5; DD §6.1, T05.
package comment

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode"
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

const (
	maxBodyRunes    = 2000 // API-DD §5.5 default; comments_body_when_present enforces it too
	defaultPageSize = 20   // DD §11.1: 20 top-level comments per page
	maxPageSize     = 50
	repliesPreview  = 2 // API-DD §5.5: up to 2 reply previews per root
)

var (
	errClosed       = httpx.NewError(http.StatusUnprocessableEntity, "COMMENT_CLOSED", "本文已关闭评论，不能发表新评论或回复。")
	errNotReplyable = httpx.NewError(http.StatusConflict, "COMMENT_NOT_REPLYABLE", "该评论已被删除或隐藏，无法回复。")
	errDuplicate    = httpx.NewError(http.StatusConflict, "DUPLICATE_COMMENT", "你刚刚已发表过相同内容的评论。")
)

type Service struct {
	pool   *pgxpool.Pool
	q      *store.Queries
	auth   *auth.Service
	logger *slog.Logger
}

func NewService(pool *pgxpool.Pool, authService *auth.Service, logger *slog.Logger) *Service {
	return &Service{pool: pool, q: store.New(pool), auth: authService, logger: logger}
}

// Routes mounts the public (P), verified-user (U) and own-account (S) endpoints.
func (s *Service) Routes(r chi.Router) {
	r.Get("/articles/{article_id}/comments", s.handle(s.listRoots))
	r.Get("/comments/{root_id}/replies", s.handle(s.listReplies))
	r.With(s.auth.RequireVerified).Post("/articles/{article_id}/comments", s.handle(s.createRoot))
	r.With(s.auth.RequireVerified).Post("/comments/{comment_id}/replies", s.handle(s.createReply))
	r.With(s.auth.RequireUser).Delete("/comments/{comment_id}", s.handle(s.deleteOwn))
	r.With(s.auth.RequireUser).Get("/me/comments", s.handle(s.listMine))
}

// AdminRoutes mounts moderation endpoints; the caller has applied RequireAdmin.
func (s *Service) AdminRoutes(r chi.Router) {
	r.Get("/comments", s.handle(s.adminList))
	r.Post("/comments/{comment_id}/hide", s.handle(s.adminHide))
	r.Post("/comments/{comment_id}/unhide", s.handle(s.adminUnhide))
	r.Delete("/comments/{comment_id}", s.handle(s.adminDelete))
}

func (s *Service) handle(fn func(http.ResponseWriter, *http.Request) error) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := fn(w, r); err != nil {
			httpx.WriteError(w, r, err)
		}
	}
}

func pathUUID(r *http.Request, name string) (uuid.UUID, error) {
	id, err := uuid.Parse(chi.URLParam(r, name))
	if err != nil {
		return uuid.Nil, httpx.ErrNotFound
	}
	return id, nil
}

func pageParams(r *http.Request) (int, int, error) {
	qs := r.URL.Query()
	page, size := 1, defaultPageSize
	if v := qs.Get("page"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 100000 {
			return 0, 0, httpx.FieldErrors(map[string][]string{"page": {"INVALID"}})
		}
		page = n
	}
	if v := qs.Get("page_size"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > maxPageSize {
			return 0, 0, httpx.FieldErrors(map[string][]string{"page_size": {"INVALID"}})
		}
		size = n
	}
	return page, size, nil
}

func pageMeta(page, size int, total int64) map[string]any {
	return map[string]any{"page": page, "page_size": size, "total": total, "total_pages": (total + int64(size) - 1) / int64(size)}
}

// ---- body validation ----

// CleanBody normalises a comment to plain text: CRLF → LF, tabs → spaces, other control and invisible
// format characters removed (except the joiners used inside emoji), more than one blank line collapsed,
// surrounding whitespace trimmed. HTML is not interpreted anywhere; clients render it as text.
func CleanBody(raw string) (string, string) {
	if !utf8.ValidString(raw) {
		return "", "INVALID"
	}
	raw = strings.ReplaceAll(strings.ReplaceAll(raw, "\r\n", "\n"), "\r", "\n")
	var b strings.Builder
	for _, r := range raw {
		switch {
		case r == '\n':
			b.WriteRune(r)
		case r == '\t':
			b.WriteRune(' ')
		case r == '‍' || r == '‌': // ZWJ / ZWNJ inside emoji and scripts
			b.WriteRune(r)
		case unicode.IsControl(r) || unicode.Is(unicode.Cf, r):
			// dropped: NUL, bidi overrides, zero-width spaces …
		default:
			b.WriteRune(r)
		}
	}
	lines := strings.Split(b.String(), "\n")
	out := make([]string, 0, len(lines))
	blank := 0
	for _, l := range lines {
		l = strings.TrimRightFunc(l, unicode.IsSpace)
		if l == "" {
			blank++
			if blank > 1 {
				continue
			}
		} else {
			blank = 0
		}
		out = append(out, l)
	}
	text := strings.TrimSpace(strings.Join(out, "\n"))
	switch n := utf8.RuneCountInString(text); {
	case n == 0:
		return "", "REQUIRED"
	case n > maxBodyRunes:
		return "", "TOO_LONG"
	}
	return text, ""
}

func decodeBody(w http.ResponseWriter, r *http.Request) (string, error) {
	var in struct {
		BodyText *string `json:"body_text"`
	}
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return "", err
	}
	if in.BodyText == nil {
		return "", httpx.FieldErrors(map[string][]string{"body_text": {"REQUIRED"}})
	}
	text, problem := CleanBody(*in.BodyText)
	if problem != "" {
		return "", httpx.FieldErrors(map[string][]string{"body_text": {problem}})
	}
	return text, nil
}

// ---- public DTO ----

type row struct {
	ID, ArticleID, AuthorID uuid.UUID
	ParentID, RootID        uuid.NullUUID
	Status                  string
	CreatedAt               time.Time
	Body                    string
	Nickname, Role          string
	Avatar                  uuid.NullUUID
	ReplyCount              *int64
	ParentNickname          string
	ParentStatus            string
}

// publicComment is the API-DD §5.5 Comment. Only VISIBLE comments carry text and author; hidden and
// deleted ones are placeholders that keep their place in the tree. Never contains an e-mail address.
func publicComment(c row, viewer *auth.CurrentUser) map[string]any {
	out := map[string]any{
		"id": c.ID, "article_id": c.ArticleID, "parent_id": nullable(c.ParentID), "root_id": nullable(c.RootID),
		"status": c.Status, "body_text": nil, "author": nil, "created_at": c.CreatedAt.UTC(),
		"can_delete": viewer != nil && viewer.ID == c.AuthorID && (c.Status == "VISIBLE" || c.Status == "HIDDEN"),
	}
	if c.Status == "VISIBLE" {
		out["body_text"] = c.Body
		var avatar any
		if c.Avatar.Valid {
			avatar = "/media/" + c.Avatar.UUID.String() + "/file"
		}
		out["author"] = map[string]any{"id": c.AuthorID, "nickname": c.Nickname, "avatar_url": avatar, "is_blog_owner": c.Role == "ADMIN"}
	}
	if c.ReplyCount != nil {
		out["reply_count"] = *c.ReplyCount
	}
	if c.ParentID.Valid {
		target := map[string]any{"id": c.ParentID.UUID, "nickname": nil}
		if c.ParentStatus == "VISIBLE" {
			target["nickname"] = c.ParentNickname
		}
		out["reply_to"] = target
	}
	return out
}

func nullable(id uuid.NullUUID) any {
	if !id.Valid {
		return nil
	}
	return id.UUID
}

// ---- public reading ----

// GET /articles/{article_id}/comments?page=&page_size=&sort=latest|oldest
func (s *Service) listRoots(w http.ResponseWriter, r *http.Request) error {
	articleID, err := pathUUID(r, "article_id")
	if err != nil {
		return err
	}
	page, size, err := pageParams(r)
	if err != nil {
		return err
	}
	sort := r.URL.Query().Get("sort")
	switch sort {
	case "":
		sort = "latest"
	case "latest", "oldest":
	default:
		return httpx.FieldErrors(map[string][]string{"sort": {"INVALID"}})
	}
	ctx := r.Context()
	state, err := s.q.GetPublicArticleCommentState(ctx, articleID)
	if errors.Is(err, pgx.ErrNoRows) {
		return httpx.ErrNotFound // unpublished, withdrawn and trashed articles do not reveal comments
	}
	if err != nil {
		return err
	}
	roots, err := s.q.ListPublicRootComments(ctx, store.ListPublicRootCommentsParams{
		ArticleID: articleID, Oldest: sort == "oldest", PageLimit: int32(size), PageOffset: int32((page - 1) * size),
	})
	if err != nil {
		return err
	}
	viewer := auth.User(r)
	ids := make([]uuid.UUID, len(roots))
	for i, c := range roots {
		ids[i] = c.ID
	}
	previews := map[uuid.UUID][]map[string]any{}
	if len(ids) > 0 {
		rows, err := s.q.ListReplyPreviews(ctx, store.ListReplyPreviewsParams{RootIds: ids, PerRoot: repliesPreview})
		if err != nil {
			return err
		}
		for _, c := range rows {
			previews[c.RootID.UUID] = append(previews[c.RootID.UUID], publicComment(row{
				ID: c.ID, ArticleID: c.ArticleID, AuthorID: c.AuthorID, ParentID: c.ParentID, RootID: c.RootID,
				Status: c.Status, CreatedAt: c.CreatedAt, Body: c.BodyText, Nickname: c.AuthorNickname,
				Role: c.AuthorRole, Avatar: c.AuthorAvatar, ParentNickname: c.ParentNickname, ParentStatus: c.ParentStatus,
			}, viewer))
		}
	}
	var total int64
	items := make([]map[string]any, 0, len(roots))
	for _, c := range roots {
		total = c.TotalCount
		count := c.ReplyCount
		item := publicComment(row{
			ID: c.ID, ArticleID: c.ArticleID, AuthorID: c.AuthorID, Status: c.Status, CreatedAt: c.CreatedAt,
			Body: c.BodyText, Nickname: c.AuthorNickname, Role: c.AuthorRole, Avatar: c.AuthorAvatar, ReplyCount: &count,
		}, viewer)
		replies := previews[c.ID]
		if replies == nil {
			replies = []map[string]any{}
		}
		item["replies"] = replies
		items = append(items, item)
	}
	if len(roots) == 0 && page > 1 {
		if first, err := s.q.ListPublicRootComments(ctx, store.ListPublicRootCommentsParams{ArticleID: articleID, PageLimit: 1}); err == nil && len(first) > 0 {
			total = first[0].TotalCount
		}
	}
	meta := pageMeta(page, size, total)
	meta["sort"] = sort
	meta["comment_count"] = state.VisibleCount
	meta["comments_enabled"] = state.CommentsEnabled
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"data": items, "meta": meta})
	return nil
}

// GET /comments/{root_id}/replies?page=&page_size= — every reply of one thread, oldest first.
func (s *Service) listReplies(w http.ResponseWriter, r *http.Request) error {
	rootID, err := pathUUID(r, "root_id")
	if err != nil {
		return err
	}
	page, size, err := pageParams(r)
	if err != nil {
		return err
	}
	ctx := r.Context()
	if _, err := s.q.GetPublicRoot(ctx, rootID); errors.Is(err, pgx.ErrNoRows) {
		return httpx.ErrNotFound
	} else if err != nil {
		return err
	}
	rows, err := s.q.ListPublicReplies(ctx, store.ListPublicRepliesParams{
		RootID: uuid.NullUUID{UUID: rootID, Valid: true}, PageLimit: int32(size), PageOffset: int32((page - 1) * size),
	})
	if err != nil {
		return err
	}
	viewer := auth.User(r)
	var total int64
	items := make([]map[string]any, 0, len(rows))
	for _, c := range rows {
		total = c.TotalCount
		items = append(items, publicComment(row{
			ID: c.ID, ArticleID: c.ArticleID, AuthorID: c.AuthorID, ParentID: c.ParentID, RootID: c.RootID,
			Status: c.Status, CreatedAt: c.CreatedAt, Body: c.BodyText, Nickname: c.AuthorNickname,
			Role: c.AuthorRole, Avatar: c.AuthorAvatar, ParentNickname: c.ParentNickname, ParentStatus: c.ParentStatus,
		}, viewer))
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"data": items, "meta": pageMeta(page, size, total)})
	return nil
}

// ---- posting (DD T05) ----

// POST /articles/{article_id}/comments — new top-level comment, VISIBLE at once.
func (s *Service) createRoot(w http.ResponseWriter, r *http.Request) error {
	articleID, err := pathUUID(r, "article_id")
	if err != nil {
		return err
	}
	return s.create(w, r, articleID, nil)
}

// POST /comments/{comment_id}/replies — reply bound to the real parent; root and article come from it.
func (s *Service) createReply(w http.ResponseWriter, r *http.Request) error {
	parentID, err := pathUUID(r, "comment_id")
	if err != nil {
		return err
	}
	parent, err := s.q.GetCommentRef(r.Context(), parentID)
	if errors.Is(err, pgx.ErrNoRows) {
		return httpx.ErrNotFound
	}
	if err != nil {
		return err
	}
	return s.create(w, r, parent.ArticleID, &parent)
}

func (s *Service) create(w http.ResponseWriter, r *http.Request, articleID uuid.UUID, parent *store.GetCommentRefRow) error {
	body, err := decodeBody(w, r)
	if err != nil {
		return err
	}
	ctx, user := r.Context(), auth.User(r)
	// API-DD §7: 5 per minute and 20 per hour per user.
	if err := s.auth.RateLimit(ctx, "COMMENT_MIN", 5, time.Minute, user.ID.String()); err != nil {
		return err
	}
	if err := s.auth.RateLimit(ctx, "COMMENT_HOUR", 20, time.Hour, user.ID.String()); err != nil {
		return err
	}
	id := uuid.New()
	var parentID, rootID uuid.NullUUID
	var created time.Time
	err = store.ExecTx(ctx, s.pool, func(q *store.Queries) error {
		article, err := q.LockArticleForComment(ctx, articleID)
		if errors.Is(err, pgx.ErrNoRows) {
			return httpx.ErrNotFound
		}
		if err != nil {
			return err
		}
		if !article.CommentsEnabled {
			return errClosed
		}
		if parent != nil {
			// Re-read under lock: the parent may have been hidden or deleted meanwhile.
			p, err := q.GetCommentForUpdate(ctx, parent.ID)
			if err != nil {
				return err
			}
			if p.Status != "VISIBLE" {
				return errNotReplyable
			}
			parentID = uuid.NullUUID{UUID: p.ID, Valid: true}
			rootID = p.RootID
			if !rootID.Valid {
				rootID = parentID // replying to a root comment
			}
		}
		dup, err := q.HasRecentDuplicateComment(ctx, store.HasRecentDuplicateCommentParams{
			AuthorID: user.ID, ArticleID: articleID, BodyText: pgtype.Text{String: body, Valid: true},
		})
		if err != nil {
			return err
		}
		if dup {
			return errDuplicate
		}
		created, err = q.InsertComment(ctx, store.InsertCommentParams{
			ID: id, ArticleID: articleID, AuthorID: user.ID, ParentID: parentID, RootID: rootID,
			BodyText: pgtype.Text{String: body, Valid: true},
		})
		return err
	})
	if err != nil {
		return err
	}
	c := row{
		ID: id, ArticleID: articleID, AuthorID: user.ID, ParentID: parentID, RootID: rootID, Status: "VISIBLE",
		CreatedAt: created, Body: body, Nickname: user.Nickname, Role: user.Role, Avatar: user.AvatarMediaID,
	}
	if parent != nil {
		c.ParentNickname, c.ParentStatus = parent.AuthorNickname, "VISIBLE"
	} else {
		zero := int64(0)
		c.ReplyCount = &zero
	}
	out := publicComment(c, user)
	if parent == nil {
		out["replies"] = []map[string]any{}
	}
	httpx.WriteData(w, http.StatusCreated, out)
	return nil
}

// ---- own comments ----

// DELETE /comments/{comment_id} — the author turns their comment into a tombstone; replies stay.
func (s *Service) deleteOwn(w http.ResponseWriter, r *http.Request) error {
	id, err := pathUUID(r, "comment_id")
	if err != nil {
		return err
	}
	ctx, user := r.Context(), auth.User(r)
	c, err := s.q.GetCommentRef(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return httpx.ErrNotFound
	}
	if err != nil {
		return err
	}
	if c.AuthorID != user.ID {
		return httpx.ErrForbidden // the server re-checks ownership; can_delete is only a UI hint
	}
	if _, err := s.q.DeleteOwnComment(ctx, store.DeleteOwnCommentParams{ID: id, AuthorID: user.ID}); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent) // already deleted: still 204 (idempotent)
	return nil
}

// GET /me/comments?page=&page_size= — own history with the article while it is public.
func (s *Service) listMine(w http.ResponseWriter, r *http.Request) error {
	page, size, err := pageParams(r)
	if err != nil {
		return err
	}
	user := auth.User(r)
	rows, err := s.q.ListMyComments(r.Context(), store.ListMyCommentsParams{
		AuthorID: user.ID, PageLimit: int32(size), PageOffset: int32((page - 1) * size),
	})
	if err != nil {
		return err
	}
	var total int64
	items := make([]map[string]any, 0, len(rows))
	for _, c := range rows {
		total = c.TotalCount
		article := map[string]any{"id": c.ArticleID, "available": c.ArticlePublic, "title": nil, "canonical_path": nil}
		if c.ArticlePublic {
			article["title"] = c.ArticleTitle
			article["canonical_path"] = "/posts/" + c.ArticleSlug
		}
		var body any
		if c.BodyText != "" {
			body = c.BodyText
		}
		items = append(items, map[string]any{
			"id": c.ID, "parent_id": nullable(c.ParentID), "root_id": nullable(c.RootID), "status": c.Status,
			"body_text": body, "created_at": c.CreatedAt.UTC(), "article": article,
			"can_delete": c.Status == "VISIBLE" || c.Status == "HIDDEN",
		})
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"data": items, "meta": pageMeta(page, size, total)})
	return nil
}

// ---- admin moderation ----

var adminStatuses = map[string]bool{"VISIBLE": true, "HIDDEN": true, "DELETED_AUTHOR": true, "DELETED_ADMIN": true, "DELETED": true}

// GET /admin/comments?article_id=&author_id=&status=&from=&to=&q=&page=&page_size=
// from/to are dates (YYYY-MM-DD, site time zone handled by the client) or RFC 3339 instants.
func (s *Service) adminList(w http.ResponseWriter, r *http.Request) error {
	qs := r.URL.Query()
	page, size, err := pageParams(r)
	if err != nil {
		return err
	}
	p := store.ListAdminCommentsParams{PageLimit: int32(size), PageOffset: int32((page - 1) * size)}
	fields := map[string][]string{}
	for key, dst := range map[string]*uuid.NullUUID{"article_id": &p.ArticleID, "author_id": &p.AuthorID} {
		if v := qs.Get(key); v != "" {
			id, err := uuid.Parse(v)
			if err != nil {
				fields[key] = []string{"INVALID"}
				continue
			}
			*dst = uuid.NullUUID{UUID: id, Valid: true}
		}
	}
	if v := qs.Get("status"); v != "" {
		if !adminStatuses[v] {
			fields["status"] = []string{"INVALID"}
		}
		p.Status = pgtype.Text{String: v, Valid: true}
	}
	for key, dst := range map[string]*pgtype.Timestamptz{"from": &p.CreatedFrom, "to": &p.CreatedTo} {
		if v := qs.Get(key); v != "" {
			t, err := time.Parse(time.RFC3339, v)
			if err != nil {
				fields[key] = []string{"INVALID"}
				continue
			}
			*dst = pgtype.Timestamptz{Time: t, Valid: true}
		}
	}
	if v := strings.TrimSpace(qs.Get("q")); v != "" {
		if utf8.RuneCountInString(v) > 100 {
			fields["q"] = []string{"TOO_LONG"}
		}
		p.Pattern = pgtype.Text{String: "%" + escapeLike(v) + "%", Valid: true}
	}
	if len(fields) > 0 {
		return httpx.FieldErrors(fields)
	}
	rows, err := s.q.ListAdminComments(r.Context(), p)
	if err != nil {
		return err
	}
	var total int64
	items := make([]map[string]any, 0, len(rows))
	for _, c := range rows {
		total = c.TotalCount
		var body, path any
		if c.BodyText.Valid {
			body = c.BodyText.String
		}
		if c.ArticleStatus == "PUBLISHED" && c.ArticleSlug.Valid {
			path = "/posts/" + c.ArticleSlug.String
		}
		items = append(items, map[string]any{
			"id": c.ID, "parent_id": nullable(c.ParentID), "root_id": nullable(c.RootID), "status": c.Status,
			"body_text": body, "created_at": c.CreatedAt.UTC(), "hidden_at": timeOrNil(c.HiddenAt),
			"deleted_at": timeOrNil(c.DeletedAt), "reply_count": c.ReplyCount,
			"author": map[string]any{
				"id": c.AuthorID, "nickname": c.AuthorNickname, "email": c.AuthorEmail, "role": c.AuthorRole, "status": c.AuthorStatus,
			},
			"article": map[string]any{
				"id": c.ArticleID, "title": c.ArticleTitle, "status": c.ArticleStatus, "canonical_path": path,
			},
		})
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"data": items, "meta": pageMeta(page, size, total)})
	return nil
}

func timeOrNil(t pgtype.Timestamptz) any {
	if !t.Valid {
		return nil
	}
	return t.Time.UTC()
}

func escapeLike(v string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(v)
}

// POST /admin/comments/{id}/hide — reversible; the text leaves every public response at once.
func (s *Service) adminHide(w http.ResponseWriter, r *http.Request) error {
	return s.moderate(w, r, "comment.hide", func(q *store.Queries, id uuid.UUID) (int64, error) { return q.HideComment(r.Context(), id) }, "VISIBLE", "HIDDEN")
}

// POST /admin/comments/{id}/unhide
func (s *Service) adminUnhide(w http.ResponseWriter, r *http.Request) error {
	return s.moderate(w, r, "comment.unhide", func(q *store.Queries, id uuid.UUID) (int64, error) { return q.UnhideComment(r.Context(), id) }, "HIDDEN", "VISIBLE")
}

// DELETE /admin/comments/{id} — irreversible tombstone (DELETED_ADMIN); replies keep their parent.
func (s *Service) adminDelete(w http.ResponseWriter, r *http.Request) error {
	id, err := pathUUID(r, "comment_id")
	if err != nil {
		return err
	}
	ctx := r.Context()
	err = store.ExecTx(ctx, s.pool, func(q *store.Queries) error {
		c, err := q.GetCommentForUpdate(ctx, id)
		if errors.Is(err, pgx.ErrNoRows) {
			return httpx.ErrNotFound
		}
		if err != nil {
			return err
		}
		n, err := q.AdminDeleteComment(ctx, id)
		if err != nil || n == 0 {
			return err // already deleted: idempotent, no second audit entry
		}
		return s.audit(r, q, "comment.delete", c)
	})
	if err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *Service) moderate(w http.ResponseWriter, r *http.Request, action string,
	apply func(*store.Queries, uuid.UUID) (int64, error), from, to string) error {
	id, err := pathUUID(r, "comment_id")
	if err != nil {
		return err
	}
	ctx := r.Context()
	var status string
	err = store.ExecTx(ctx, s.pool, func(q *store.Queries) error {
		c, err := q.GetCommentForUpdate(ctx, id)
		if errors.Is(err, pgx.ErrNoRows) {
			return httpx.ErrNotFound
		}
		if err != nil {
			return err
		}
		status = c.Status
		switch c.Status {
		case to:
			return nil // repeat: return the current state
		case from:
		default:
			return httpx.NewError(http.StatusConflict, "COMMENT_STATE_CONFLICT", "评论已被删除，无法执行该操作。")
		}
		if _, err := apply(q, id); err != nil {
			return err
		}
		status = to
		return s.audit(r, q, action, c)
	})
	if err != nil {
		return err
	}
	httpx.WriteData(w, http.StatusOK, map[string]any{"id": id, "status": status})
	return nil
}

// audit writes admin_audit_logs without the comment text (DD §8.5: no needless personal data).
func (s *Service) audit(r *http.Request, q *store.Queries, action string, c store.GetCommentForUpdateRow) error {
	details, _ := json.Marshal(map[string]any{"article_id": c.ArticleID, "author_id": c.AuthorID, "previous_status": c.Status})
	reqID := httpx.RequestID(r.Context())
	return q.InsertAuditLog(r.Context(), store.InsertAuditLogParams{
		ID: uuid.New(), ActorUserID: auth.User(r).ID, Action: action, ObjectType: "comment", ObjectKey: c.ID.String(),
		RequestID: pgtype.Text{String: reqID, Valid: reqID != ""}, Details: details,
	})
}
