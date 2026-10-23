package article

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/xiaowork-dev/xiaowork-blog/apps/api/internal/platform/httpx"
	"github.com/xiaowork-dev/xiaowork-blog/apps/api/internal/store"
)

// Guard is what the public endpoints need from the auth module: the shared persistent rate limiter,
// the browser context of a request and the trusted client IP.
type Guard interface {
	RateLimit(ctx context.Context, action string, limit int, window time.Duration, subject string) error
	VisitorKey(r *http.Request) (string, bool)
	ClientIP(r *http.Request) string
}

const (
	publicPageSize   = 10 // API-DD §3.1 default for public lists
	maxQueryRunes    = 100
	maxSearchTerms   = 5
	archiveRowLimit  = 2000
	viewDedupWindow  = 30 * time.Minute
	viewIPLimit      = 120
	viewIPWindow     = 10 * time.Minute
	searchLimit      = 60
	searchWindow     = time.Minute
	publicCacheValue = "public, no-cache" // cacheable, but always revalidated: publish/withdraw show at once
	// SiteTimeZone is the single display/archive time zone of V1.0 (ADR-0005); Nuxt formats dates in it too.
	SiteTimeZone = "Asia/Shanghai"
)

// Bots, crawlers and headless browsers never count as reads (API-DD §7).
var botUA = regexp.MustCompile(`(?i)bot|crawl|spider|slurp|preview|headless|lighthouse|curl|wget|python|httpclient|go-http|java/|okhttp|axios|node-fetch|scrapy|phantom|facebookexternalhit`)

// articleSummary is the shared row shape of the public list and search queries.
type articleSummary = store.ListPublicArticlesRow

// PublicRoutes mounts the public article, discovery and view endpoints (API-DD §4.1, permission P).
func (s *Service) PublicRoutes(r chi.Router) {
	r.Get("/articles", s.handle(s.publicList))
	r.Get("/articles/{slug}", s.handle(s.publicDetail))
	r.Post("/articles/{id}/views", s.handle(s.recordView))
	r.Get("/search/articles", s.handle(s.search))
	r.Get("/categories", s.handle(s.publicCategories))
	r.Get("/categories/{slug}/articles", s.handle(s.categoryArticles))
	r.Get("/tags", s.handle(s.publicTags))
	r.Get("/tags/{slug}/articles", s.handle(s.tagArticles))
	r.Get("/archives", s.handle(s.archives))
}

// GET /articles?page=&page_size=&category=&tag=&sort=latest|oldest
func (s *Service) publicList(w http.ResponseWriter, r *http.Request) error {
	qs := r.URL.Query()
	var category, tag uuid.NullUUID
	var meta map[string]any
	if slug := qs.Get("category"); slug != "" {
		c, err := s.categoryBySlug(r.Context(), slug)
		if err != nil {
			return err
		}
		category = uuid.NullUUID{UUID: c.ID, Valid: true}
		meta = map[string]any{"category": publicCategoryDTO(c)}
	}
	if slug := qs.Get("tag"); slug != "" {
		t, err := s.tagBySlug(r.Context(), slug)
		if err != nil {
			return err
		}
		tag = uuid.NullUUID{UUID: t.ID, Valid: true}
		meta = map[string]any{"tag": publicTagDTO(t)}
	}
	return s.writeList(w, r, category, tag, meta)
}

// GET /categories/{slug}/articles — meta.category describes the category.
func (s *Service) categoryArticles(w http.ResponseWriter, r *http.Request) error {
	c, err := s.categoryBySlug(r.Context(), chi.URLParam(r, "slug"))
	if err != nil {
		return err
	}
	return s.writeList(w, r, uuid.NullUUID{UUID: c.ID, Valid: true}, uuid.NullUUID{}, map[string]any{"category": publicCategoryDTO(c)})
}

// GET /tags/{slug}/articles — meta.tag describes the tag.
func (s *Service) tagArticles(w http.ResponseWriter, r *http.Request) error {
	t, err := s.tagBySlug(r.Context(), chi.URLParam(r, "slug"))
	if err != nil {
		return err
	}
	return s.writeList(w, r, uuid.NullUUID{}, uuid.NullUUID{UUID: t.ID, Valid: true}, map[string]any{"tag": publicTagDTO(t)})
}

func (s *Service) writeList(w http.ResponseWriter, r *http.Request, category, tag uuid.NullUUID, extra map[string]any) error {
	qs := r.URL.Query()
	page, size, err := pageParams(qs.Get("page"), qs.Get("page_size"), publicPageSize)
	if err != nil {
		return err
	}
	var oldest bool
	switch qs.Get("sort") {
	case "", "latest":
	case "oldest":
		oldest = true
	default:
		return httpx.FieldErrors(map[string][]string{"sort": {"INVALID"}})
	}
	rows, err := s.q.ListPublicArticles(r.Context(), store.ListPublicArticlesParams{
		CategoryID: category, TagID: tag, Oldest: oldest, PageLimit: int32(size), PageOffset: int32((page - 1) * size),
	})
	if err != nil {
		return err
	}
	return s.writeSummaries(w, r, rows, page, size, extra)
}

// GET /search/articles?q=&page=&page_size= — keyword search of public snapshots only (DISCOVER-01).
// Whitespace separates terms; every term must match the title, summary or body (case-insensitive
// substring, so Chinese words work without a tokenizer). No semantic or fuzzy ranking is claimed.
func (s *Service) search(w http.ResponseWriter, r *http.Request) error {
	qs := r.URL.Query()
	q := strings.TrimSpace(qs.Get("q"))
	switch {
	case q == "":
		return httpx.FieldErrors(map[string][]string{"q": {"REQUIRED"}})
	case utf8.RuneCountInString(q) > maxQueryRunes:
		return httpx.FieldErrors(map[string][]string{"q": {"TOO_LONG"}})
	}
	page, size, err := pageParams(qs.Get("page"), qs.Get("page_size"), publicPageSize)
	if err != nil {
		return err
	}
	if err := s.guard.RateLimit(r.Context(), "SEARCH", searchLimit, searchWindow, s.guard.ClientIP(r)); err != nil {
		return err
	}
	terms := searchTerms(q)
	patterns := make([]string, len(terms))
	for i, t := range terms {
		patterns[i] = "%" + escapeLike(t) + "%"
	}
	rows, err := s.q.SearchPublicArticles(r.Context(), store.SearchPublicArticlesParams{
		FirstPattern: patterns[0], Patterns: patterns, PageLimit: int32(size), PageOffset: int32((page - 1) * size),
	})
	if err != nil {
		return err
	}
	summaries := make([]articleSummary, len(rows))
	for i, row := range rows {
		summaries[i] = articleSummary(row)
	}
	return s.writeSummaries(w, r, summaries, page, size, map[string]any{"q": q, "terms": terms})
}

// searchTerms splits on whitespace, drops duplicates (case-insensitively) and keeps the longest
// terms first so the index pre-filter uses the most selective one.
func searchTerms(q string) []string {
	seen := map[string]bool{}
	var terms []string
	for _, f := range strings.FieldsFunc(q, unicode.IsSpace) {
		k := strings.ToLower(f)
		if seen[k] {
			continue
		}
		seen[k] = true
		terms = append(terms, f)
	}
	for i := 1; i < len(terms); i++ {
		for j := i; j > 0 && utf8.RuneCountInString(terms[j]) > utf8.RuneCountInString(terms[j-1]); j-- {
			terms[j], terms[j-1] = terms[j-1], terms[j]
		}
	}
	if len(terms) > maxSearchTerms {
		terms = terms[:maxSearchTerms]
	}
	return terms
}

func (s *Service) writeSummaries(w http.ResponseWriter, r *http.Request, rows []articleSummary, page, size int, extra map[string]any) error {
	ids := make([]uuid.UUID, len(rows))
	for i, row := range rows {
		ids[i] = row.ID
	}
	tagsByArticle := map[uuid.UUID][]map[string]any{}
	if len(ids) > 0 {
		tags, err := s.q.ListPublicationTagsForArticles(r.Context(), ids)
		if err != nil {
			return err
		}
		for _, t := range tags {
			tagsByArticle[t.ArticleID] = append(tagsByArticle[t.ArticleID], map[string]any{"id": t.ID, "name": t.Name, "slug": t.Slug})
		}
	}
	var total int64
	items := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		total = row.TotalCount
		tags := tagsByArticle[row.ID]
		if tags == nil {
			tags = []map[string]any{}
		}
		var cover, category any
		if row.CoverMediaID.Valid {
			cover = map[string]any{"id": row.CoverMediaID.UUID, "url": mediaURL(row.CoverMediaID.UUID)}
		}
		if row.CategoryID.Valid {
			category = map[string]any{"id": row.CategoryID.UUID, "name": row.CategoryName.String, "slug": row.CategorySlug.String}
		}
		items = append(items, map[string]any{
			"id": row.ID, "slug": row.Slug, "title": row.Title, "summary": row.Summary, "cover": cover,
			"category": category, "tags": tags, "first_published_at": timeOrNil(row.FirstPublishedAt),
			"public_updated_at": timeOrNil(row.PublicUpdatedAt), "view_count": row.ViewCount,
			"comments_enabled": row.CommentsEnabled, "canonical_path": "/posts/" + row.Slug,
		})
	}
	if len(rows) == 0 && page > 1 {
		// An out-of-range page still reports the real total so the UI can link back to the last page.
		total = s.countFallback(r, extra)
	}
	meta := map[string]any{"page": page, "page_size": size, "total": total, "total_pages": (total + int64(size) - 1) / int64(size)}
	for k, v := range extra {
		meta[k] = v
	}
	w.Header().Set("Cache-Control", publicCacheValue)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"data": items, "meta": meta})
	return nil
}

// countFallback re-runs the query for page 1 to learn the total when the requested page is empty.
func (s *Service) countFallback(r *http.Request, extra map[string]any) int64 {
	ctx := r.Context()
	if q, ok := extra["q"].(string); ok {
		terms := searchTerms(q)
		patterns := make([]string, len(terms))
		for i, t := range terms {
			patterns[i] = "%" + escapeLike(t) + "%"
		}
		rows, err := s.q.SearchPublicArticles(ctx, store.SearchPublicArticlesParams{FirstPattern: patterns[0], Patterns: patterns, PageLimit: 1})
		if err != nil || len(rows) == 0 {
			return 0
		}
		return rows[0].TotalCount
	}
	p := store.ListPublicArticlesParams{PageLimit: 1}
	if c, ok := extra["category"].(map[string]any); ok {
		p.CategoryID = uuid.NullUUID{UUID: c["id"].(uuid.UUID), Valid: true}
	}
	if t, ok := extra["tag"].(map[string]any); ok {
		p.TagID = uuid.NullUUID{UUID: t["id"].(uuid.UUID), Valid: true}
	}
	rows, err := s.q.ListPublicArticles(ctx, p)
	if err != nil || len(rows) == 0 {
		return 0
	}
	return rows[0].TotalCount
}

// GET /categories — active categories with their public article counts.
func (s *Service) publicCategories(w http.ResponseWriter, r *http.Request) error {
	rows, err := s.q.ListPublicCategories(r.Context())
	if err != nil {
		return err
	}
	items := make([]map[string]any, 0, len(rows))
	for _, c := range rows {
		items = append(items, map[string]any{
			"id": c.ID, "name": c.Name, "slug": c.Slug, "description": textOrNil(c.Description), "article_count": c.ArticleCount,
		})
	}
	w.Header().Set("Cache-Control", publicCacheValue)
	httpx.WriteData(w, http.StatusOK, items)
	return nil
}

// GET /tags — active tags with their public article counts.
func (s *Service) publicTags(w http.ResponseWriter, r *http.Request) error {
	rows, err := s.q.ListPublicTags(r.Context())
	if err != nil {
		return err
	}
	items := make([]map[string]any, 0, len(rows))
	for _, t := range rows {
		items = append(items, map[string]any{"id": t.ID, "name": t.Name, "slug": t.Slug, "article_count": t.ArticleCount})
	}
	w.Header().Set("Cache-Control", publicCacheValue)
	httpx.WriteData(w, http.StatusOK, items)
	return nil
}

// GET /archives?year= — public articles grouped by year and month of first publication (SiteTimeZone).
// meta.years lists every year with its count so the page can offer the other years.
func (s *Service) archives(w http.ResponseWriter, r *http.Request) error {
	var year pgtype.Int4
	if v := r.URL.Query().Get("year"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1970 || n > 9999 {
			return httpx.FieldErrors(map[string][]string{"year": {"INVALID"}})
		}
		year = pgtype.Int4{Int32: int32(n), Valid: true}
	}
	ctx := r.Context()
	years, err := s.q.ListArchiveYears(ctx, SiteTimeZone)
	if err != nil {
		return err
	}
	rows, err := s.q.ListArchiveArticles(ctx, store.ListArchiveArticlesParams{Tz: SiteTimeZone, Year: year, RowLimit: archiveRowLimit + 1})
	if err != nil {
		return err
	}
	truncated := len(rows) > archiveRowLimit
	if truncated {
		rows = rows[:archiveRowLimit]
	}
	type month struct {
		Year     int              `json:"year"`
		Month    int              `json:"month"`
		Articles []map[string]any `json:"articles"`
	}
	groups := []*month{}
	for _, row := range rows {
		t := row.FirstPublishedAt.Time.UTC()
		if n := len(groups); n == 0 || groups[n-1].Year != int(row.Year) || groups[n-1].Month != int(row.Month) {
			groups = append(groups, &month{Year: int(row.Year), Month: int(row.Month)})
		}
		g := groups[len(groups)-1]
		g.Articles = append(g.Articles, map[string]any{
			"id": row.ID, "slug": row.Slug, "title": row.Title, "first_published_at": t, "canonical_path": "/posts/" + row.Slug,
		})
	}
	yearList := make([]map[string]any, 0, len(years))
	var total int64
	for _, y := range years {
		yearList = append(yearList, map[string]any{"year": y.Year, "article_count": y.ArticleCount})
		total += y.ArticleCount
	}
	var selected any
	if year.Valid {
		selected = year.Int32
	}
	w.Header().Set("Cache-Control", publicCacheValue)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"data": groups, "meta": map[string]any{
		"years": yearList, "year": selected, "total": total, "truncated": truncated,
	}})
	return nil
}

// POST /articles/{id}/views — approximate read counter (API-DD §4.1, §7; DD §5.9).
// The browser reports a read after the page has been visible for a while; SSR never calls this.
// Counted at most once per article per browser context (session or anonymous CSRF cookie) within
// 30 minutes, bots are ignored and each IP is rate limited. Only HMAC digests with an expiry are
// stored (auth_rate_windows), never a permanent visitor log. Always 204 unless limited or unknown.
func (s *Service) recordView(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return err
	}
	ctx := r.Context()
	if err := s.guard.RateLimit(ctx, "ARTICLE_VIEW_IP", viewIPLimit, viewIPWindow, s.guard.ClientIP(r)); err != nil {
		return err
	}
	published, err := s.q.IsArticlePublished(ctx, id)
	if err != nil {
		return err
	}
	if !published {
		return httpx.ErrNotFound
	}
	visitor, ok := s.guard.VisitorKey(r)
	ua := r.UserAgent()
	if ok && ua != "" && !botUA.MatchString(ua) {
		err := s.guard.RateLimit(ctx, "ARTICLE_VIEW", 1, viewDedupWindow, id.String()+"|"+visitor)
		var apiErr *httpx.APIError
		switch {
		case err == nil:
			if err := s.q.IncrementArticleViews(ctx, id); err != nil {
				return err
			}
		case errors.As(err, &apiErr) && apiErr.Code == "RATE_LIMITED":
			// already counted for this browser in the current window
		default:
			return err
		}
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *Service) categoryBySlug(ctx context.Context, slug string) (store.GetCategoryBySlugRow, error) {
	if !validSlug(slug, maxTermSlugLen) {
		return store.GetCategoryBySlugRow{}, httpx.ErrNotFound
	}
	c, err := s.q.GetCategoryBySlug(ctx, slug)
	if errors.Is(err, pgx.ErrNoRows) {
		return c, httpx.ErrNotFound
	}
	return c, err
}

func (s *Service) tagBySlug(ctx context.Context, slug string) (store.GetTagBySlugRow, error) {
	if !validSlug(slug, maxTermSlugLen) {
		return store.GetTagBySlugRow{}, httpx.ErrNotFound
	}
	t, err := s.q.GetTagBySlug(ctx, slug)
	if errors.Is(err, pgx.ErrNoRows) {
		return t, httpx.ErrNotFound
	}
	return t, err
}

func publicCategoryDTO(c store.GetCategoryBySlugRow) map[string]any {
	return map[string]any{"id": c.ID, "name": c.Name, "slug": c.Slug, "description": textOrNil(c.Description)}
}

func publicTagDTO(t store.GetTagBySlugRow) map[string]any {
	return map[string]any{"id": t.ID, "name": t.Name, "slug": t.Slug}
}
