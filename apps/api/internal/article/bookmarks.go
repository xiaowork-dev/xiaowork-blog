package article

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/xiaowork-dev/xiaowork-blog/apps/api/internal/auth"
	"github.com/xiaowork-dev/xiaowork-blog/apps/api/internal/platform/httpx"
	"github.com/xiaowork-dev/xiaowork-blog/apps/api/internal/store"
)

// P8-B12: related articles on the article page and reader bookmarks ("收藏"). Only public snapshots of
// PUBLISHED articles are ever returned; a bookmark of an article that is later withdrawn stays stored
// but is hidden until the article is public again.

const (
	relatedLimit      = 3
	maxBookmarks      = 1000
	bookmarkPageSize  = 10
	bookmarkRateLimit = 60
	bookmarkRateWin   = time.Minute
)

var errBookmarkLimit = httpx.NewError(http.StatusUnprocessableEntity, "BOOKMARK_LIMIT", "收藏已达上限（1000 篇），请先取消一些收藏。")

func (s *Service) bookmarkRoutes(r chi.Router) {
	r.Get("/articles/{id}/related", s.handle(s.related))
	r.Group(func(u chi.Router) {
		u.Use(s.guard.RequireUser)
		u.Get("/me/bookmarks", s.handle(s.listBookmarks))
		u.Get("/me/bookmarks/ids", s.handle(s.bookmarkIDs))
		u.Put("/me/bookmarks/{id}", s.handle(s.addBookmark))
		u.Delete("/me/bookmarks/{id}", s.handle(s.removeBookmark))
	})
}

// GET /articles/{id}/related — up to 3 other public articles, most related first (shared tags, then the
// same category, then newest). 404 when the article itself is not public.
func (s *Service) related(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return err
	}
	ctx := r.Context()
	published, err := s.q.IsArticlePublished(ctx, id)
	if err != nil {
		return err
	}
	if !published {
		return httpx.ErrNotFound
	}
	rows, err := s.q.ListRelatedArticles(ctx, store.ListRelatedArticlesParams{ArticleID: id, RowLimit: relatedLimit})
	if err != nil {
		return err
	}
	summaries := make([]articleSummary, len(rows))
	for i, row := range rows {
		summaries[i] = articleSummary(row)
	}
	return s.writeSummaries(w, r, summaries, 1, relatedLimit, nil)
}

// GET /me/bookmarks?page=&page_size= — the reader's public bookmarked articles, newest bookmark first.
func (s *Service) listBookmarks(w http.ResponseWriter, r *http.Request) error {
	qs := r.URL.Query()
	page, size, err := pageParams(qs.Get("page"), qs.Get("page_size"), bookmarkPageSize)
	if err != nil {
		return err
	}
	rows, err := s.q.ListBookmarkedArticles(r.Context(), store.ListBookmarkedArticlesParams{
		UserID: auth.User(r).ID, PageLimit: int32(size), PageOffset: int32((page - 1) * size),
	})
	if err != nil {
		return err
	}
	summaries := make([]articleSummary, len(rows))
	for i, row := range rows {
		summaries[i] = articleSummary(row)
	}
	w.Header().Set("Cache-Control", "private, no-store")
	// An empty out-of-range page reports total 0; the page offers a link back to page 1.
	return s.writeSummaries(w, r, summaries, page, size, nil)
}

// GET /me/bookmarks/ids — IDs of the reader's public bookmarked articles (for marking cards).
func (s *Service) bookmarkIDs(w http.ResponseWriter, r *http.Request) error {
	ids, err := s.q.ListBookmarkedArticleIDs(r.Context(), store.ListBookmarkedArticleIDsParams{UserID: auth.User(r).ID, RowLimit: maxBookmarks})
	if err != nil {
		return err
	}
	httpx.WriteData(w, http.StatusOK, ids)
	return nil
}

// PUT /me/bookmarks/{id} — idempotent; only public articles can be bookmarked.
func (s *Service) addBookmark(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return err
	}
	ctx, user := r.Context(), auth.User(r)
	if err := s.guard.RateLimit(ctx, "BOOKMARK", bookmarkRateLimit, bookmarkRateWin, user.ID.String()); err != nil {
		return err
	}
	published, err := s.q.IsArticlePublished(ctx, id)
	if err != nil {
		return err
	}
	if !published {
		return httpx.ErrNotFound
	}
	n, err := s.q.CountBookmarks(ctx, user.ID)
	if err != nil {
		return err
	}
	if n >= maxBookmarks {
		return errBookmarkLimit
	}
	if _, err := s.q.AddBookmark(ctx, store.AddBookmarkParams{UserID: user.ID, ArticleID: id}); err != nil {
		return err
	}
	httpx.WriteData(w, http.StatusOK, map[string]any{"article_id": id, "bookmarked": true})
	return nil
}

// DELETE /me/bookmarks/{id} — idempotent.
func (s *Service) removeBookmark(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return err
	}
	if _, err := s.q.DeleteBookmark(r.Context(), store.DeleteBookmarkParams{UserID: auth.User(r).ID, ArticleID: id}); err != nil {
		return err
	}
	httpx.WriteData(w, http.StatusOK, map[string]any{"article_id": id, "bookmarked": false})
	return nil
}
