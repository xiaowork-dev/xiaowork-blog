// Package site serves the public site profile and the about page (API-DD §4.1 /site, /pages/about).
// Editing them is the P7 admin "站点设置"; until then the deployment defaults of migration 00006 apply.
package site

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/xiaowork-dev/xiaowork-blog/apps/api/internal/platform/httpx"
	"github.com/xiaowork-dev/xiaowork-blog/apps/api/internal/store"
)

type Service struct {
	q *store.Queries
}

func NewService(pool *pgxpool.Pool) *Service { return &Service{q: store.New(pool)} }

// PublicRoutes mounts the permission-P endpoints.
func (s *Service) PublicRoutes(r chi.Router) {
	r.Get("/site", s.handle(s.site))
	r.Get("/pages/about", s.handle(s.about))
}

func (s *Service) handle(fn func(http.ResponseWriter, *http.Request) error) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := fn(w, r); err != nil {
			httpx.WriteError(w, r, err)
		}
	}
}

// GET /site — only the public fields: no SMTP settings, secrets or account e-mail addresses.
// contact_email is the separately configured public contact address (DD §8.1).
func (s *Service) site(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	st, err := s.q.GetSiteSettings(ctx)
	if err != nil {
		return err
	}
	social, err := s.q.ListEnabledSocialLinks(ctx)
	if err != nil {
		return err
	}
	nav, err := s.q.ListEnabledNavigationLinks(ctx)
	if err != nil {
		return err
	}
	links := make([]map[string]any, 0, len(social))
	for _, l := range social {
		links = append(links, map[string]any{"id": l.ID, "icon_key": l.IconKey, "label": l.Label, "url": l.Url})
	}
	navigation := make([]map[string]any, 0, len(nav))
	for _, n := range nav {
		navigation = append(navigation, map[string]any{"id": n.ID, "label": n.Label, "link_type": n.LinkType, "url": n.Url})
	}
	w.Header().Set("Cache-Control", "public, no-cache")
	httpx.WriteData(w, http.StatusOK, map[string]any{
		"site_title":              st.SiteTitle,
		"site_description":        st.SiteDescription,
		"profile":                 map[string]any{"name": st.ProfileName, "bio": st.ProfileBio, "avatar": media(st.ProfileAvatarMediaID)},
		"logo":                    media(st.LogoMediaID),
		"favicon":                 media(st.FaviconMediaID),
		"contact_email":           text(st.ContactEmail),
		"default_seo_title":       text(st.DefaultSeoTitle),
		"default_seo_description": text(st.DefaultSeoDescription),
		"social_links":            links,
		"navigation_links":        navigation,
	})
	return nil
}

// GET /pages/about — the saved about page; 404 while the administrator has not written one yet.
func (s *Service) about(w http.ResponseWriter, r *http.Request) error {
	p, err := s.q.GetStaticPage(r.Context(), "about")
	if errors.Is(err, pgx.ErrNoRows) {
		return httpx.ErrNotFound
	}
	if err != nil {
		return err
	}
	w.Header().Set("Cache-Control", "public, no-cache")
	httpx.WriteData(w, http.StatusOK, map[string]any{
		"title": p.Title, "body_markdown": p.BodyMarkdown, "updated_at": p.UpdatedAt.UTC(),
	})
	return nil
}

func media(id uuid.NullUUID) any {
	if !id.Valid {
		return nil
	}
	return map[string]any{"id": id.UUID, "url": "/media/" + id.UUID.String() + "/file"}
}

func text(t pgtype.Text) any {
	if !t.Valid {
		return nil
	}
	return t.String
}
