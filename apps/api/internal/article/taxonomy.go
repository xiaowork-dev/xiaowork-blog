package article

import (
	"errors"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/xiaowork-dev/xiaowork-blog/apps/api/internal/platform/httpx"
	"github.com/xiaowork-dev/xiaowork-blog/apps/api/internal/store"
)

// Categories and tags (API-DD §4.3, §5.8): name, slug, sort_order, is_active (+ description for categories).
// Renaming takes effect on public pages immediately; changing an article's assignment only after publishing.

func (s *Service) taxonomyRoutes(r chi.Router) {
	r.Get("/categories", s.handle(s.listCategories))
	r.Post("/categories", s.handle(s.createCategory))
	r.Patch("/categories/{id}", s.handle(s.patchCategory))
	r.Delete("/categories/{id}", s.handle(s.deleteTerm(true)))
	r.Get("/tags", s.handle(s.listTags))
	r.Post("/tags", s.handle(s.createTag))
	r.Patch("/tags/{id}", s.handle(s.patchTag))
	r.Delete("/tags/{id}", s.handle(s.deleteTerm(false)))
}

type termInput struct {
	Name        *string `json:"name"`
	Slug        *string `json:"slug"`
	Description *string `json:"description"`
	SortOrder   *int32  `json:"sort_order"`
	IsActive    *bool   `json:"is_active"`
}

// term is the validated, complete state of a category or tag.
type term struct {
	name, slug  string
	description pgtype.Text
	sortOrder   int32
	isActive    bool
}

func (in termInput) apply(t *term, withDescription bool) map[string][]string {
	f := map[string][]string{}
	if in.Name != nil {
		t.name = strings.TrimSpace(*in.Name)
	}
	if in.Slug != nil {
		t.slug = strings.TrimSpace(*in.Slug)
	}
	if in.SortOrder != nil {
		t.sortOrder = *in.SortOrder
	}
	if in.IsActive != nil {
		t.isActive = *in.IsActive
	}
	if withDescription && in.Description != nil {
		d := strings.TrimSpace(*in.Description)
		t.description = pgtype.Text{String: d, Valid: d != ""}
	} else if !withDescription && in.Description != nil {
		f["description"] = []string{"NOT_ALLOWED"}
	}
	if n := utf8.RuneCountInString(t.name); n < 1 || n > 60 {
		f["name"] = []string{"INVALID_LENGTH"}
	}
	if !validSlug(t.slug, maxTermSlugLen) {
		f["slug"] = []string{"INVALID_SLUG"}
	}
	if utf8.RuneCountInString(t.description.String) > 300 {
		f["description"] = []string{"TOO_LONG"}
	}
	return f
}

func categoryDTO(c store.Category) map[string]any {
	return map[string]any{"id": c.ID, "name": c.Name, "slug": c.Slug, "description": textOrNil(c.Description),
		"sort_order": c.SortOrder, "is_active": c.IsActive}
}

func tagDTO(t store.Tag) map[string]any {
	return map[string]any{"id": t.ID, "name": t.Name, "slug": t.Slug, "sort_order": t.SortOrder, "is_active": t.IsActive}
}

func (s *Service) listCategories(w http.ResponseWriter, r *http.Request) error {
	rows, err := s.q.ListCategoriesAdmin(r.Context())
	if err != nil {
		return err
	}
	out := make([]map[string]any, 0, len(rows))
	for _, c := range rows {
		m := categoryDTO(store.Category{ID: c.ID, Name: c.Name, Slug: c.Slug, Description: c.Description, SortOrder: c.SortOrder, IsActive: c.IsActive})
		m["draft_count"], m["published_count"] = c.DraftCount, c.PublishedCount
		out = append(out, m)
	}
	httpx.WriteData(w, http.StatusOK, out)
	return nil
}

func (s *Service) listTags(w http.ResponseWriter, r *http.Request) error {
	rows, err := s.q.ListTagsAdmin(r.Context())
	if err != nil {
		return err
	}
	out := make([]map[string]any, 0, len(rows))
	for _, t := range rows {
		m := tagDTO(store.Tag{ID: t.ID, Name: t.Name, Slug: t.Slug, SortOrder: t.SortOrder, IsActive: t.IsActive})
		m["draft_count"], m["published_count"] = t.DraftCount, t.PublishedCount
		out = append(out, m)
	}
	httpx.WriteData(w, http.StatusOK, out)
	return nil
}

func slugConflict(err error) bool { return isPgCode(err, "23505", "") }

func (s *Service) createCategory(w http.ResponseWriter, r *http.Request) error {
	var in termInput
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	t := term{isActive: true}
	if f := in.apply(&t, true); len(f) > 0 {
		return httpx.FieldErrors(f)
	}
	c, err := s.q.CreateCategoryFull(r.Context(), store.CreateCategoryFullParams{
		ID: uuid.New(), Name: t.name, Slug: t.slug, Description: t.description, SortOrder: t.sortOrder, IsActive: t.isActive,
	})
	if slugConflict(err) {
		return errSlugTaken
	}
	if err != nil {
		return err
	}
	httpx.WriteData(w, http.StatusCreated, categoryDTO(c))
	return nil
}

func (s *Service) patchCategory(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return err
	}
	var in termInput
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	cur, err := s.q.GetCategory(r.Context(), id)
	if errors.Is(err, pgx.ErrNoRows) {
		return httpx.ErrNotFound
	}
	if err != nil {
		return err
	}
	t := term{name: cur.Name, slug: cur.Slug, description: cur.Description, sortOrder: cur.SortOrder, isActive: cur.IsActive}
	if f := in.apply(&t, true); len(f) > 0 {
		return httpx.FieldErrors(f)
	}
	c, err := s.q.UpdateCategory(r.Context(), store.UpdateCategoryParams{
		ID: id, Name: t.name, Slug: t.slug, Description: t.description, SortOrder: t.sortOrder, IsActive: t.isActive,
	})
	if slugConflict(err) {
		return errSlugTaken
	}
	if err != nil {
		return err
	}
	httpx.WriteData(w, http.StatusOK, categoryDTO(c))
	return nil
}

func (s *Service) createTag(w http.ResponseWriter, r *http.Request) error {
	var in termInput
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	t := term{isActive: true}
	if f := in.apply(&t, false); len(f) > 0 {
		return httpx.FieldErrors(f)
	}
	tag, err := s.q.CreateTagFull(r.Context(), store.CreateTagFullParams{
		ID: uuid.New(), Name: t.name, Slug: t.slug, SortOrder: t.sortOrder, IsActive: t.isActive,
	})
	if slugConflict(err) {
		return errSlugTaken
	}
	if err != nil {
		return err
	}
	httpx.WriteData(w, http.StatusCreated, tagDTO(tag))
	return nil
}

func (s *Service) patchTag(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return err
	}
	var in termInput
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	cur, err := s.q.GetTag(r.Context(), id)
	if errors.Is(err, pgx.ErrNoRows) {
		return httpx.ErrNotFound
	}
	if err != nil {
		return err
	}
	t := term{name: cur.Name, slug: cur.Slug, sortOrder: cur.SortOrder, isActive: cur.IsActive}
	if f := in.apply(&t, false); len(f) > 0 {
		return httpx.FieldErrors(f)
	}
	tag, err := s.q.UpdateTag(r.Context(), store.UpdateTagParams{ID: id, Name: t.name, Slug: t.slug, SortOrder: t.sortOrder, IsActive: t.isActive})
	if slugConflict(err) {
		return errSlugTaken
	}
	if err != nil {
		return err
	}
	httpx.WriteData(w, http.StatusOK, tagDTO(tag))
	return nil
}

// deleteTerm removes an unused category/tag; any draft or public reference → 409 RESOURCE_IN_USE
// (the foreign keys decide, so there is no check-then-delete race).
func (s *Service) deleteTerm(category bool) func(http.ResponseWriter, *http.Request) error {
	return func(w http.ResponseWriter, r *http.Request) error {
		id, err := pathID(r)
		if err != nil {
			return err
		}
		var n int64
		if category {
			n, err = s.q.DeleteCategory(r.Context(), id)
		} else {
			n, err = s.q.DeleteTag(r.Context(), id)
		}
		if isPgCode(err, "23503", "") {
			return httpx.NewError(http.StatusConflict, "RESOURCE_IN_USE", "仍有文章（草稿或已发布版本）使用它，请先修改这些文章或将其停用。")
		}
		if err != nil {
			return err
		}
		if n == 0 {
			return httpx.ErrNotFound
		}
		w.WriteHeader(http.StatusNoContent)
		return nil
	}
}
