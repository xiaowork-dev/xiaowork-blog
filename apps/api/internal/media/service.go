package media

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/xiaowork-dev/xiaowork-blog/apps/api/internal/auth"
	"github.com/xiaowork-dev/xiaowork-blog/apps/api/internal/platform/httpx"
	"github.com/xiaowork-dev/xiaowork-blog/apps/api/internal/store"
)

// Options configure limits and the media root.
type Options struct {
	Root   string
	Media  Limits // ARTICLE / SITE / PAGE uploads (admin)
	Avatar Limits // AVATAR uploads (any signed-in user, own profile only)
	// MinFreeBytes is the free space kept in reserve on the media volume; uploads are refused below it.
	MinFreeBytes int64
}

// Service implements the media endpoints of API-DD §4.4 / §5.7.
type Service struct {
	pool    *pgxpool.Pool
	q       *store.Queries
	auth    *auth.Service
	storage *Storage
	opts    Options
	logger  *slog.Logger
	sem     chan struct{} // bounds concurrent decoding (memory)
}

func NewService(pool *pgxpool.Pool, authService *auth.Service, storage *Storage, opts Options, logger *slog.Logger) *Service {
	return &Service{
		pool: pool, q: store.New(pool), auth: authService, storage: storage, opts: opts, logger: logger,
		sem: make(chan struct{}, 2),
	}
}

var (
	errInUse = func(details map[string]any) error {
		return &httpx.APIError{Status: http.StatusConflict, Code: "RESOURCE_IN_USE",
			Message: "该图片仍被文章、头像或站点设置引用，不能删除。", Details: details}
	}
	errStorageFull = httpx.NewError(http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "服务器存储空间不足，请联系管理员。")
	errTypeRefused = httpx.NewError(http.StatusUnsupportedMediaType, "UNSUPPORTED_MEDIA_TYPE",
		"仅支持 JPG、PNG、WebP、GIF 图片，且扩展名必须与实际格式一致。")
)

// ---- routes ----

// AdminRoutes mounts the library endpoints; the caller has already applied RequireAdmin.
func (s *Service) AdminRoutes(r chi.Router) {
	r.Get("/media", s.handle(s.list))
	r.Post("/media", s.handle(s.uploadAdmin))
	r.Delete("/media/{id}", s.handle(s.deleteMedia))
}

// AvatarRoute is POST /me/avatar (permission S).
func (s *Service) AvatarRoute(r chi.Router) {
	r.With(s.auth.RequireUser).Post("/me/avatar", s.handle(s.uploadAvatar))
}

// FileRoutes mounts GET/HEAD /media/{id}/file outside /api/v1 (API-DD §2).
func (s *Service) FileRoutes(r chi.Router) {
	r.Get("/media/{id}/file", s.handle(s.serveFile))
	r.Head("/media/{id}/file", s.handle(s.serveFile))
}

func (s *Service) handle(fn func(http.ResponseWriter, *http.Request) error) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := fn(w, r); err != nil {
			httpx.WriteError(w, r, err)
		}
	}
}

func mediaURL(id uuid.UUID) string { return "/media/" + id.String() + "/file" }

func mediaDTO(m store.MediaAsset) map[string]any {
	return map[string]any{
		"id": m.ID, "url": mediaURL(m.ID), "thumbnail_url": mediaURL(m.ID) + "?variant=thumb",
		"original_filename": m.OriginalFilename, "mime_type": m.MimeType, "size_bytes": m.SizeBytes,
		"width": m.Width, "height": m.Height, "usage_kind": m.UsageKind, "created_at": m.CreatedAt.UTC(),
		// Freshly uploaded media is private until a published article / site setting / avatar uses it.
		"visibility": "PRIVATE_UNTIL_REFERENCED",
	}
}

// ---- upload ----

type upload struct {
	tempPath string
	filename string
	fields   map[string]string
}

// receive streams the multipart body: one "file" part written straight to a temp file (never buffered in
// memory, size enforced while copying) plus small text fields.
func (s *Service) receive(w http.ResponseWriter, r *http.Request, lim Limits) (*upload, error) {
	if mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type")); err != nil || mt != "multipart/form-data" {
		return nil, httpx.NewError(http.StatusUnsupportedMediaType, "UNSUPPORTED_MEDIA_TYPE", "请使用 multipart/form-data 上传。")
	}
	if free, ok := freeBytes(s.opts.Root); ok && free < s.opts.MinFreeBytes+3*lim.MaxBytes {
		s.logger.Error("media volume nearly full", "free_bytes", free)
		return nil, errStorageFull
	}
	r.Body = http.MaxBytesReader(w, r.Body, lim.MaxBytes+1<<20) // file limit + room for multipart framing
	mr, err := r.MultipartReader()
	if err != nil {
		return nil, httpx.ErrInvalidRequest
	}
	up := &upload{fields: map[string]string{}}
	fail := func(err error) (*upload, error) {
		if up.tempPath != "" {
			_ = os.Remove(up.tempPath)
		}
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			return nil, httpx.ErrPayloadTooLarge
		}
		return nil, err
	}
	for {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fail(err)
		}
		switch name := part.FormName(); {
		case name == "file" && up.tempPath == "":
			tmp, err := s.storage.newTemp("upload-*.part")
			if err != nil {
				return fail(err)
			}
			up.tempPath, up.filename = tmp.Name(), part.FileName()
			n, err := io.Copy(tmp, io.LimitReader(part, lim.MaxBytes+1))
			if err == nil {
				err = tmp.Sync()
			}
			if cerr := tmp.Close(); err == nil {
				err = cerr
			}
			if err != nil {
				return fail(err)
			}
			if n > lim.MaxBytes {
				return fail(httpx.ErrPayloadTooLarge)
			}
			if n == 0 {
				return fail(httpx.FieldErrors(map[string][]string{"file": {"EMPTY"}}))
			}
		case name != "file" && len(up.fields) < 4:
			v, err := io.ReadAll(io.LimitReader(part, 65))
			if err != nil || len(v) > 64 {
				return fail(httpx.ErrInvalidRequest)
			}
			up.fields[name] = string(v)
		default:
			return fail(httpx.ErrInvalidRequest) // a second file or too many fields
		}
	}
	if up.tempPath == "" {
		return nil, httpx.FieldErrors(map[string][]string{"file": {"REQUIRED"}})
	}
	return up, nil
}

// stored is a validated upload whose files are already in their final place.
type stored struct {
	params   store.CreateMediaAssetParams
	thumbKey string
}

// cleanup removes committed files after the database refused the record (compensation, DD §8.3).
func (s *Service) cleanup(st *stored, logger *slog.Logger) {
	for _, key := range []string{st.params.StorageKey, st.thumbKey} {
		if err := s.storage.delete(key); err != nil {
			logger.Error("orphan media file left after failed upload", "key", key, "error", err.Error())
		}
	}
}

// process validates the temp file, writes the thumbnail and moves both into place. The database row is
// written afterwards by the caller; on any failure nothing is left behind.
func (s *Service) process(ctx context.Context, up *upload, kind string, uploader uuid.UUID, lim Limits) (*stored, error) {
	defer os.Remove(up.tempPath) // no-op once renamed
	select {
	case s.sem <- struct{}{}:
		defer func() { <-s.sem }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	f, err := os.Open(up.tempPath)
	if err != nil {
		return nil, err
	}
	in, err := inspect(f, up.filename, lim)
	size, _ := f.Seek(0, io.SeekEnd)
	_ = f.Close()
	switch {
	case errors.Is(err, errUnsupported):
		return nil, errTypeRefused
	case errors.Is(err, errTooManyPixel):
		return nil, httpx.FieldErrors(map[string][]string{"file": {"DIMENSIONS_TOO_LARGE"}})
	case errors.Is(err, errUndecodable):
		return nil, httpx.FieldErrors(map[string][]string{"file": {"DECODE_FAILED"}})
	case err != nil:
		return nil, err
	}
	// Privacy: drop EXIF/GPS, XMP, IPTC and comments without re-encoding (ADR-0008), then make sure the
	// cleaned file is still the same image before it is stored.
	if in.orientation, err = stripMetadata(up.tempPath, in.format.mime); err != nil {
		return nil, httpx.FieldErrors(map[string][]string{"file": {"DECODE_FAILED"}})
	}
	if size, err = verifySameImage(up.tempPath, in); err != nil {
		return nil, httpx.FieldErrors(map[string][]string{"file": {"DECODE_FAILED"}})
	}
	digest, err := fileSHA256(up.tempPath)
	if err != nil {
		return nil, err
	}

	id := uuid.New()
	st := &stored{params: store.CreateMediaAssetParams{
		ID: id, UploadedBy: uploader, UsageKind: kind, StorageKey: originalKey(id, in.format.ext),
		OriginalFilename: cleanFilename(up.filename, in.format.ext), MimeType: in.format.mime, SizeBytes: size,
		Width: int32(in.width), Height: int32(in.height), Sha256: digest,
	}}

	var thumbTemp string
	if tf, err := s.storage.newTemp("thumb-*.part"); err != nil {
		return nil, err
	} else {
		thumbTemp = tf.Name()
		defer os.Remove(thumbTemp)
		made, err := writeThumbnail(in, tf)
		if err == nil {
			err = tf.Sync()
		}
		if cerr := tf.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return nil, err
		}
		if made {
			st.thumbKey = thumbKey(st.params.StorageKey, in.format.mime)
		}
	}

	if err := s.storage.commit(up.tempPath, st.params.StorageKey); err != nil {
		return nil, fmt.Errorf("store original: %w", err)
	}
	if st.thumbKey != "" {
		if err := s.storage.commit(thumbTemp, st.thumbKey); err != nil {
			_ = s.storage.delete(st.params.StorageKey)
			return nil, fmt.Errorf("store thumbnail: %w", err)
		}
	}
	return st, nil
}

// cleanFilename keeps only a display name: no directories, control characters or overlong text.
func cleanFilename(name, ext string) string {
	if i := strings.LastIndexAny(name, `/\`); i >= 0 {
		name = name[i+1:]
	}
	name = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return -1
		}
		return r
	}, name)
	name = strings.TrimSpace(name)
	for len(name) > 255 {
		_, size := utf8.DecodeLastRuneInString(name)
		name = name[:len(name)-size]
	}
	if name == "" || name == "." || name == ".." {
		name = "image." + ext
	}
	return name
}

// extendDeadlines gives uploads and downloads more time than ordinary JSON requests.
func extendDeadlines(w http.ResponseWriter, d time.Duration) {
	rc := http.NewResponseController(w)
	_ = rc.SetReadDeadline(time.Now().Add(d))
	_ = rc.SetWriteDeadline(time.Now().Add(d))
}

// POST /admin/media — ARTICLE / SITE / PAGE images (permission A).
func (s *Service) uploadAdmin(w http.ResponseWriter, r *http.Request) error {
	extendDeadlines(w, 2*time.Minute)
	ctx, user, logger := r.Context(), auth.User(r), httpx.LoggerFrom(r.Context())
	if err := s.auth.RateLimit(ctx, "MEDIA_UPLOAD", 120, time.Hour, user.ID.String()); err != nil {
		return err
	}
	up, err := s.receive(w, r, s.opts.Media)
	if err != nil {
		return err
	}
	defer os.Remove(up.tempPath)
	kind := strings.ToUpper(strings.TrimSpace(up.fields["usage_kind"]))
	if kind == "" {
		kind = "ARTICLE"
	}
	if kind != "ARTICLE" && kind != "SITE" && kind != "PAGE" {
		return httpx.FieldErrors(map[string][]string{"usage_kind": {"INVALID"}}) // avatars use /me/avatar
	}
	st, err := s.process(ctx, up, kind, user.ID, s.opts.Media)
	if err != nil {
		return err
	}
	m, err := s.q.CreateMediaAsset(context.WithoutCancel(ctx), st.params)
	if err != nil {
		s.cleanup(st, logger)
		return err
	}
	logger.Info("media uploaded", "media_id", m.ID, "usage_kind", kind, "size_bytes", m.SizeBytes)
	httpx.WriteData(w, http.StatusCreated, mediaDTO(m))
	return nil
}

// POST /me/avatar — a user's own avatar only (permission S). The profile switches in the same transaction
// as the media row; the previous avatar is deleted afterwards if nothing else references it.
func (s *Service) uploadAvatar(w http.ResponseWriter, r *http.Request) error {
	extendDeadlines(w, 2*time.Minute)
	ctx, user, logger := r.Context(), auth.User(r), httpx.LoggerFrom(r.Context())
	if err := s.auth.RateLimit(ctx, "AVATAR_UPLOAD", 10, time.Hour, user.ID.String()); err != nil {
		return err // API-DD §7: 10 per hour per user
	}
	up, err := s.receive(w, r, s.opts.Avatar)
	if err != nil {
		return err
	}
	defer os.Remove(up.tempPath)
	if len(up.fields) > 0 {
		return httpx.ErrInvalidRequest // the target is always the caller's own profile
	}
	st, err := s.process(ctx, up, "AVATAR", user.ID, s.opts.Avatar)
	if err != nil {
		return err
	}
	var previous uuid.NullUUID
	dbCtx := context.WithoutCancel(ctx)
	err = store.ExecTx(dbCtx, s.pool, func(q *store.Queries) error {
		if _, err := q.CreateMediaAsset(dbCtx, st.params); err != nil {
			return err
		}
		old, err := q.GetUserAvatarMedia(dbCtx, user.ID)
		if err != nil {
			return err
		}
		previous = old
		return q.SetUserAvatar(dbCtx, store.SetUserAvatarParams{UserID: user.ID, AvatarMediaID: uuid.NullUUID{UUID: st.params.ID, Valid: true}})
	})
	if err != nil {
		s.cleanup(st, logger)
		return err
	}
	if previous.Valid {
		if _, err := s.deleteUnreferenced(dbCtx, previous.UUID); err != nil && !isInUse(err) {
			logger.Warn("old avatar cleanup deferred", "media_id", previous.UUID, "error", err.Error())
		}
	}
	logger.Info("avatar updated", "user_id", user.ID, "media_id", st.params.ID)
	httpx.WriteData(w, http.StatusOK, map[string]any{"avatar_url": mediaURL(st.params.ID)})
	return nil
}

// ---- delete (DD T06 state machine: READY → PENDING_DELETE → file removed → DELETED) ----

func isInUse(err error) bool {
	var apiErr *httpx.APIError
	return errors.As(err, &apiErr) && apiErr.Code == "RESOURCE_IN_USE"
}

func referenceDetails(c store.CountMediaReferencesRow) (map[string]any, int64) {
	total := c.DraftBodyRefs + c.DraftCoverRefs + c.PublicationBodyRefs + c.PublicationCoverRefs + c.AvatarRefs + c.SiteRefs + c.PageRefs
	// Counts only; no titles or user data (API-DD §5.7 "不包含私人内容的引用数量").
	return map[string]any{"references": map[string]int64{
		"article_drafts": c.DraftBodyRefs + c.DraftCoverRefs, "published_articles": c.PublicationBodyRefs + c.PublicationCoverRefs,
		"avatars": c.AvatarRefs, "site_settings": c.SiteRefs, "pages": c.PageRefs,
	}, "total": total}, total
}

// deleteUnreferenced runs both phases. It returns (true, nil) once the media is gone or queued for the
// janitor, and a RESOURCE_IN_USE error when any reference exists.
func (s *Service) deleteUnreferenced(ctx context.Context, id uuid.UUID) (bool, error) {
	err := store.ExecTx(ctx, s.pool, func(q *store.Queries) error {
		m, err := q.LockMediaAsset(ctx, id)
		if errors.Is(err, pgx.ErrNoRows) {
			return httpx.ErrNotFound
		}
		if err != nil {
			return err
		}
		switch m.State {
		case "DELETED":
			return httpx.ErrNotFound
		case "PENDING_DELETE":
			return nil // already accepted; finish below
		}
		refs, err := q.CountMediaReferences(ctx, id)
		if err != nil {
			return err
		}
		if details, total := referenceDetails(refs); total > 0 {
			return errInUse(details)
		}
		_, err = q.SetMediaPendingDelete(ctx, id)
		return err
	})
	if err != nil {
		return false, err
	}
	return true, s.finalizeDelete(ctx, id)
}

// finalizeDelete removes the files of a PENDING_DELETE asset while holding its row lock, so no new
// reference can be inserted concurrently (references take FOR KEY SHARE on the media row). If a reference
// slipped in between the phases the asset is restored to READY. A failure leaves the row PENDING_DELETE
// (never served) and the janitor retries.
func (s *Service) finalizeDelete(ctx context.Context, id uuid.UUID) error {
	restored := false
	err := store.ExecTx(ctx, s.pool, func(q *store.Queries) error {
		m, err := q.LockMediaAsset(ctx, id)
		if err != nil {
			return err
		}
		if m.State != "PENDING_DELETE" {
			return nil
		}
		refs, err := q.CountMediaReferences(ctx, id)
		if err != nil {
			return err
		}
		if _, total := referenceDetails(refs); total > 0 {
			restored = true
			_, err := q.RevertMediaReady(ctx, id)
			return err
		}
		if err := s.storage.delete(thumbKey(m.StorageKey, m.MimeType)); err != nil {
			return fmt.Errorf("remove thumbnail: %w", err)
		}
		if err := s.storage.delete(m.StorageKey); err != nil {
			return fmt.Errorf("remove original: %w", err)
		}
		_, err = q.MarkMediaDeleted(ctx, id)
		return err
	})
	if err != nil {
		return err
	}
	if restored {
		s.logger.Warn("media delete aborted: a reference was added concurrently", "media_id", id)
		refs, _ := s.q.CountMediaReferences(ctx, id)
		details, _ := referenceDetails(refs)
		return errInUse(details)
	}
	return nil
}

// DELETE /admin/media/{id}
func (s *Service) deleteMedia(w http.ResponseWriter, r *http.Request) error {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		return httpx.ErrNotFound
	}
	ok, err := s.deleteUnreferenced(r.Context(), id)
	if !ok {
		return err
	}
	logger := httpx.LoggerFrom(r.Context())
	if err != nil {
		if isInUse(err) {
			return err
		}
		// Accepted: the asset is PENDING_DELETE and no longer served; the janitor finishes the removal.
		logger.Error("media file removal failed; will retry", "media_id", id, "error", err.Error())
	} else {
		logger.Info("media deleted", "media_id", id)
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// ---- library listing ----

func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

// GET /admin/media?page=&page_size=&q=&usage_kind= (one kind or a comma-separated list)
func (s *Service) list(w http.ResponseWriter, r *http.Request) error {
	qs := r.URL.Query()
	page, size := 1, 20
	if v := qs.Get("page"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 100000 {
			return httpx.FieldErrors(map[string][]string{"page": {"INVALID"}})
		}
		page = n
	}
	if v := qs.Get("page_size"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 100 {
			return httpx.FieldErrors(map[string][]string{"page_size": {"INVALID"}})
		}
		size = n
	}
	q := strings.TrimSpace(qs.Get("q"))
	if utf8.RuneCountInString(q) > 100 {
		return httpx.FieldErrors(map[string][]string{"q": {"TOO_LONG"}})
	}
	kind := strings.ToUpper(qs.Get("usage_kind"))
	if kind != "" {
		for _, k := range strings.Split(kind, ",") {
			if k != "ARTICLE" && k != "SITE" && k != "PAGE" && k != "AVATAR" {
				return httpx.FieldErrors(map[string][]string{"usage_kind": {"INVALID"}})
			}
		}
	}
	rows, err := s.q.ListMediaLibrary(r.Context(), store.ListMediaLibraryParams{
		Pattern: "%" + escapeLike(q) + "%", UsageKind: kind, PageLimit: int32(size), PageOffset: int32((page - 1) * size),
	})
	if err != nil {
		return err
	}
	items := make([]map[string]any, 0, len(rows))
	var total int64
	for _, m := range rows {
		total = m.TotalCount
		items = append(items, map[string]any{
			"id": m.ID, "url": mediaURL(m.ID), "thumbnail_url": mediaURL(m.ID) + "?variant=thumb",
			"original_filename": m.OriginalFilename, "mime_type": m.MimeType, "size_bytes": m.SizeBytes,
			"width": m.Width, "height": m.Height, "usage_kind": m.UsageKind, "created_at": m.CreatedAt.UTC(),
			"uploaded_by":     map[string]any{"id": m.UploadedBy, "nickname": m.UploaderNickname},
			"reference_count": m.ReferenceCount,
		})
	}
	if len(rows) == 0 && page > 1 {
		// The window function yields no total on an empty page; count separately is unnecessary for UI.
		total = -1
	}
	meta := map[string]any{"page": page, "page_size": size}
	if total >= 0 {
		meta["total"] = total
		meta["total_pages"] = (total + int64(size) - 1) / int64(size)
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"data": items, "meta": meta})
	return nil
}

// ---- file delivery ----

// GET|HEAD /media/{id}/file[?variant=thumb] (API-DD §5.7). Anything the caller may not see — unknown id,
// draft-only image, deleted file — is the same 404, so existence is never disclosed.
func (s *Service) serveFile(w http.ResponseWriter, r *http.Request) error {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		return httpx.ErrNotFound
	}
	variant := r.URL.Query().Get("variant")
	if variant != "" && variant != "thumb" {
		return httpx.ErrNotFound
	}
	ctx := r.Context()
	m, err := s.q.GetMediaAsset(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && m.State != "READY" {
		return httpx.ErrNotFound
	}
	if err != nil {
		return err
	}
	public, err := s.q.IsMediaPubliclyVisible(ctx, id)
	if err != nil {
		return err
	}
	if !public {
		u := auth.User(r)
		allowed := u != nil && u.Status == "ACTIVE" &&
			(u.Role == "ADMIN" && u.EmailVerified || m.UsageKind == "AVATAR" && m.UploadedBy == u.ID)
		if !allowed {
			return httpx.ErrNotFound
		}
	}

	key, mimeType, etag := m.StorageKey, m.MimeType, fmt.Sprintf(`"%x"`, m.Sha256)
	if variant == "thumb" {
		if tk := thumbKey(m.StorageKey, m.MimeType); tk != "" && s.storage.exists(tk) {
			key, mimeType, etag = tk, thumbMime(formats[m.MimeType].thumbExt), fmt.Sprintf(`"%x-thumb"`, m.Sha256)
		}
	}
	f, err := s.storage.open(key)
	if errors.Is(err, os.ErrNotExist) {
		// DB says READY but the file is gone (e.g. volume not restored): report, never 500 to visitors.
		httpx.LoggerFrom(ctx).Error("media file missing on disk", "media_id", id, "key", key)
		return httpx.ErrNotFound
	}
	if err != nil {
		return err
	}
	defer f.Close()

	extendDeadlines(w, 2*time.Minute)
	h := w.Header()
	h.Set("Content-Type", mimeType)
	h.Set("Content-Disposition", "inline")
	h.Set("Content-Security-Policy", "default-src 'none'; sandbox")
	h.Set("ETag", etag)
	if public {
		h.Set("Cache-Control", "public, max-age=300") // short: withdrawing an article must take effect soon
	} else {
		h.Set("Cache-Control", "private, no-store")
	}
	http.ServeContent(w, r, "", m.CreatedAt, f)
	return nil
}

// ---- maintenance ----

// Report compares the database with the media volume (DD §7.5, §11.2). Orphans are only reported,
// never deleted automatically.
type Report struct {
	MissingFiles []uuid.UUID // READY/PENDING rows whose original is missing
	OrphanFiles  []string    // files under originals/ without a live row
}

func (s *Service) CheckConsistency(ctx context.Context) (Report, error) {
	var rep Report
	rows, err := s.q.ListStoredMedia(ctx)
	if err != nil {
		return rep, err
	}
	known := make(map[string]bool, len(rows))
	for _, row := range rows {
		known[row.StorageKey] = true
		if !s.storage.exists(row.StorageKey) {
			rep.MissingFiles = append(rep.MissingFiles, row.ID)
		}
	}
	keys, err := s.storage.listOriginals()
	if err != nil {
		return rep, err
	}
	for _, k := range keys {
		if !known[k] {
			rep.OrphanFiles = append(rep.OrphanFiles, k)
		}
	}
	return rep, nil
}

// Maintain finishes interrupted deletions, removes stale temp files and logs the consistency report.
func (s *Service) Maintain(ctx context.Context) {
	if n, err := s.storage.cleanTemp(time.Hour); err != nil {
		s.logger.Warn("media temp cleanup failed", "error", err.Error())
	} else if n > 0 {
		s.logger.Info("media temp cleanup", "removed", n)
	}
	pending, err := s.q.ListPendingDeleteMedia(ctx)
	if err != nil {
		s.logger.Warn("listing pending media deletions failed", "error", err.Error())
	}
	for _, id := range pending {
		if err := s.finalizeDelete(ctx, id); err != nil && !isInUse(err) {
			s.logger.Warn("pending media deletion still failing", "media_id", id, "error", err.Error())
		}
	}
	rep, err := s.CheckConsistency(ctx)
	if err != nil {
		s.logger.Warn("media consistency check failed", "error", err.Error())
		return
	}
	level := slog.LevelInfo
	if len(rep.MissingFiles) > 0 || len(rep.OrphanFiles) > 0 {
		level = slog.LevelWarn
	}
	s.logger.Log(ctx, level, "media consistency report", "missing_files", len(rep.MissingFiles), "orphan_files", len(rep.OrphanFiles))
}

// RunJanitor calls Maintain periodically until ctx is done.
func (s *Service) RunJanitor(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		s.Maintain(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
