package article

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/xiaowork-dev/xiaowork-blog/apps/api/internal/auth"
	"github.com/xiaowork-dev/xiaowork-blog/apps/api/internal/platform/httpx"
	"github.com/xiaowork-dev/xiaowork-blog/apps/api/internal/store"
)

// Service implements /admin/articles, /admin/categories, /admin/tags and the public article endpoints.
type Service struct {
	pool     *pgxpool.Pool
	q        *store.Queries
	siteHost string // host of PUBLIC_BASE_URL; absolute /media URLs on it count as references
	guard    Guard
	logger   *slog.Logger
}

func NewService(pool *pgxpool.Pool, siteHost string, guard Guard, logger *slog.Logger) *Service {
	return &Service{pool: pool, q: store.New(pool), siteHost: siteHost, guard: guard, logger: logger}
}

const (
	maxDraftBody    = 2 << 20 // 2 MiB of Markdown (API-DD §5.2 default)
	maxTitleRunes   = 200
	maxSummaryRunes = 500
	maxSlugLen      = 160
	maxTagsPerDraft = 20
	maxTermSlugLen  = 100
)

var (
	errTrashed      = httpx.NewError(http.StatusConflict, "ARTICLE_TRASHED", "文章在回收站中，请先恢复后再编辑或发布。")
	errNotPublished = httpx.NewError(http.StatusConflict, "ARTICLE_NOT_PUBLISHED", "文章当前未公开，无需撤回。")
	errSlugTaken    = httpx.NewError(http.StatusConflict, "SLUG_TAKEN", "该 Slug 已被其他文章使用（包括历史地址），请修改后重试。")
)

func revisionConflict(current int64) error {
	return &httpx.APIError{Status: http.StatusConflict, Code: "DRAFT_REVISION_CONFLICT",
		Message: "草稿已在其他位置更新，请重新加载后比较差异。", Details: map[string]any{"current_revision": current}}
}

func mediaNotReady(ids []uuid.UUID) error {
	return &httpx.APIError{Status: http.StatusUnprocessableEntity, Code: "MEDIA_NOT_READY",
		Message: "文章引用的图片不存在、已删除或不可用于文章。", Details: map[string]any{"media_ids": ids}}
}

// AdminRoutes mounts the article and taxonomy endpoints; the caller has applied RequireAdmin.
func (s *Service) AdminRoutes(r chi.Router) {
	r.Get("/articles", s.handle(s.list))
	r.Post("/articles", s.handle(s.create))
	r.Get("/articles/{id}", s.handle(s.detail))
	r.Put("/articles/{id}/draft", s.handle(s.saveDraft))
	r.Post("/articles/{id}/publish", s.handle(s.publish))
	r.Post("/articles/{id}/withdraw", s.handle(s.withdraw))
	r.Delete("/articles/{id}", s.handle(s.trash))
	r.Post("/articles/{id}/restore", s.handle(s.restore))
	s.taxonomyRoutes(r)
}

func (s *Service) handle(fn func(http.ResponseWriter, *http.Request) error) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := fn(w, r); err != nil {
			httpx.WriteError(w, r, err)
		}
	}
}

func pathID(r *http.Request) (uuid.UUID, error) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		return uuid.Nil, httpx.ErrNotFound
	}
	return id, nil
}

func isPgCode(err error, code, constraint string) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == code && (constraint == "" || pgErr.ConstraintName == constraint)
}

func pgText(s *string) pgtype.Text {
	if s == nil {
		return pgtype.Text{}
	}
	return pgtype.Text{String: *s, Valid: true}
}

func nullID(id *uuid.UUID) uuid.NullUUID {
	if id == nil {
		return uuid.NullUUID{}
	}
	return uuid.NullUUID{UUID: *id, Valid: true}
}

func textOrNil(t pgtype.Text) any {
	if !t.Valid {
		return nil
	}
	return t.String
}

func idOrNil(id uuid.NullUUID) any {
	if !id.Valid {
		return nil
	}
	return id.UUID
}

func timeOrNil(t pgtype.Timestamptz) any {
	if !t.Valid {
		return nil
	}
	return t.Time.UTC()
}

func mediaURL(id uuid.UUID) string { return "/media/" + id.String() + "/file" }

// hasUnpublishedChanges follows API-DD §5.2: draft revision differs from the last published one.
func hasUnpublishedChanges(a store.Article, draftRevision int64) bool {
	return !a.LastPublishedDraftRevision.Valid || a.LastPublishedDraftRevision.Int64 != draftRevision
}

func (s *Service) audit(ctx context.Context, q *store.Queries, r *http.Request, action string, id uuid.UUID, details map[string]any) error {
	var raw []byte
	if details != nil {
		raw, _ = json.Marshal(details)
	}
	reqID := httpx.RequestID(r.Context())
	return q.InsertAuditLog(ctx, store.InsertAuditLogParams{
		ID: uuid.New(), ActorUserID: auth.User(r).ID, Action: action, ObjectType: "article", ObjectKey: id.String(),
		RequestID: pgtype.Text{String: reqID, Valid: reqID != ""}, Details: raw,
	})
}

// ---- list / create / detail ----

func escapeLike(v string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(v)
}

// GET /admin/articles?status=&q=&page=&page_size=
func (s *Service) list(w http.ResponseWriter, r *http.Request) error {
	qs := r.URL.Query()
	page, size, err := pageParams(qs.Get("page"), qs.Get("page_size"), 20)
	if err != nil {
		return err
	}
	status := strings.ToUpper(qs.Get("status"))
	switch status {
	case "", "DRAFT", "PUBLISHED", "WITHDRAWN", "TRASHED", "CHANGED":
	default:
		return httpx.FieldErrors(map[string][]string{"status": {"INVALID"}})
	}
	q := strings.TrimSpace(qs.Get("q"))
	if utf8.RuneCountInString(q) > 100 {
		return httpx.FieldErrors(map[string][]string{"q": {"TOO_LONG"}})
	}
	rows, err := s.q.ListAdminArticles(r.Context(), store.ListAdminArticlesParams{
		StatusFilter: status, Pattern: "%" + escapeLike(q) + "%", PageLimit: int32(size), PageOffset: int32((page - 1) * size),
	})
	if err != nil {
		return err
	}
	items := make([]map[string]any, 0, len(rows))
	var total int64
	for _, a := range rows {
		total = a.TotalCount
		// Same rule as the detail endpoint (API-DD §5.2): the draft differs from what was last published.
		changed := !a.LastPublishedDraftRevision.Valid || a.LastPublishedDraftRevision.Int64 != a.DraftRevision
		items = append(items, map[string]any{
			"id": a.ID, "status": a.Status, "title": a.DraftTitle, "public_title": textOrNil(a.PublicTitle),
			"slug": textOrNil(a.PublicSlug), "candidate_slug": textOrNil(a.CandidateSlug), "category_name": textOrNil(a.CategoryName),
			"draft_revision": a.DraftRevision, "saved_at": a.SavedAt.UTC(), "has_unpublished_changes": changed,
			"first_published_at": timeOrNil(a.FirstPublishedAt), "public_updated_at": timeOrNil(a.PublicUpdatedAt),
			"updated_at": a.UpdatedAt.UTC(), "trashed_at": timeOrNil(a.TrashedAt),
		})
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"data": items, "meta": map[string]any{
		"page": page, "page_size": size, "total": total, "total_pages": (total + int64(size) - 1) / int64(size),
	}})
	return nil
}

func pageParams(p, ps string, def int) (int, int, error) {
	page, size := 1, def
	if p != "" {
		n, err := strconv.Atoi(p)
		if err != nil || n < 1 || n > 100000 {
			return 0, 0, httpx.FieldErrors(map[string][]string{"page": {"INVALID"}})
		}
		page = n
	}
	if ps != "" {
		n, err := strconv.Atoi(ps)
		if err != nil || n < 1 || n > 100 {
			return 0, 0, httpx.FieldErrors(map[string][]string{"page_size": {"INVALID"}})
		}
		size = n
	}
	return page, size, nil
}

// POST /admin/articles — empty draft, revision 1 (API-DD §5.2, DD T03 step 1).
func (s *Service) create(w http.ResponseWriter, r *http.Request) error {
	ctx, user := r.Context(), auth.User(r)
	id := uuid.New()
	var draft store.ArticleDraft
	err := store.ExecTx(ctx, s.pool, func(q *store.Queries) error {
		if _, err := q.CreateArticle(ctx, store.CreateArticleParams{ID: id, AuthorID: user.ID}); err != nil {
			return err
		}
		d, err := q.CreateArticleDraft(ctx, store.CreateArticleDraftParams{ArticleID: id, LastEditedBy: user.ID})
		if err != nil {
			return err
		}
		draft = d
		return q.CreateArticleMetrics(ctx, id)
	})
	if err != nil {
		return err
	}
	httpx.LoggerFrom(ctx).Info("article created", "article_id", id)
	httpx.WriteData(w, http.StatusCreated, map[string]any{
		"article_id": id, "status": "DRAFT", "draft": map[string]any{"revision": draft.Revision, "saved_at": draft.SavedAt.UTC()},
	})
	return nil
}

func draftDTO(d store.ArticleDraft, tagIDs []uuid.UUID) map[string]any {
	var cover any
	if d.CoverMediaID.Valid {
		cover = map[string]any{"id": d.CoverMediaID.UUID, "url": mediaURL(d.CoverMediaID.UUID)}
	}
	return map[string]any{
		"revision": d.Revision, "title": d.Title, "summary": textOrNil(d.Summary), "body_markdown": d.BodyMarkdown,
		"candidate_slug": textOrNil(d.CandidateSlug), "category_id": idOrNil(d.CategoryID), "tag_ids": tagIDs,
		"cover_media_id": idOrNil(d.CoverMediaID), "cover": cover, "seo_title": textOrNil(d.SeoTitle),
		"seo_description": textOrNil(d.SeoDescription), "comments_enabled": d.CommentsEnabled, "saved_at": d.SavedAt.UTC(),
	}
}

// GET /admin/articles/{id} — lifecycle, full draft and a summary of the current public version.
func (s *Service) detail(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return err
	}
	ctx := r.Context()
	a, err := s.q.GetArticle(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return httpx.ErrNotFound
	}
	if err != nil {
		return err
	}
	d, err := s.q.GetArticleDraft(ctx, id)
	if err != nil {
		return err
	}
	tags, err := s.q.ListDraftTagIDs(ctx, id)
	if err != nil {
		return err
	}
	var publication any
	if p, err := s.q.GetPublication(ctx, id); err == nil {
		publication = map[string]any{
			"publication_revision": p.PublicationRevision, "source_draft_revision": p.SourceDraftRevision,
			"title": p.Title, "slug": p.Slug, "canonical_path": "/posts/" + p.Slug, "published_at": p.PublishedAt.UTC(),
			"is_public": a.Status == "PUBLISHED",
		}
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	httpx.WriteData(w, http.StatusOK, map[string]any{
		"article_id": a.ID, "status": a.Status, "created_at": a.CreatedAt.UTC(), "updated_at": a.UpdatedAt.UTC(),
		"first_published_at": timeOrNil(a.FirstPublishedAt), "public_updated_at": timeOrNil(a.PublicUpdatedAt),
		"trashed_at": timeOrNil(a.TrashedAt), "withdrawn_at": timeOrNil(a.WithdrawnAt),
		"has_unpublished_changes": hasUnpublishedChanges(a, d.Revision),
		"draft":                   draftDTO(d, tags), "current_publication": publication,
	})
	return nil
}

// ---- draft save (API-DD §5.2: full snapshot + revision CAS) ----

type draftInput struct {
	ExpectedRevision int64       `json:"expected_revision"`
	Title            string      `json:"title"`
	Summary          *string     `json:"summary"`
	BodyMarkdown     string      `json:"body_markdown"`
	CandidateSlug    *string     `json:"candidate_slug"`
	CategoryID       *uuid.UUID  `json:"category_id"`
	TagIDs           []uuid.UUID `json:"tag_ids"`
	CoverMediaID     *uuid.UUID  `json:"cover_media_id"`
	SeoTitle         *string     `json:"seo_title"`
	SeoDescription   *string     `json:"seo_description"`
	CommentsEnabled  bool        `json:"comments_enabled"`
}

var draftKeys = []string{"expected_revision", "title", "summary", "body_markdown", "candidate_slug", "category_id",
	"tag_ids", "cover_media_id", "seo_title", "seo_description", "comments_enabled"}

// decodeDraft enforces the full-snapshot contract: every field must be present (nullable ones as null),
// unknown fields are rejected, the body may be up to maxDraftBody.
func decodeDraft(w http.ResponseWriter, r *http.Request) (*draftInput, error) {
	if mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type")); err != nil || mt != "application/json" {
		return nil, httpx.ErrUnsupportedType
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxDraftBody+64<<10))
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			return nil, httpx.ErrPayloadTooLarge
		}
		return nil, httpx.ErrInvalidRequest
	}
	var present map[string]json.RawMessage
	if err := json.Unmarshal(raw, &present); err != nil {
		return nil, httpx.ErrInvalidRequest
	}
	missing := map[string][]string{}
	for _, k := range draftKeys {
		if _, ok := present[k]; !ok {
			missing[k] = []string{"REQUIRED"}
		}
	}
	if len(missing) > 0 {
		return nil, &httpx.APIError{Status: http.StatusBadRequest, Code: "INVALID_REQUEST",
			Message: "草稿必须提交完整字段（可空字段请显式传 null）。", Details: map[string]any{"fields": missing}}
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var in draftInput
	if err := dec.Decode(&in); err != nil {
		return nil, httpx.ErrInvalidRequest
	}
	return &in, nil
}

func trimOptional(p *string) *string {
	if p == nil {
		return nil
	}
	v := strings.TrimSpace(*p)
	if v == "" {
		return nil
	}
	return &v
}

func (in *draftInput) normalise() map[string][]string {
	f := map[string][]string{}
	if in.ExpectedRevision < 1 {
		f["expected_revision"] = []string{"INVALID"}
	}
	if utf8.RuneCountInString(in.Title) > maxTitleRunes {
		f["title"] = []string{"TOO_LONG"}
	}
	if !utf8.ValidString(in.BodyMarkdown) || len(in.BodyMarkdown) > maxDraftBody {
		f["body_markdown"] = []string{"TOO_LONG"}
	}
	in.Summary, in.SeoTitle, in.SeoDescription = trimOptional(in.Summary), trimOptional(in.SeoTitle), trimOptional(in.SeoDescription)
	if in.Summary != nil && utf8.RuneCountInString(*in.Summary) > maxSummaryRunes {
		f["summary"] = []string{"TOO_LONG"}
	}
	if in.SeoTitle != nil && utf8.RuneCountInString(*in.SeoTitle) > 200 {
		f["seo_title"] = []string{"TOO_LONG"}
	}
	if in.SeoDescription != nil && utf8.RuneCountInString(*in.SeoDescription) > 320 {
		f["seo_description"] = []string{"TOO_LONG"}
	}
	if in.CandidateSlug = trimOptional(in.CandidateSlug); in.CandidateSlug != nil && !validSlug(*in.CandidateSlug, maxSlugLen) {
		f["candidate_slug"] = []string{"INVALID_SLUG"}
	}
	seen := map[uuid.UUID]bool{}
	tags := in.TagIDs[:0:0]
	for _, t := range in.TagIDs {
		if !seen[t] {
			seen[t] = true
			tags = append(tags, t)
		}
	}
	in.TagIDs = tags
	if len(in.TagIDs) > maxTagsPerDraft {
		f["tag_ids"] = []string{"TOO_MANY"}
	}
	return f
}

// checkMedia locks the referenced media FOR SHARE and verifies each is READY and not an avatar.
func checkMedia(ctx context.Context, q *store.Queries, ids []uuid.UUID) error {
	if len(ids) == 0 {
		return nil
	}
	rows, err := q.LockReadyMedia(ctx, ids)
	if err != nil {
		return err
	}
	ok := map[uuid.UUID]bool{}
	for _, row := range rows {
		ok[row.ID] = row.UsageKind != "AVATAR"
	}
	var bad []uuid.UUID
	for _, id := range ids {
		if !ok[id] {
			bad = append(bad, id)
		}
	}
	if len(bad) > 0 {
		return mediaNotReady(bad)
	}
	return nil
}

// PUT /admin/articles/{id}/draft — only the draft and its relations change; the public snapshot,
// public tags and public media are never touched here (DD §1 constraint 1, API-12).
func (s *Service) saveDraft(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return err
	}
	in, err := decodeDraft(w, r)
	if err != nil {
		return err
	}
	if f := in.normalise(); len(f) > 0 {
		return httpx.FieldErrors(f)
	}
	ctx, user := r.Context(), auth.User(r)
	bodyMedia := mediaRefs(in.BodyMarkdown, s.siteHost)
	var saved store.UpdateArticleDraftRow
	var article store.Article
	err = store.ExecTx(ctx, s.pool, func(q *store.Queries) error {
		a, err := q.LockArticle(ctx, id) // DD T03: lock the article before the draft
		if errors.Is(err, pgx.ErrNoRows) {
			return httpx.ErrNotFound
		}
		if err != nil {
			return err
		}
		if a.Status == "TRASHED" {
			return errTrashed
		}
		article = a
		if in.CategoryID != nil {
			if _, err := q.GetCategory(ctx, *in.CategoryID); errors.Is(err, pgx.ErrNoRows) {
				return httpx.FieldErrors(map[string][]string{"category_id": {"NOT_FOUND"}})
			} else if err != nil {
				return err
			}
		}
		if len(in.TagIDs) > 0 {
			n, err := q.CountExistingTags(ctx, in.TagIDs)
			if err != nil {
				return err
			}
			if n != int64(len(in.TagIDs)) {
				return httpx.FieldErrors(map[string][]string{"tag_ids": {"NOT_FOUND"}})
			}
		}
		all := append([]uuid.UUID{}, bodyMedia...)
		if in.CoverMediaID != nil {
			all = append(all, *in.CoverMediaID)
		}
		if err := checkMedia(ctx, q, all); err != nil {
			return err
		}
		row, err := q.UpdateArticleDraft(ctx, store.UpdateArticleDraftParams{
			Title: in.Title, Summary: pgText(in.Summary), BodyMarkdown: in.BodyMarkdown, CandidateSlug: pgText(in.CandidateSlug),
			CategoryID: nullID(in.CategoryID), CoverMediaID: nullID(in.CoverMediaID), SeoTitle: pgText(in.SeoTitle),
			SeoDescription: pgText(in.SeoDescription), CommentsEnabled: in.CommentsEnabled, LastEditedBy: user.ID,
			ArticleID: id, ExpectedRevision: in.ExpectedRevision,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			current, cerr := q.CurrentDraftRevision(ctx, id)
			if cerr != nil {
				return cerr
			}
			return revisionConflict(current) // a stale request can never overwrite a newer draft
		}
		if err != nil {
			return err
		}
		saved = row
		if err := q.DeleteDraftTags(ctx, id); err != nil {
			return err
		}
		if err := q.AddDraftTags(ctx, store.AddDraftTagsParams{ArticleID: id, TagIds: in.TagIDs}); err != nil {
			return err
		}
		if err := q.DeleteDraftMedia(ctx, id); err != nil {
			return err
		}
		if err := q.AddDraftMedia(ctx, store.AddDraftMediaParams{ArticleID: id, MediaIds: bodyMedia}); err != nil {
			return err
		}
		return q.TouchArticle(ctx, id)
	})
	if err != nil {
		return err
	}
	httpx.WriteData(w, http.StatusOK, map[string]any{
		"article_id": id, "revision": saved.Revision, "saved_at": saved.SavedAt.UTC(),
		"has_unpublished_changes": hasUnpublishedChanges(article, saved.Revision),
	})
	return nil
}

// ---- publish (API-DD §5.3, DD T03 step 3) ----

func (s *Service) publish(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return err
	}
	var in struct {
		ExpectedDraftRevision int64 `json:"expected_draft_revision"`
	}
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	if in.ExpectedDraftRevision < 1 {
		return httpx.FieldErrors(map[string][]string{"expected_draft_revision": {"INVALID"}})
	}
	ctx := r.Context()
	var result map[string]any
	err = store.ExecTx(ctx, s.pool, func(q *store.Queries) error {
		a, err := q.LockArticle(ctx, id)
		if errors.Is(err, pgx.ErrNoRows) {
			return httpx.ErrNotFound
		}
		if err != nil {
			return err
		}
		if a.Status == "TRASHED" {
			return errTrashed
		}
		d, err := q.GetArticleDraft(ctx, id)
		if err != nil {
			return err
		}
		if d.Revision != in.ExpectedDraftRevision {
			return revisionConflict(d.Revision)
		}
		// Same revision re-published while still PUBLISHED: business no-op (API-14). A withdrawn article
		// published again with the same revision is a real publish (API-15).
		if a.Status == "PUBLISHED" && a.LastPublishedDraftRevision.Valid && a.LastPublishedDraftRevision.Int64 == d.Revision {
			p, err := q.GetPublication(ctx, id)
			if err != nil {
				return err
			}
			result = publishDTO(id, p, false)
			return nil
		}

		fields := map[string][]string{}
		if strings.TrimSpace(d.Title) == "" {
			fields["title"] = []string{"REQUIRED"}
		}
		if strings.TrimSpace(d.BodyMarkdown) == "" {
			fields["body_markdown"] = []string{"REQUIRED"}
		}
		if len(fields) > 0 {
			return httpx.FieldErrors(fields)
		}

		slug, err := s.resolveSlug(ctx, q, a, d)
		if err != nil {
			return err
		}
		mediaIDs, err := q.ListDraftMediaIDs(ctx, id)
		if err != nil {
			return err
		}
		if d.CoverMediaID.Valid {
			mediaIDs = append(mediaIDs, d.CoverMediaID.UUID)
		}
		if err := checkMedia(ctx, q, mediaIDs); err != nil {
			return err
		}

		if err := q.RegisterArticleSlug(ctx, store.RegisterArticleSlugParams{Slug: slug, ArticleID: id}); err != nil {
			if isPgCode(err, "23505", "article_slugs_pkey") {
				return errSlugTaken // the slug registry's primary key decides races (DB-10)
			}
			return err
		}
		if err := q.ClearCurrentArticleSlug(ctx, store.ClearCurrentArticleSlugParams{ArticleID: id, Slug: slug}); err != nil {
			return err
		}
		if err := q.SetCurrentArticleSlug(ctx, store.SetCurrentArticleSlugParams{Slug: slug, ArticleID: id}); err != nil {
			return err
		}
		plain := plainText(d.BodyMarkdown)
		now := time.Now().UTC().Truncate(time.Microsecond)
		if _, err := q.UpsertPublicationFromDraft(ctx, store.UpsertPublicationFromDraftParams{
			FallbackSummary: autoSummary(summaryText(d.BodyMarkdown)), BodyPlainText: plain, Slug: slug, PublishedAt: now, ArticleID: id,
		}); err != nil {
			return err
		}
		for _, step := range []func(context.Context, uuid.UUID) error{
			q.DeletePublicationTags, q.CopyDraftTagsToPublication, q.DeletePublicationMedia, q.CopyDraftMediaToPublication,
		} {
			if err := step(ctx, id); err != nil {
				return err
			}
		}
		if err := q.MarkArticlePublished(ctx, store.MarkArticlePublishedParams{PublishedAt: now, DraftRevision: d.Revision, ID: id}); err != nil {
			return err
		}
		p, err := q.GetPublication(ctx, id)
		if err != nil {
			return err
		}
		if err := s.audit(ctx, q, r, "ARTICLE_PUBLISH", id, map[string]any{
			"publication_revision": p.PublicationRevision, "source_draft_revision": d.Revision, "slug": slug,
		}); err != nil {
			return err
		}
		result = publishDTO(id, p, true)
		return nil
	})
	if err != nil {
		return err
	}
	httpx.LoggerFrom(ctx).Info("article published", "article_id", id, "changed", result["published_changed"])
	httpx.WriteData(w, http.StatusOK, result)
	return nil
}

func publishDTO(id uuid.UUID, p store.ArticlePublication, changed bool) map[string]any {
	return map[string]any{
		"article_id": id, "status": "PUBLISHED", "publication_revision": p.PublicationRevision,
		"source_draft_revision": p.SourceDraftRevision, "slug": p.Slug, "canonical_path": "/posts/" + p.Slug,
		"published_changed": changed, "published_at": p.PublishedAt.UTC(),
	}
}

// resolveSlug: the draft's candidate slug if set; otherwise keep the current public slug; otherwise derive
// one from the title (PRD POST-09 "自动生成且唯一"), falling back to an id-based slug. Only manually chosen
// slugs fail with SLUG_TAKEN; generated ones are made unique.
func (s *Service) resolveSlug(ctx context.Context, q *store.Queries, a store.Article, d store.ArticleDraft) (string, error) {
	if d.CandidateSlug.Valid {
		return d.CandidateSlug.String, nil
	}
	if p, err := q.GetPublication(ctx, a.ID); err == nil {
		return p.Slug, nil
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return "", err
	}
	short := strings.ReplaceAll(a.ID.String(), "-", "")[:8]
	base := slugify(d.Title)
	if base == "" {
		return "post-" + short, nil
	}
	owner, err := q.SlugOwner(ctx, base)
	switch {
	case errors.Is(err, pgx.ErrNoRows) || err == nil && owner == a.ID:
		return base, nil
	case err != nil:
		return "", err
	}
	return base + "-" + short, nil
}

// ---- withdraw / trash / restore (API-DD §5.4, DD T04) ----

func (s *Service) lifecycle(w http.ResponseWriter, r *http.Request, fn func(ctx context.Context, q *store.Queries, a store.Article) (status string, changed bool, err error), action string, code int) error {
	id, err := pathID(r)
	if err != nil {
		return err
	}
	ctx := r.Context()
	var status string
	err = store.ExecTx(ctx, s.pool, func(q *store.Queries) error {
		a, err := q.LockArticle(ctx, id)
		if errors.Is(err, pgx.ErrNoRows) {
			return httpx.ErrNotFound
		}
		if err != nil {
			return err
		}
		st, changed, err := fn(ctx, q, a)
		if err != nil {
			return err
		}
		status = st
		if changed {
			return s.audit(ctx, q, r, action, id, map[string]any{"from": a.Status, "to": st})
		}
		return nil
	})
	if err != nil {
		return err
	}
	if code == http.StatusNoContent {
		w.WriteHeader(code)
		return nil
	}
	httpx.WriteData(w, code, map[string]any{"article_id": id, "status": status})
	return nil
}

// POST /admin/articles/{id}/withdraw: PUBLISHED → WITHDRAWN; repeating it reports the current state.
func (s *Service) withdraw(w http.ResponseWriter, r *http.Request) error {
	return s.lifecycle(w, r, func(ctx context.Context, q *store.Queries, a store.Article) (string, bool, error) {
		switch a.Status {
		case "PUBLISHED":
			_, err := q.WithdrawArticle(ctx, a.ID)
			return "WITHDRAWN", true, err
		case "WITHDRAWN":
			return "WITHDRAWN", false, nil
		}
		return "", false, errNotPublished
	}, "ARTICLE_WITHDRAW", http.StatusOK)
}

// DELETE /admin/articles/{id}: soft delete to the recycle bin; comments and media are kept.
func (s *Service) trash(w http.ResponseWriter, r *http.Request) error {
	return s.lifecycle(w, r, func(ctx context.Context, q *store.Queries, a store.Article) (string, bool, error) {
		if a.Status == "TRASHED" {
			return "TRASHED", false, nil
		}
		_, err := q.TrashArticle(ctx, a.ID)
		return "TRASHED", true, err
	}, "ARTICLE_TRASH", http.StatusNoContent)
}

// POST /admin/articles/{id}/restore: back to WITHDRAWN (was published) or DRAFT — never PUBLISHED.
func (s *Service) restore(w http.ResponseWriter, r *http.Request) error {
	return s.lifecycle(w, r, func(ctx context.Context, q *store.Queries, a store.Article) (string, bool, error) {
		if a.Status != "TRASHED" {
			return a.Status, false, nil
		}
		st, err := q.RestoreArticle(ctx, a.ID)
		return st, true, err
	}, "ARTICLE_RESTORE", http.StatusOK)
}

// CheckMedia is checkMedia for other modules (site settings, about page): same READY / not-avatar rule and
// the same FOR SHARE lock against concurrent deletion.
func CheckMedia(ctx context.Context, q *store.Queries, ids []uuid.UUID) error {
	return checkMedia(ctx, q, ids)
}
