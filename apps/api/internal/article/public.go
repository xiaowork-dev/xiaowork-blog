package article

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/xiaowork-dev/xiaowork-blog/apps/api/internal/platform/httpx"
	"github.com/xiaowork-dev/xiaowork-blog/apps/api/internal/store"
)

// GET /api/v1/articles/{slug} (API-DD §5.1). Only the public snapshot of a PUBLISHED article is ever read;
// drafts, withdrawn and trashed articles are a plain 404. An old slug returns the article with
// canonical_path and resolved_from_old_slug=true; the Nuxt page turns that into the HTTP 301.
func (s *Service) publicDetail(w http.ResponseWriter, r *http.Request) error {
	slug := chi.URLParam(r, "slug")
	if !validSlug(slug, maxSlugLen) {
		return httpx.ErrNotFound
	}
	ctx := r.Context()
	a, err := s.q.GetPublishedArticleBySlug(ctx, slug)
	if errors.Is(err, pgx.ErrNoRows) {
		return httpx.ErrNotFound
	}
	if err != nil {
		return err
	}
	tags, err := s.q.ListPublicationTagsFull(ctx, a.ID)
	if err != nil {
		return err
	}
	tagList := make([]map[string]any, 0, len(tags))
	for _, t := range tags {
		tagList = append(tagList, map[string]any{"id": t.ID, "name": t.Name, "slug": t.Slug})
	}
	var category, cover any
	if a.CategoryID.Valid {
		if c, err := s.q.GetCategory(ctx, a.CategoryID.UUID); err == nil {
			category = map[string]any{"id": c.ID, "name": c.Name, "slug": c.Slug}
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
	}
	if a.CoverMediaID.Valid {
		cover = map[string]any{"id": a.CoverMediaID.UUID, "url": mediaURL(a.CoverMediaID.UUID)}
	}
	views, err := s.q.GetArticleViews(ctx, a.ID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	neighbour := func(fn func() (string, string, error)) (any, error) {
		slug, title, err := fn()
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		return map[string]any{"slug": slug, "title": title, "canonical_path": "/posts/" + slug}, nil
	}
	params := store.PreviousPublishedArticleParams{FirstPublishedAt: a.FirstPublishedAt.Time, ID: a.ID}
	prev, err := neighbour(func() (string, string, error) {
		row, err := s.q.PreviousPublishedArticle(ctx, params)
		return row.Slug, row.Title, err
	})
	if err != nil {
		return err
	}
	next, err := neighbour(func() (string, string, error) {
		row, err := s.q.NextPublishedArticle(ctx, store.NextPublishedArticleParams(params))
		return row.Slug, row.Title, err
	})
	if err != nil {
		return err
	}
	// Published content may be revalidated by caches but never served stale without asking.
	w.Header().Set("Cache-Control", "public, no-cache")
	httpx.WriteData(w, http.StatusOK, map[string]any{
		"id": a.ID, "slug": a.CurrentSlug, "title": a.Title, "summary": a.Summary, "body_markdown": a.BodyMarkdown,
		"cover": cover, "category": category, "tags": tagList, "seo_title": textOrNil(a.SeoTitle),
		"seo_description": textOrNil(a.SeoDescription), "comments_enabled": a.CommentsEnabled,
		"first_published_at": timeOrNil(a.FirstPublishedAt), "public_updated_at": timeOrNil(a.PublicUpdatedAt),
		"view_count": views, "canonical_path": "/posts/" + a.CurrentSlug, "resolved_from_old_slug": a.ResolvedFromOldSlug,
		"previous_article": prev, "next_article": next,
	})
	return nil
}
