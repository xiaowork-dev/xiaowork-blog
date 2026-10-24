package media_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"net/http"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/xiaowork-dev/xiaowork-blog/apps/api/internal/media"
)

// Normal uploads of all four formats; random storage keys; thumbnails for large stills only; admin read,
// HEAD, thumbnail variant; anonymous visitors cannot read unreferenced media.
func TestUploadAndServeAllFormats(t *testing.T) {
	h := newHarness(t)
	admin := h.user("ADMIN", true)
	cases := []struct {
		name, mime string
		data       []byte
		w, h       float64
		thumb      bool
	}{
		{"photo.PNG", "image/png", pngImage(1200, 800), 1200, 800, true},
		{"shot.jpeg", "image/jpeg", jpegImage(640, 480), 640, 480, true},
		{"anim.gif", "image/gif", animatedGIF(3, 64), 64, 64, false},
		{"tiny.webp", "image/webp", tinyWebP, 1, 1, false},
	}
	for _, c := range cases {
		r := admin.uploadAdmin(c.name, c.data)
		d := r.data()
		if r.Status != 201 || d["mime_type"] != c.mime || d["width"] != c.w || d["height"] != c.h ||
			d["visibility"] != "PRIVATE_UNTIL_REFERENCED" || d["usage_kind"] != "ARTICLE" || d["original_filename"] != c.name {
			t.Fatalf("%s: %d %s", c.name, r.Status, r.Body)
		}
		id := d["id"].(string)
		state, key := h.mediaRow(id)
		if state != "READY" || !strings.HasPrefix(key, "originals/"+id[:2]+"/"+id+".") || strings.Contains(key, c.name) {
			t.Fatalf("%s stored as %s (%s)", c.name, key, state)
		}
		thumbs := 0
		for _, f := range h.files("thumbnails") {
			if strings.Contains(f, id) {
				thumbs++
			}
		}
		if (thumbs == 1) != c.thumb {
			t.Fatalf("%s: thumbnails=%d want %v", c.name, thumbs, c.thumb)
		}

		got := admin.get(d["url"].(string))
		if got.Status != 200 || !bytes.Equal(got.Body, c.data) || got.Header.Get("Content-Type") != c.mime ||
			got.Header.Get("Cache-Control") != "private, no-store" || got.Header.Get("X-Content-Type-Options") != "nosniff" {
			t.Fatalf("%s admin read: %d %v", c.name, got.Status, got.Header)
		}
		head := admin.do(http.MethodHead, d["url"].(string), nil, "")
		if head.Status != 200 || len(head.Body) != 0 || head.Header.Get("Content-Length") != itoa(len(c.data)) {
			t.Fatalf("%s HEAD: %d %v", c.name, head.Status, head.Header)
		}
		thumb := admin.get(d["thumbnail_url"].(string))
		if thumb.Status != 200 || c.thumb && len(thumb.Body) >= len(c.data) {
			t.Fatalf("%s thumb: %d len=%d", c.name, thumb.Status, len(thumb.Body))
		}
		if c.mime == "image/gif" && !bytes.Equal(thumb.Body, c.data) {
			t.Fatal("GIF variant must be the original animation")
		}
		expect(t, h.anon().get(d["url"].(string)), 404, "NOT_FOUND")
	}
	noTempFiles(t, h)

	// Conditional request: the ETag lets browsers revalidate cheaply.
	r := admin.uploadAdmin("etag.png", pngImage(10, 10))
	first := admin.get(r.data()["url"].(string))
	req, _ := http.NewRequest(http.MethodGet, h.srv.URL+r.data()["url"].(string), nil)
	req.Header.Set("If-None-Match", first.Header.Get("ETag"))
	resp, err := admin.http.Do(req)
	if err != nil || resp.StatusCode != http.StatusNotModified {
		t.Fatalf("If-None-Match: %v %v", resp.StatusCode, err)
	}
	resp.Body.Close()
}

func itoa(n int) string { return strconv.Itoa(n) }

func mustTime(t *testing.T) time.Time {
	t.Helper()
	return time.Now().Add(-2 * time.Hour)
}

// Oversized, disguised, undecodable, bombs, traversal names and malformed requests.
func TestRejectsMaliciousAndInvalidUploads(t *testing.T) {
	h := newHarness(t)
	admin := h.user("ADMIN", true)

	expect(t, admin.uploadAdmin("big.png", bytes.Repeat([]byte{0}, 2<<20+10)), 413, "PAYLOAD_TOO_LARGE")
	expect(t, admin.uploadAdmin("page.png", []byte("<!doctype html><script>alert(1)</script>")), 415, "UNSUPPORTED_MEDIA_TYPE")
	expect(t, admin.uploadAdmin("logo.svg", []byte(`<svg xmlns="http://www.w3.org/2000/svg" onload="alert(1)"/>`)), 415, "UNSUPPORTED_MEDIA_TYPE")
	expect(t, admin.uploadAdmin("photo.png", jpegImage(32, 32)), 415, "UNSUPPORTED_MEDIA_TYPE") // JPEG bytes, .png name
	expect(t, admin.uploadAdmin("noext", pngImage(8, 8)), 415, "UNSUPPORTED_MEDIA_TYPE")
	expect(t, admin.uploadAdmin("run.php.gif", []byte("GIF89a<?php system($_GET['c']); ?>")), 422, "VALIDATION_FAILED")
	expect(t, admin.uploadAdmin("bomb.png", pngBomb(50000, 50000)), 422, "VALIDATION_FAILED") // 2.5 GP claimed
	expect(t, admin.uploadAdmin("wide.png", pngBomb(20000, 1)), 422, "VALIDATION_FAILED")     // side > 16384
	full := pngImage(300, 300)
	expect(t, admin.uploadAdmin("cut.png", full[:len(full)/2]), 422, "VALIDATION_FAILED")     // header ok, data truncated
	expect(t, admin.uploadAdmin("frames.gif", animatedGIF(501, 2)), 422, "VALIDATION_FAILED") // frame bomb
	expect(t, admin.uploadAdmin("empty.png", nil), 422, "VALIDATION_FAILED")
	expect(t, admin.upload("/api/v1/admin/media", "a.png", pngImage(8, 8), map[string]string{"usage_kind": "AVATAR"}), 422, "VALIDATION_FAILED")
	expect(t, admin.do(http.MethodPost, "/api/v1/admin/media", strings.NewReader(`{"file":"x"}`), "application/json"), 415, "UNSUPPORTED_MEDIA_TYPE")

	// Path traversal in the client file name only affects the display name.
	r := admin.uploadAdmin(`../../..\..\etc/evil.png`, pngImage(16, 16))
	if r.Status != 201 || r.data()["original_filename"] != "evil.png" {
		t.Fatalf("traversal name: %d %s", r.Status, r.Body)
	}
	if _, err := os.Stat(h.root + "/../etc/evil.png"); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("file written outside the media root")
	}
	// Only the one legitimate file exists; every rejected upload was cleaned up.
	if got := h.files("originals"); len(got) != 1 {
		t.Fatalf("originals after rejections: %v", got)
	}
	noTempFiles(t, h)

	// Malformed ids and unknown variants are plain 404s.
	expect(t, admin.get("/media/not-a-uuid/file"), 404, "NOT_FOUND")
	expect(t, admin.get(r.data()["url"].(string)+"?variant=../../etc/passwd"), 404, "NOT_FOUND")
}

// Permission matrix: only admins manage library media; users only their own avatar.
func TestPermissions(t *testing.T) {
	h := newHarness(t)
	admin := h.user("ADMIN", true)
	alice := h.user("USER", true)
	bob := h.user("USER", true)

	expect(t, h.anon().uploadAdmin("x.png", pngImage(8, 8)), 401, "AUTH_REQUIRED") // valid anonymous CSRF, no session
	expect(t, alice.uploadAdmin("x.png", pngImage(8, 8)), 403, "FORBIDDEN")
	expect(t, alice.get("/api/v1/admin/media"), 403, "FORBIDDEN")
	img := admin.uploadAdmin("draft.png", pngImage(8, 8))
	expect(t, alice.do(http.MethodDelete, "/api/v1/admin/media/"+img.data()["id"].(string), nil, ""), 403, "FORBIDDEN")
	expect(t, alice.get(img.data()["url"].(string)), 404, "NOT_FOUND") // private draft image

	// Alice's avatar: her profile switches, the image becomes public (active user's avatar).
	r := alice.upload("/api/v1/me/avatar", "me.png", pngImage(64, 64), nil)
	if r.Status != 200 {
		t.Fatalf("avatar: %d %s", r.Status, r.Body)
	}
	avatarURL := r.data()["avatar_url"].(string)
	avatarID := strings.Split(avatarURL, "/")[2]
	var profileAvatar string
	_ = h.pool.QueryRow(context.Background(), `SELECT avatar_media_id::text FROM user_profiles WHERE user_id = $1`, alice.id).Scan(&profileAvatar)
	if profileAvatar != avatarID {
		t.Fatalf("profile avatar = %s, want %s", profileAvatar, avatarID)
	}
	if got := h.anon().get(avatarURL); got.Status != 200 || got.Header.Get("Cache-Control") != "public, max-age=300" {
		t.Fatalf("public avatar: %d %v", got.Status, got.Header)
	}
	// Bob can neither delete it nor redirect /me/avatar at Alice; admins cannot delete it while in use.
	expect(t, bob.do(http.MethodDelete, "/api/v1/admin/media/"+avatarID, nil, ""), 403, "FORBIDDEN")
	expect(t, bob.upload("/api/v1/me/avatar", "x.png", pngImage(8, 8), map[string]string{"user_id": alice.id.String()}), 400, "INVALID_REQUEST")
	del := admin.do(http.MethodDelete, "/api/v1/admin/media/"+avatarID, nil, "")
	expect(t, del, 409, "RESOURCE_IN_USE")
	if refs := del.JSON["error"].(map[string]any)["details"].(map[string]any)["references"].(map[string]any); refs["avatars"] != float64(1) {
		t.Fatalf("reference details: %v", refs)
	}

	// Replacing the avatar deletes the previous, now unreferenced, file.
	r = alice.upload("/api/v1/me/avatar", "new.jpg", jpegImage(64, 64), nil)
	if r.Status != 200 {
		t.Fatal(string(r.Body))
	}
	if state, key := h.mediaRow(avatarID); state != "DELETED" || fileExists(h.root, key) {
		t.Fatalf("old avatar state=%s file present=%v", state, fileExists(h.root, key))
	}
	// Avatar-specific limits.
	expect(t, alice.upload("/api/v1/me/avatar", "huge.png", bytes.Repeat([]byte{1}, 300<<10), nil), 413, "PAYLOAD_TOO_LARGE")
	expect(t, alice.upload("/api/v1/me/avatar", "wide.png", pngBomb(2000, 1000), nil), 422, "VALIDATION_FAILED")
	expect(t, h.anon().upload("/api/v1/me/avatar", "x.png", pngImage(8, 8), nil), 401, "AUTH_REQUIRED")

	// A disabled user's avatar is no longer public.
	newID := strings.Split(r.data()["avatar_url"].(string), "/")[2]
	h.exec(`UPDATE users SET status = 'DISABLED' WHERE id = $1`, alice.id)
	expect(t, h.anon().get("/media/"+newID+"/file"), 404, "NOT_FOUND")
}

// Draft-only media stays private; publishing makes it public; withdrawing hides it again.
func TestDraftPrivatePublishedPublic(t *testing.T) {
	h := newHarness(t)
	admin := h.user("ADMIN", true)
	r := admin.uploadAdmin("cover.jpg", jpegImage(800, 600))
	id, url := r.data()["id"].(string), r.data()["url"].(string)
	article := uuid.New()
	h.exec(`INSERT INTO articles (id, author_id) VALUES ($1, $2)`, article, admin.id)
	h.exec(`INSERT INTO article_drafts (article_id, last_edited_by, cover_media_id) VALUES ($1, $2, $3)`, article, admin.id, id)
	h.exec(`INSERT INTO article_draft_media (article_id, media_id) VALUES ($1, $2)`, article, id)

	expect(t, h.anon().get(url), 404, "NOT_FOUND")
	expect(t, h.anon().get(url+"?variant=thumb"), 404, "NOT_FOUND")
	expect(t, h.user("USER", true).get(url), 404, "NOT_FOUND")
	if admin.get(url).Status != 200 {
		t.Fatal("admin cannot preview the draft image")
	}

	h.exec(`UPDATE articles SET status = 'PUBLISHED', first_published_at = now(), public_updated_at = now(),
		last_published_draft_revision = 1 WHERE id = $1`, article)
	h.exec(`INSERT INTO article_slugs (slug, article_id, is_current) VALUES ('media-post', $1, TRUE)`, article)
	h.exec(`INSERT INTO article_publications (article_id, publication_revision, source_draft_revision, title, summary,
		body_markdown, body_plain_text, slug, cover_media_id, comments_enabled, published_at)
		VALUES ($1, 1, 1, 't', '', 'b', 'b', 'media-post', $2, TRUE, now())`, article, id)
	pub := h.anon().get(url)
	if pub.Status != 200 || pub.Header.Get("Cache-Control") != "public, max-age=300" {
		t.Fatalf("published image: %d %v", pub.Status, pub.Header)
	}
	if th := h.anon().get(url + "?variant=thumb"); th.Status != 200 || th.Header.Get("Content-Type") != "image/jpeg" {
		t.Fatalf("published thumbnail: %d %v", th.Status, th.Header)
	}

	h.exec(`UPDATE articles SET status = 'WITHDRAWN', withdrawn_at = now() WHERE id = $1`, article)
	expect(t, h.anon().get(url), 404, "NOT_FOUND")
}

// Reference protection, two-phase delete, idempotency and the concurrent-reference race.
func TestDeleteProtectionAndRace(t *testing.T) {
	h := newHarness(t)
	admin := h.user("ADMIN", true)
	used := admin.uploadAdmin("used.png", pngImage(900, 600)).data()["id"].(string)
	article := uuid.New()
	h.exec(`INSERT INTO articles (id, author_id) VALUES ($1, $2)`, article, admin.id)
	h.exec(`INSERT INTO article_drafts (article_id, last_edited_by) VALUES ($1, $2)`, article, admin.id)
	h.exec(`INSERT INTO article_draft_media (article_id, media_id) VALUES ($1, $2)`, article, used)
	r := admin.do(http.MethodDelete, "/api/v1/admin/media/"+used, nil, "")
	expect(t, r, 409, "RESOURCE_IN_USE")
	if d := r.JSON["error"].(map[string]any)["details"].(map[string]any); d["total"] != float64(1) {
		t.Fatalf("details: %v", d)
	}
	if state, _ := h.mediaRow(used); state != "READY" {
		t.Fatal("referenced media changed state")
	}

	// Site settings references protect too.
	logo := admin.upload("/api/v1/admin/media", "logo.png", pngImage(32, 32), map[string]string{"usage_kind": "SITE"}).data()["id"].(string)
	h.exec(`UPDATE site_settings SET logo_media_id = $1`, logo)
	expect(t, admin.do(http.MethodDelete, "/api/v1/admin/media/"+logo, nil, ""), 409, "RESOURCE_IN_USE")
	if h.anon().get("/media/"+logo+"/file").Status != 200 {
		t.Fatal("site logo should be public")
	}

	free := admin.uploadAdmin("free.png", pngImage(900, 600))
	freeID := free.data()["id"].(string)
	_, key := h.mediaRow(freeID)
	if r := admin.do(http.MethodDelete, "/api/v1/admin/media/"+freeID, nil, ""); r.Status != 204 {
		t.Fatalf("delete: %d %s", r.Status, r.Body)
	}
	if state, _ := h.mediaRow(freeID); state != "DELETED" || fileExists(h.root, key) {
		t.Fatal("deleted media still present")
	}
	for _, f := range h.files("thumbnails") {
		if strings.Contains(f, freeID) {
			t.Fatal("thumbnail left behind")
		}
	}
	expect(t, admin.get(free.data()["url"].(string)), 404, "NOT_FOUND")
	expect(t, admin.do(http.MethodDelete, "/api/v1/admin/media/"+freeID, nil, ""), 404, "NOT_FOUND")
	expect(t, admin.do(http.MethodDelete, "/api/v1/admin/media/"+uuid.NewString(), nil, ""), 404, "NOT_FOUND")

	// Race: a reference appears after phase 1 (PENDING_DELETE). Phase 2 must restore READY and keep the file.
	raced := admin.uploadAdmin("raced.png", pngImage(20, 20)).data()["id"].(string)
	_, racedKey := h.mediaRow(raced)
	h.exec(`UPDATE media_assets SET state = 'PENDING_DELETE' WHERE id = $1`, raced)
	h.exec(`INSERT INTO article_draft_media (article_id, media_id) VALUES ($1, $2)`, article, raced)
	if err := h.svc.FinalizeDelete(context.Background(), uuid.MustParse(raced)); err == nil {
		t.Fatal("finalize should report the new reference")
	}
	if state, _ := h.mediaRow(raced); state != "READY" || !fileExists(h.root, racedKey) {
		t.Fatalf("raced media state=%s file=%v", state, fileExists(h.root, racedKey))
	}
}

// Disk and database failures leave neither orphan files nor rows pointing at missing files.
func TestFailureConsistency(t *testing.T) {
	h := newHarness(t)
	admin := h.user("ADMIN", true)

	// Disk failure while moving the file into place.
	media.SetFailures(h.storage, func(string, string) error { return errors.New("simulated EIO") }, nil)
	expect(t, admin.uploadAdmin("disk.png", pngImage(900, 600)), 503, "SERVICE_UNAVAILABLE") // I/O failure: retry later
	media.SetFailures(h.storage, os.Rename, nil)
	var rows int
	_ = h.pool.QueryRow(context.Background(), `SELECT count(*) FROM media_assets`).Scan(&rows)
	if rows != 0 || len(h.files("originals")) != 0 || len(h.files("thumbnails")) != 0 {
		t.Fatalf("after disk failure: rows=%d originals=%v thumbs=%v", rows, h.files("originals"), h.files("thumbnails"))
	}
	noTempFiles(t, h)

	// Database rejects the row after the files were moved: they are compensated away.
	h.exec(`CREATE FUNCTION p3_fail() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'simulated DB failure'; END $$`)
	h.exec(`CREATE TRIGGER p3_fail BEFORE INSERT ON media_assets FOR EACH ROW EXECUTE FUNCTION p3_fail()`)
	expect(t, admin.uploadAdmin("db.png", pngImage(900, 600)), 500, "INTERNAL_ERROR")
	if len(h.files("originals")) != 0 || len(h.files("thumbnails")) != 0 {
		t.Fatalf("orphans after DB failure: %v %v", h.files("originals"), h.files("thumbnails"))
	}
	noTempFiles(t, h)
	h.exec(`DROP TRIGGER p3_fail ON media_assets`)

	// File removal fails during delete: accepted (204), row stays PENDING_DELETE and is never served;
	// the janitor completes the deletion once the disk recovers.
	r := admin.uploadAdmin("stuck.png", pngImage(900, 600))
	id := r.data()["id"].(string)
	_, key := h.mediaRow(id)
	media.SetFailures(h.storage, nil, func(string) error { return errors.New("simulated EROFS") })
	if d := admin.do(http.MethodDelete, "/api/v1/admin/media/"+id, nil, ""); d.Status != 204 {
		t.Fatalf("delete with failing disk: %d %s", d.Status, d.Body)
	}
	if state, _ := h.mediaRow(id); state != "PENDING_DELETE" || !fileExists(h.root, key) {
		t.Fatalf("state=%s file=%v", state, fileExists(h.root, key))
	}
	expect(t, admin.get(r.data()["url"].(string)), 404, "NOT_FOUND")
	media.SetFailures(h.storage, nil, os.Remove)
	h.svc.Maintain(context.Background())
	if state, _ := h.mediaRow(id); state != "DELETED" || fileExists(h.root, key) {
		t.Fatalf("after janitor: state=%s file=%v", state, fileExists(h.root, key))
	}

	// A READY row whose file vanished (e.g. volume not restored) is a 404, and the report shows it;
	// stray files are reported as orphans and left untouched.
	lost := admin.uploadAdmin("lost.png", pngImage(10, 10))
	lostID := lost.data()["id"].(string)
	_, lostKey := h.mediaRow(lostID)
	if err := os.Remove(h.root + "/" + lostKey); err != nil {
		t.Fatal(err)
	}
	expect(t, admin.get(lost.data()["url"].(string)), 404, "NOT_FOUND")
	stray := h.root + "/originals/ab/abababab-abab-4bab-8bab-abababababab.png"
	_ = os.MkdirAll(h.root+"/originals/ab", 0o750)
	_ = os.WriteFile(stray, []byte("x"), 0o600)
	rep, err := h.svc.CheckConsistency(context.Background())
	if err != nil || len(rep.MissingFiles) != 1 || rep.MissingFiles[0].String() != lostID || len(rep.OrphanFiles) != 1 {
		t.Fatalf("report = %+v, %v", rep, err)
	}
	h.svc.Maintain(context.Background())
	if !fileExists(h.root, "originals/ab/abababab-abab-4bab-8bab-abababababab.png") {
		t.Fatal("janitor deleted an orphan file it cannot attribute")
	}

	// Stale temp files from crashed uploads are cleaned up.
	old := h.root + "/temp/upload-crashed.part"
	_ = os.WriteFile(old, []byte("partial"), 0o600)
	past := mustTime(t)
	_ = os.Chtimes(old, past, past)
	h.svc.Maintain(context.Background())
	noTempFiles(t, h)

	// Database outage: JSON 503 for library calls and file reads.
	h.pool.Close()
	expect(t, admin.get("/api/v1/admin/media"), 503, "SERVICE_UNAVAILABLE")
	expect(t, admin.get(r.data()["url"].(string)), 503, "SERVICE_UNAVAILABLE")
}

// Library listing: search, filter, pagination, reference counts, deleted media hidden.
func TestLibraryListing(t *testing.T) {
	h := newHarness(t)
	admin := h.user("ADMIN", true)
	for _, name := range []string{"sunset.png", "Sunrise.png", "logo_100%.png", "diagram.png"} {
		admin.uploadAdmin(name, pngImage(8, 8))
	}
	site := admin.upload("/api/v1/admin/media", "favicon.png", pngImage(8, 8), map[string]string{"usage_kind": "SITE"})
	h.exec(`UPDATE site_settings SET favicon_media_id = $1`, site.data()["id"])

	list := func(query string) (items []any, meta map[string]any) {
		t.Helper()
		r := admin.get("/api/v1/admin/media" + query)
		if r.Status != 200 {
			t.Fatalf("list %s: %d %s", query, r.Status, r.Body)
		}
		items, _ = r.JSON["data"].([]any)
		meta, _ = r.JSON["meta"].(map[string]any)
		return
	}
	if items, meta := list(""); len(items) != 5 || meta["total"] != float64(5) {
		t.Fatalf("all: %d %v", len(items), meta)
	}
	if items, _ := list("?q=SUN"); len(items) != 2 {
		t.Fatalf("q=SUN: %d", len(items))
	}
	if items, _ := list("?q=" + "100%25"); len(items) != 1 { // literal % is escaped, not a wildcard
		t.Fatalf("q=100%%: %d", len(items))
	}
	items, _ := list("?usage_kind=site")
	if len(items) != 1 || items[0].(map[string]any)["reference_count"] != float64(1) {
		t.Fatalf("usage_kind=site: %v", items)
	}
	if items, meta := list("?page=2&page_size=2"); len(items) != 2 || meta["total_pages"] != float64(3) {
		t.Fatalf("page 2: %d %v", len(items), meta)
	}
	expect(t, admin.get("/api/v1/admin/media?page_size=500"), 422, "VALIDATION_FAILED")

	all, _ := list("?q=diagram")
	id := all[0].(map[string]any)["id"].(string)
	admin.do(http.MethodDelete, "/api/v1/admin/media/"+id, nil, "")
	if items, _ := list("?q=diagram"); len(items) != 0 {
		t.Fatal("deleted media still listed")
	}
}

// Privacy metadata is removed at upload (ADR-0008): a phone photo with GPS EXIF and an XMP author is
// stored and served without them, the orientation survives, and size / SHA-256 describe the stored file.
func TestUploadStripsPrivacyMetadata(t *testing.T) {
	h := newHarness(t)
	admin := h.user("ADMIN", true)
	alice := h.user("USER", true)
	plain := jpegImage(64, 48)
	gps := []byte("Exif\x00\x00II\x2A\x00\x08\x00\x00\x00\x01\x00\x12\x01\x03\x00\x01\x00\x00\x00\x06\x00\x00\x00\x00\x00\x00\x00GPSLatitude=39.9042N GPSLongitude=116.4074E")
	xmp := []byte("http://ns.adobe.com/xap/1.0/\x00<dc:creator>Alice Private</dc:creator>")
	seg := func(marker byte, payload []byte) []byte {
		n := len(payload) + 2
		return append([]byte{0xFF, marker, byte(n >> 8), byte(n)}, payload...)
	}
	photo := append(append(append([]byte{0xFF, 0xD8}, seg(0xE1, gps)...), seg(0xE1, xmp)...), plain[2:]...)

	for _, up := range []response{
		admin.uploadAdmin("phone.jpg", photo),
		alice.upload("/api/v1/me/avatar", "me.jpg", photo, nil),
	} {
		if up.Status != 200 && up.Status != 201 {
			t.Fatalf("upload: %d %s", up.Status, up.Body)
		}
		url, _ := up.data()["url"].(string)
		if url == "" {
			url, _ = up.data()["avatar_url"].(string)
		}
		got := admin.get(url)
		if got.Status != 200 || bytes.Contains(got.Body, []byte("GPS")) || bytes.Contains(got.Body, []byte("Alice Private")) {
			t.Fatalf("served file still carries metadata (%d bytes)", len(got.Body))
		}
		if !bytes.Contains(got.Body, []byte("Exif\x00\x00MM")) {
			t.Fatal("orientation tag not preserved")
		}
		id := strings.Split(url, "/")[2]
		var size int64
		var digest []byte
		if err := h.pool.QueryRow(context.Background(), `SELECT size_bytes, sha256 FROM media_assets WHERE id = $1`, id).Scan(&size, &digest); err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(got.Body)
		if size != int64(len(got.Body)) || !bytes.Equal(digest, sum[:]) {
			t.Fatalf("size/sha256 do not describe the stored file: %d vs %d", size, len(got.Body))
		}
	}
}
