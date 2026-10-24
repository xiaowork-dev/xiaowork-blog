package site

// Admin side of the site profile (P7, ADMIN-07): GET/PATCH /admin/site, PUT /admin/site/social-links,
// PUT /admin/site/navigation-links, GET/PUT /admin/pages/about (API-DD §4.4, §5.8; DD §8.1–§8.4).
// Only public presentation fields live here; deployment secrets (SMTP, database, session keys) are
// environment variables and are never read or written by these endpoints.

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/mail"
	"net/url"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/xiaowork-dev/xiaowork-blog/apps/api/internal/article"
	"github.com/xiaowork-dev/xiaowork-blog/apps/api/internal/auth"
	"github.com/xiaowork-dev/xiaowork-blog/apps/api/internal/platform/httpx"
	"github.com/xiaowork-dev/xiaowork-blog/apps/api/internal/store"
)

const (
	maxSocialLinks = 12
	maxNavLinks    = 10
	maxAboutBody   = 1 << 20 // 1 MiB of Markdown
	maxURLLen      = 2048
)

// IconKeys is the allow-list of profile link icons (DD §8.2 "预设图标集合"); the web UI draws each of them.
var IconKeys = map[string]bool{"github": true, "mail": true, "rss": true, "link": true}

// Internal navigation must point at public pages: admin, account, auth and API paths need a login or are
// not pages at all (DD §8.3 "避免产生需要认证才能访问的公开导航目标").
var privatePath = regexp.MustCompile(`^/(admin|account|auth|api|media)(/|$|\?|#)`)

// AdminRoutes mounts the admin endpoints; the caller has applied RequireAdmin.
func (s *Service) AdminRoutes(r chi.Router) {
	r.Get("/site", s.handle(s.adminSite))
	r.Patch("/site", s.handle(s.patchSite))
	r.Put("/site/social-links", s.handle(s.putSocialLinks))
	r.Put("/site/navigation-links", s.handle(s.putNavigationLinks))
	r.Get("/pages/about", s.handle(s.adminAbout))
	r.Put("/pages/about", s.handle(s.putAbout))
}

// ---- helpers ----

func cleanLine(s string) (string, bool) {
	s = strings.TrimSpace(s)
	for _, r := range s {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return "", false
		}
	}
	return s, true
}

func checkText(fields map[string][]string, key, v string, min, max int) string {
	s, ok := cleanLine(v)
	n := utf8.RuneCountInString(s)
	switch {
	case !ok:
		fields[key] = []string{"INVALID_CHARACTERS"}
	case n < min:
		fields[key] = []string{"REQUIRED"}
	case n > max:
		fields[key] = []string{"TOO_LONG"}
	}
	return s
}

func validEmail(s string) bool {
	// One plain address only: no display name, query (?subject=), fragment, path or address list.
	if len(s) > 254 || strings.ContainsAny(s, " \t\r\n<>\"?#/,;:") {
		return false
	}
	a, err := mail.ParseAddress(s)
	return err == nil && a.Name == "" && a.Address == s && strings.Contains(s[strings.LastIndex(s, "@")+1:], ".")
}

// SafeExternalURL accepts https URLs with a host, or mailto: with one plain address (API-DD §5.8).
func SafeExternalURL(raw string, allowMailto bool) bool {
	if raw == "" || len(raw) > maxURLLen || strings.IndexFunc(raw, unicode.IsSpace) >= 0 {
		return false
	}
	if allowMailto && strings.HasPrefix(strings.ToLower(raw), "mailto:") {
		return validEmail(raw[len("mailto:"):])
	}
	u, err := url.Parse(raw)
	return err == nil && u.Scheme == "https" && u.Host != "" && u.User == nil && strings.HasPrefix(raw, "https://")
}

// SafeInternalPath accepts a site-relative public path such as /about or /categories/dev.
func SafeInternalPath(raw string) bool {
	if len(raw) > 200 || !strings.HasPrefix(raw, "/") || strings.HasPrefix(raw, "//") || strings.Contains(raw, `\`) ||
		strings.IndexFunc(raw, unicode.IsSpace) >= 0 || privatePath.MatchString(raw) {
		return false
	}
	u, err := url.Parse(raw)
	return err == nil && u.Scheme == "" && u.Host == ""
}

func (s *Service) audit(r *http.Request, q *store.Queries, action, key string, details map[string]any) error {
	raw, _ := json.Marshal(details)
	reqID := httpx.RequestID(r.Context())
	return q.InsertAuditLog(r.Context(), store.InsertAuditLogParams{
		ID: uuid.New(), ActorUserID: auth.User(r).ID, Action: action, ObjectType: "site", ObjectKey: key,
		RequestID: pgtype.Text{String: reqID, Valid: reqID != ""}, Details: raw,
	})
}

func textOrNil(t pgtype.Text) any {
	if !t.Valid {
		return nil
	}
	return t.String
}

// ---- site settings ----

func settingsDTO(st store.SiteSetting) map[string]any {
	return map[string]any{
		"site_title": st.SiteTitle, "site_description": st.SiteDescription, "profile_name": st.ProfileName,
		"profile_bio": st.ProfileBio, "profile_avatar": media(st.ProfileAvatarMediaID), "logo": media(st.LogoMediaID),
		"favicon": media(st.FaviconMediaID), "contact_email": textOrNil(st.ContactEmail),
		"default_seo_title": textOrNil(st.DefaultSeoTitle), "default_seo_description": textOrNil(st.DefaultSeoDescription),
		"updated_at": st.UpdatedAt.UTC(),
	}
}

// GET /admin/site — editable settings plus every social/navigation link (enabled or not).
func (s *Service) adminSite(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	st, err := s.q.GetSiteSettings(ctx)
	if err != nil {
		return err
	}
	social, err := s.q.ListAllSocialLinks(ctx)
	if err != nil {
		return err
	}
	nav, err := s.q.ListAllNavigationLinks(ctx)
	if err != nil {
		return err
	}
	out := settingsDTO(st)
	links := make([]map[string]any, 0, len(social))
	for _, l := range social {
		links = append(links, map[string]any{"id": l.ID, "icon_key": l.IconKey, "label": l.Label, "url": l.Url, "is_enabled": l.IsEnabled})
	}
	navigation := make([]map[string]any, 0, len(nav))
	for _, n := range nav {
		navigation = append(navigation, map[string]any{"id": n.ID, "label": n.Label, "link_type": n.LinkType, "url": n.Url, "is_enabled": n.IsEnabled})
	}
	out["social_links"] = links
	out["navigation_links"] = navigation
	out["icon_keys"] = []string{"github", "mail", "rss", "link"}
	httpx.WriteData(w, http.StatusOK, out)
	return nil
}

var siteFields = map[string]bool{
	"site_title": true, "site_description": true, "profile_name": true, "profile_bio": true,
	"profile_avatar_media_id": true, "logo_media_id": true, "favicon_media_id": true, "contact_email": true,
	"default_seo_title": true, "default_seo_description": true,
}

// PATCH /admin/site — partial update of the singleton row (API-DD §5.8 field list). Omitted keys keep
// their value; null clears an optional field. Referenced images must be READY and not avatars, and are
// locked FOR SHARE against concurrent deletion (P3 rule).
func (s *Service) patchSite(w http.ResponseWriter, r *http.Request) error {
	var in map[string]json.RawMessage
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	for k := range in {
		if !siteFields[k] {
			return &httpx.APIError{Status: http.StatusBadRequest, Code: "INVALID_REQUEST", Message: "请求包含不能修改的字段。",
				Details: map[string]any{"fields": map[string][]string{k: {"NOT_ALLOWED"}}}}
		}
	}
	ctx := r.Context()
	var saved store.SiteSetting
	err := store.ExecTx(ctx, s.pool, func(q *store.Queries) error {
		cur, err := q.LockSiteSettings(ctx)
		if err != nil {
			return err
		}
		p := store.UpdateSiteSettingsParams{
			SiteTitle: cur.SiteTitle, SiteDescription: cur.SiteDescription, ProfileName: cur.ProfileName,
			ProfileBio: cur.ProfileBio, ProfileAvatarMediaID: cur.ProfileAvatarMediaID, LogoMediaID: cur.LogoMediaID,
			FaviconMediaID: cur.FaviconMediaID, ContactEmail: cur.ContactEmail, DefaultSeoTitle: cur.DefaultSeoTitle,
			DefaultSeoDescription: cur.DefaultSeoDescription,
		}
		fields := map[string][]string{}
		str := func(key string) (string, bool) {
			raw, ok := in[key]
			if !ok {
				return "", false
			}
			var v string
			if json.Unmarshal(raw, &v) != nil {
				fields[key] = []string{"INVALID"}
				return "", false
			}
			return v, true
		}
		optional := func(key string, max int, dst *pgtype.Text) {
			raw, ok := in[key]
			if !ok {
				return
			}
			var v *string
			if json.Unmarshal(raw, &v) != nil {
				fields[key] = []string{"INVALID"}
				return
			}
			if v == nil {
				*dst = pgtype.Text{}
				return
			}
			t := checkText(fields, key, *v, 0, max)
			*dst = pgtype.Text{String: t, Valid: t != ""}
		}
		mediaID := func(key string, dst *uuid.NullUUID) {
			raw, ok := in[key]
			if !ok {
				return
			}
			var v *string
			if json.Unmarshal(raw, &v) != nil {
				fields[key] = []string{"INVALID"}
				return
			}
			if v == nil {
				*dst = uuid.NullUUID{}
				return
			}
			id, err := uuid.Parse(*v)
			if err != nil {
				fields[key] = []string{"INVALID"}
				return
			}
			*dst = uuid.NullUUID{UUID: id, Valid: true}
		}
		if v, ok := str("site_title"); ok {
			p.SiteTitle = checkText(fields, "site_title", v, 1, 120)
		}
		if v, ok := str("site_description"); ok {
			p.SiteDescription = checkText(fields, "site_description", v, 0, 320)
		}
		if v, ok := str("profile_name"); ok {
			p.ProfileName = checkText(fields, "profile_name", v, 1, 80)
		}
		if v, ok := str("profile_bio"); ok {
			p.ProfileBio = checkText(fields, "profile_bio", v, 0, 240)
		}
		optional("default_seo_title", 200, &p.DefaultSeoTitle)
		optional("default_seo_description", 320, &p.DefaultSeoDescription)
		optional("contact_email", 254, &p.ContactEmail)
		if p.ContactEmail.Valid && !validEmail(p.ContactEmail.String) {
			fields["contact_email"] = []string{"INVALID_EMAIL"}
		}
		mediaID("profile_avatar_media_id", &p.ProfileAvatarMediaID)
		mediaID("logo_media_id", &p.LogoMediaID)
		mediaID("favicon_media_id", &p.FaviconMediaID)
		if len(fields) > 0 {
			return httpx.FieldErrors(fields)
		}
		var ids []uuid.UUID
		for _, m := range []uuid.NullUUID{p.ProfileAvatarMediaID, p.LogoMediaID, p.FaviconMediaID} {
			if m.Valid {
				ids = append(ids, m.UUID)
			}
		}
		if err := article.CheckMedia(ctx, q, ids); err != nil {
			return err
		}
		if saved, err = q.UpdateSiteSettings(ctx, p); err != nil {
			return err
		}
		changed := make([]string, 0, len(in))
		for k := range in {
			changed = append(changed, k)
		}
		return s.audit(r, q, "site.update", "settings", map[string]any{"fields": changed})
	})
	if err != nil {
		return err
	}
	httpx.WriteData(w, http.StatusOK, settingsDTO(saved))
	return nil
}

type linkInput struct {
	ID        *string `json:"id"`
	IconKey   string  `json:"icon_key"`
	Label     string  `json:"label"`
	LinkType  string  `json:"link_type"`
	URL       string  `json:"url"`
	IsEnabled *bool   `json:"is_enabled"`
}

func decodeLinks(w http.ResponseWriter, r *http.Request) ([]linkInput, error) {
	var in struct {
		Links *[]linkInput `json:"links"`
	}
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return nil, err
	}
	if in.Links == nil {
		return nil, httpx.FieldErrors(map[string][]string{"links": {"REQUIRED"}})
	}
	return *in.Links, nil
}

func linkID(fields map[string][]string, key string, raw *string) uuid.UUID {
	if raw == nil || *raw == "" {
		return uuid.New()
	}
	id, err := uuid.Parse(*raw)
	if err != nil {
		fields[key] = []string{"INVALID"}
	}
	return id
}

// PUT /admin/site/social-links — {"links":[{id?, icon_key, label, url, is_enabled}]} replaces the whole
// list; the array order becomes sort_order. Only https: and mailto: targets (API-DD §5.8).
func (s *Service) putSocialLinks(w http.ResponseWriter, r *http.Request) error {
	links, err := decodeLinks(w, r)
	if err != nil {
		return err
	}
	fields := map[string][]string{}
	if len(links) > maxSocialLinks {
		fields["links"] = []string{"TOO_MANY"}
	}
	rows := make([]store.InsertSocialLinkParams, 0, len(links))
	seen := map[uuid.UUID]bool{}
	for i, l := range links {
		k := fmt.Sprintf("links[%d]", i)
		id := linkID(fields, k+".id", l.ID)
		if seen[id] {
			fields[k+".id"] = []string{"DUPLICATE"}
		}
		seen[id] = true
		if !IconKeys[l.IconKey] {
			fields[k+".icon_key"] = []string{"INVALID"}
		}
		label := checkText(fields, k+".label", l.Label, 1, 80)
		u := strings.TrimSpace(l.URL)
		if !SafeExternalURL(u, true) {
			fields[k+".url"] = []string{"UNSAFE_URL"}
		}
		enabled := l.IsEnabled == nil || *l.IsEnabled
		rows = append(rows, store.InsertSocialLinkParams{ID: id, IconKey: l.IconKey, Label: label, Url: u, SortOrder: int32(i), IsEnabled: enabled})
	}
	if len(fields) > 0 {
		return httpx.FieldErrors(fields)
	}
	ctx := r.Context()
	err = store.ExecTx(ctx, s.pool, func(q *store.Queries) error {
		if _, err := q.LockSiteSettings(ctx); err != nil { // serialises concurrent replacements
			return err
		}
		if err := q.DeleteAllSocialLinks(ctx); err != nil {
			return err
		}
		for _, row := range rows {
			if err := q.InsertSocialLink(ctx, row); err != nil {
				return err
			}
		}
		return s.audit(r, q, "site.social_links", "social_links", map[string]any{"count": len(rows)})
	})
	if err != nil {
		return err
	}
	return s.adminSite(w, r)
}

// PUT /admin/site/navigation-links — {"links":[{id?, label, link_type, url, is_enabled}]}; INTERNAL links are
// public site paths (no /admin, /account, /auth, /api), EXTERNAL links are https only.
func (s *Service) putNavigationLinks(w http.ResponseWriter, r *http.Request) error {
	links, err := decodeLinks(w, r)
	if err != nil {
		return err
	}
	fields := map[string][]string{}
	if len(links) > maxNavLinks {
		fields["links"] = []string{"TOO_MANY"}
	}
	rows := make([]store.InsertNavigationLinkParams, 0, len(links))
	seen := map[uuid.UUID]bool{}
	for i, l := range links {
		k := fmt.Sprintf("links[%d]", i)
		id := linkID(fields, k+".id", l.ID)
		if seen[id] {
			fields[k+".id"] = []string{"DUPLICATE"}
		}
		seen[id] = true
		label := checkText(fields, k+".label", l.Label, 1, 60)
		u := strings.TrimSpace(l.URL)
		switch l.LinkType {
		case "INTERNAL":
			if !SafeInternalPath(u) {
				fields[k+".url"] = []string{"INVALID_PATH"}
			}
		case "EXTERNAL":
			if !SafeExternalURL(u, false) {
				fields[k+".url"] = []string{"UNSAFE_URL"}
			}
		default:
			fields[k+".link_type"] = []string{"INVALID"}
		}
		enabled := l.IsEnabled == nil || *l.IsEnabled
		rows = append(rows, store.InsertNavigationLinkParams{ID: id, Label: label, LinkType: l.LinkType, Url: u, SortOrder: int32(i), IsEnabled: enabled})
	}
	if len(fields) > 0 {
		return httpx.FieldErrors(fields)
	}
	ctx := r.Context()
	err = store.ExecTx(ctx, s.pool, func(q *store.Queries) error {
		if _, err := q.LockSiteSettings(ctx); err != nil {
			return err
		}
		if err := q.DeleteAllNavigationLinks(ctx); err != nil {
			return err
		}
		for _, row := range rows {
			if err := q.InsertNavigationLink(ctx, row); err != nil {
				return err
			}
		}
		return s.audit(r, q, "site.navigation_links", "navigation_links", map[string]any{"count": len(rows)})
	})
	if err != nil {
		return err
	}
	return s.adminSite(w, r)
}

// ---- about page (saving publishes at once: DD §8.4, UI/UX §7.8) ----

// GET /admin/pages/about — the editable Markdown; exists=false before the first save.
func (s *Service) adminAbout(w http.ResponseWriter, r *http.Request) error {
	p, err := s.q.GetAboutPageAdmin(r.Context())
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.WriteData(w, http.StatusOK, map[string]any{"exists": false, "title": "关于", "body_markdown": "", "updated_at": nil, "updated_by": nil})
		return nil
	}
	if err != nil {
		return err
	}
	httpx.WriteData(w, http.StatusOK, map[string]any{
		"exists": true, "title": p.Title, "body_markdown": p.BodyMarkdown, "updated_at": p.UpdatedAt.UTC(), "updated_by": p.UpdatedByNickname,
	})
	return nil
}

// PUT /admin/pages/about — {title, body_markdown}. The Markdown is stored as written and rendered by the
// same safe renderer as articles; its /media references are checked like article images and recorded in
// static_page_media so they become public and protected from deletion.
func (s *Service) putAbout(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Title        *string `json:"title"`
		BodyMarkdown *string `json:"body_markdown"`
	}
	if err := httpx.DecodeJSONLimit(w, r, &in, maxAboutBody+64<<10); err != nil {
		return err
	}
	fields := map[string][]string{}
	if in.Title == nil {
		fields["title"] = []string{"REQUIRED"}
	}
	if in.BodyMarkdown == nil {
		fields["body_markdown"] = []string{"REQUIRED"}
	}
	if len(fields) > 0 {
		return httpx.FieldErrors(fields)
	}
	title := checkText(fields, "title", *in.Title, 1, 160)
	body := strings.ReplaceAll(*in.BodyMarkdown, "\r\n", "\n")
	if len(body) > maxAboutBody {
		fields["body_markdown"] = []string{"TOO_LONG"}
	}
	if !utf8.ValidString(body) || strings.ContainsRune(body, 0) {
		fields["body_markdown"] = []string{"INVALID_CHARACTERS"}
	}
	if len(fields) > 0 {
		return httpx.FieldErrors(fields)
	}
	ctx, user := r.Context(), auth.User(r)
	refs := article.MediaRefs(body, s.siteHost)
	err := store.ExecTx(ctx, s.pool, func(q *store.Queries) error {
		if err := q.LockAboutPage(ctx); err != nil {
			return err
		}
		if err := article.CheckMedia(ctx, q, refs); err != nil {
			return err
		}
		if _, err := q.UpsertAboutPage(ctx, store.UpsertAboutPageParams{
			Title: title, BodyMarkdown: body, BodyPlainText: article.PlainText(body), UpdatedBy: user.ID,
		}); err != nil {
			return err
		}
		if err := q.DeleteAboutPageMedia(ctx); err != nil {
			return err
		}
		if len(refs) > 0 {
			if err := q.AddAboutPageMedia(ctx, refs); err != nil {
				return err
			}
		}
		return s.audit(r, q, "page.update", "about", map[string]any{"media_refs": len(refs), "bytes": len(body)})
	})
	if err != nil {
		return err
	}
	return s.adminAbout(w, r)
}
