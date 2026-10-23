package store_test

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/xiaowork-dev/xiaowork-blog/apps/api/internal/platform/database/dbtest"
	"github.com/xiaowork-dev/xiaowork-blog/apps/api/internal/store"
)

// DB-07: two editors saving from the same revision — exactly one wins, the newer draft is kept.
func TestDraftRevisionCAS(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.NewMigratedPool(t)
	admin := newAdmin(t, pool)
	id := newArticle(t, pool, admin.ID)

	titles := []string{"window A", "window B"}
	errs := make([]error, len(titles))
	var wg sync.WaitGroup
	for i, title := range titles {
		wg.Add(1)
		go func(i int, title string) {
			defer wg.Done()
			_, errs[i] = saveDraft(ctx, pool, id, admin.ID, 1, draftInput{Title: title, Body: "body", CommentsEnabled: true})
		}(i, title)
	}
	wg.Wait()

	winner := -1
	for i, err := range errs {
		switch {
		case err == nil:
			winner = i
		case !errors.Is(err, errRevisionConflict):
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if winner < 0 || (errs[0] == nil) == (errs[1] == nil) {
		t.Fatalf("want exactly one success, got %v", errs)
	}
	draft, err := store.New(pool).GetArticleDraft(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if draft.Revision != 2 || draft.Title != titles[winner] {
		t.Fatalf("draft = rev %d %q, want rev 2 %q", draft.Revision, draft.Title, titles[winner])
	}
	// A late request still carrying revision 1 cannot overwrite revision 2.
	if _, err := saveDraft(ctx, pool, id, admin.ID, 1, draftInput{Title: "stale", Body: "x"}); !errors.Is(err, errRevisionConflict) {
		t.Fatalf("stale save: %v", err)
	}
}

// DB-05, DB-17, DB-08, DB-09: editing a published article only touches the draft; publishing switches
// everything at once; re-publishing the same revision is a no-op; old slugs resolve to the current one.
func TestDraftAndPublicationAreIsolated(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.NewMigratedPool(t)
	q := store.New(pool)
	admin := newAdmin(t, pool)
	catA, _ := q.CreateCategory(ctx, store.CreateCategoryParams{ID: uuid.New(), Name: "开发笔记", Slug: "dev"})
	catB, _ := q.CreateCategory(ctx, store.CreateCategoryParams{ID: uuid.New(), Name: "生活", Slug: "life"})
	tag1, _ := q.CreateTag(ctx, store.CreateTagParams{ID: uuid.New(), Name: "Docker", Slug: "docker"})
	tag2, _ := q.CreateTag(ctx, store.CreateTagParams{ID: uuid.New(), Name: "Go", Slug: "go"})
	m1 := newMedia(t, q, admin.ID, "ARTICLE")
	m2 := newMedia(t, q, admin.ID, "ARTICLE")

	id := newArticle(t, pool, admin.ID)
	rev, err := saveDraft(ctx, pool, id, admin.ID, 1, draftInput{
		Title: "V1 标题", Body: "V1 正文", Slug: "hello", Category: nullID(catA.ID), Cover: nullID(m1),
		CommentsEnabled: true, Tags: []uuid.UUID{tag1.ID}, Media: []uuid.UUID{m1},
	})
	if err != nil {
		t.Fatal(err)
	}
	firstAt := time.Now().UTC().Truncate(time.Microsecond)
	res, err := publish(ctx, pool, id, rev, firstAt)
	if err != nil || !res.Changed || res.PublicationRevision != 1 {
		t.Fatalf("first publish = %+v, %v", res, err)
	}

	// Edit everything in the draft.
	rev, err = saveDraft(ctx, pool, id, admin.ID, rev, draftInput{
		Title: "V2 标题", Body: "V2 正文", Slug: "hello-v2", Category: nullID(catB.ID), Cover: nullID(m2),
		CommentsEnabled: false, Tags: []uuid.UUID{tag2.ID}, Media: []uuid.UUID{m2},
	})
	if err != nil {
		t.Fatal(err)
	}

	pub, err := q.GetPublishedArticleBySlug(ctx, "hello")
	if err != nil {
		t.Fatal(err)
	}
	if pub.Title != "V1 标题" || pub.BodyMarkdown != "V1 正文" || pub.CategoryID.UUID != catA.ID ||
		pub.CoverMediaID.UUID != m1 || !pub.CommentsEnabled || pub.ResolvedFromOldSlug {
		t.Fatalf("public snapshot changed by a draft save: %+v", pub)
	}
	if tags, _ := q.ListPublicationTagSlugs(ctx, id); !slices.Equal(tags, []string{"docker"}) {
		t.Fatalf("public tags = %v, want [docker]", tags)
	}
	if tags, _ := q.ListDraftTagSlugs(ctx, id); !slices.Equal(tags, []string{"go"}) {
		t.Fatalf("draft tags = %v, want [go]", tags)
	}
	if _, err := q.GetPublishedArticleBySlug(ctx, "hello-v2"); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("draft slug became public before publishing: %v", err)
	}
	assertMediaVisible(t, q, m1, true)
	assertMediaVisible(t, q, m2, false) // only referenced by the draft (DB-14)

	// Publish the update: every public field switches together.
	secondAt := firstAt.Add(time.Minute)
	res, err = publish(ctx, pool, id, rev, secondAt)
	if err != nil || !res.Changed || res.PublicationRevision != 2 {
		t.Fatalf("second publish = %+v, %v", res, err)
	}
	pub, err = q.GetPublishedArticleBySlug(ctx, "hello-v2")
	if err != nil {
		t.Fatal(err)
	}
	if pub.Title != "V2 标题" || pub.CategoryID.UUID != catB.ID || pub.CoverMediaID.UUID != m2 || pub.CommentsEnabled {
		t.Fatalf("public snapshot after update: %+v", pub)
	}
	if tags, _ := q.ListPublicationTagSlugs(ctx, id); !slices.Equal(tags, []string{"go"}) {
		t.Fatalf("public tags after update = %v", tags)
	}
	assertMediaVisible(t, q, m1, false)
	assertMediaVisible(t, q, m2, true)

	// Old slug still resolves to the article and reports the canonical one (Nuxt issues the 301).
	old, err := q.GetPublishedArticleBySlug(ctx, "hello")
	if err != nil || !old.ResolvedFromOldSlug || old.CurrentSlug != "hello-v2" {
		t.Fatalf("old slug lookup = %+v, %v", old, err)
	}

	art, _ := q.GetArticle(ctx, id)
	if !art.FirstPublishedAt.Time.Equal(firstAt) || !art.PublicUpdatedAt.Time.Equal(secondAt) {
		t.Fatalf("first_published_at=%v public_updated_at=%v", art.FirstPublishedAt.Time, art.PublicUpdatedAt.Time)
	}

	// DB-08: repeating the same revision changes nothing.
	res, err = publish(ctx, pool, id, rev, secondAt.Add(time.Minute))
	if err != nil || res.Changed {
		t.Fatalf("repeat publish = %+v, %v", res, err)
	}
	art, _ = q.GetArticle(ctx, id)
	pub, _ = q.GetPublishedArticleBySlug(ctx, "hello-v2")
	if pub.PublicationRevision != 2 || !art.PublicUpdatedAt.Time.Equal(secondAt) {
		t.Fatalf("repeat publish modified state: rev=%d updated=%v", pub.PublicationRevision, art.PublicUpdatedAt.Time)
	}

	// Exactly one current slug per article.
	var current int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM article_slugs WHERE article_id = $1 AND is_current`, id).Scan(&current); err != nil || current != 1 {
		t.Fatalf("current slugs = %d, %v", current, err)
	}
	_, err = pool.Exec(ctx, `UPDATE article_slugs SET is_current = TRUE WHERE slug = 'hello'`)
	expectPgError(t, err, codeUniqueViolation, "article_slugs_one_current_per_article")
}

func assertMediaVisible(t *testing.T, q *store.Queries, id uuid.UUID, want bool) {
	t.Helper()
	got, err := q.IsMediaPubliclyVisible(context.Background(), id)
	if err != nil || got != want {
		t.Fatalf("media %s visible = %v (%v), want %v", id, got, err, want)
	}
}

// DB-06 and DB-10: a publish that hits a taken slug rolls back completely; concurrent publishes of the
// same slug let exactly one article win.
func TestPublishIsAtomicAndSlugsAreUnique(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.NewMigratedPool(t)
	q := store.New(pool)
	admin := newAdmin(t, pool)
	tag, _ := q.CreateTag(ctx, store.CreateTagParams{ID: uuid.New(), Name: "Go", Slug: "go"})

	owner := createPublished(t, pool, admin.ID, "taken", "Owner", "body")
	// Owner moves to a new slug; "taken" stays reserved as its historical slug and is never reused.
	rev, _ := q.GetArticleDraft(ctx, owner)
	r, err := saveDraft(ctx, pool, owner, admin.ID, rev.Revision, draftInput{Title: "Owner", Body: "body", Slug: "owner-new", CommentsEnabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := publish(ctx, pool, owner, r, time.Now()); err != nil {
		t.Fatal(err)
	}

	other := newArticle(t, pool, admin.ID)
	r, err = saveDraft(ctx, pool, other, admin.ID, 1, draftInput{Title: "Other", Body: "body", Slug: "taken", CommentsEnabled: true, Tags: []uuid.UUID{tag.ID}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = publish(ctx, pool, other, r, time.Now())
	expectPgError(t, err, codeUniqueViolation, "article_slugs_pkey")

	art, _ := q.GetArticle(ctx, other)
	if art.Status != "DRAFT" || art.FirstPublishedAt.Valid {
		t.Fatalf("failed publish changed the article: %+v", art)
	}
	var pubs, slugs, pubTags int
	_ = pool.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM article_publications WHERE article_id = $1),
		(SELECT count(*) FROM article_slugs WHERE article_id = $1),
		(SELECT count(*) FROM article_publication_tags WHERE article_id = $1)`, other).Scan(&pubs, &slugs, &pubTags)
	if pubs != 0 || slugs != 0 || pubTags != 0 {
		t.Fatalf("partial publish left rows: publications=%d slugs=%d tags=%d", pubs, slugs, pubTags)
	}

	// A publication can only point at a slug registered to the same article ("taken" belongs to owner's history).
	_, err = pool.Exec(ctx, `INSERT INTO article_publications (article_id, publication_revision, source_draft_revision,
		title, summary, body_markdown, body_plain_text, slug, comments_enabled, published_at)
		VALUES ($1, 1, 1, 't', '', 'b', 'b', 'taken', TRUE, now())`, other)
	expectPgError(t, err, codeForeignKeyViolation, "article_publications_slug_registered")

	// Concurrent publish of the same slug by two articles.
	ids := []uuid.UUID{newArticle(t, pool, admin.ID), newArticle(t, pool, admin.ID)}
	revs := make([]int64, len(ids))
	for i, id := range ids {
		if revs[i], err = saveDraft(ctx, pool, id, admin.ID, 1, draftInput{Title: "Race", Body: "body", Slug: "race-slug", CommentsEnabled: true}); err != nil {
			t.Fatal(err)
		}
	}
	errs := make([]error, len(ids))
	var wg sync.WaitGroup
	for i, id := range ids {
		wg.Add(1)
		go func(i int, id uuid.UUID) {
			defer wg.Done()
			_, errs[i] = publish(ctx, pool, id, revs[i], time.Now())
		}(i, id)
	}
	wg.Wait()
	ok := 0
	for _, err := range errs {
		if err == nil {
			ok++
			continue
		}
		expectPgError(t, err, codeUniqueViolation, "article_slugs_pkey")
	}
	if ok != 1 {
		t.Fatalf("%d concurrent publishes of one slug succeeded, want 1", ok)
	}
}

// DB-09, DB-20 and T04: withdrawn/trashed articles vanish from every public read; restore never republishes.
func TestWithdrawTrashRestore(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.NewMigratedPool(t)
	q := store.New(pool)
	admin := newAdmin(t, pool)
	cover := newMedia(t, q, admin.ID, "ARTICLE")

	id := newArticle(t, pool, admin.ID)
	rev, _ := saveDraft(ctx, pool, id, admin.ID, 1, draftInput{Title: "Docker Compose 容器化部署", Body: "容器化部署笔记", Slug: "compose", Cover: nullID(cover), CommentsEnabled: true})
	if _, err := publish(ctx, pool, id, rev, time.Now()); err != nil {
		t.Fatal(err)
	}
	draftOnly := newArticle(t, pool, admin.ID)
	if _, err := saveDraft(ctx, pool, draftOnly, admin.ID, 1, draftInput{Title: "容器 草稿", Body: "容器", Slug: "draft-only", CommentsEnabled: true}); err != nil {
		t.Fatal(err)
	}

	// Search (DB-16, data part) only sees the public snapshot.
	hits, err := q.SearchPublishedArticles(ctx, store.SearchPublishedArticlesParams{Pattern: "%容器%", PageLimit: 10, PageOffset: 0})
	if err != nil || len(hits) != 1 || hits[0].ID != id {
		t.Fatalf("search = %+v, %v", hits, err)
	}
	assertMediaVisible(t, q, cover, true)

	if n, _ := q.WithdrawArticle(ctx, id); n != 1 {
		t.Fatal("withdraw failed")
	}
	if _, err := q.GetPublishedArticleBySlug(ctx, "compose"); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("withdrawn article still readable: %v", err)
	}
	list, _ := q.ListPublishedArticles(ctx, store.ListPublishedArticlesParams{PageLimit: 10, PageOffset: 0})
	hits, _ = q.SearchPublishedArticles(ctx, store.SearchPublishedArticlesParams{Pattern: "%容器%", PageLimit: 10, PageOffset: 0})
	if len(list) != 0 || len(hits) != 0 {
		t.Fatalf("withdrawn article listed=%d searched=%d", len(list), len(hits))
	}
	assertMediaVisible(t, q, cover, false)

	// Explicitly re-publishing a withdrawn article with the same revision is a real publish (API §5.3).
	res, err := publish(ctx, pool, id, rev, time.Now())
	if err != nil || !res.Changed {
		t.Fatalf("re-publish after withdraw = %+v, %v", res, err)
	}

	if n, _ := q.TrashArticle(ctx, id); n != 1 {
		t.Fatal("trash failed")
	}
	if _, err := q.GetPublishedArticleBySlug(ctx, "compose"); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("trashed article still readable: %v", err)
	}
	if status, err := q.RestoreArticle(ctx, id); err != nil || status != "WITHDRAWN" {
		t.Fatalf("restore published article = %q, %v; want WITHDRAWN", status, err)
	}
	if n, _ := q.TrashArticle(ctx, draftOnly); n != 1 {
		t.Fatal("trash draft failed")
	}
	if status, err := q.RestoreArticle(ctx, draftOnly); err != nil || status != "DRAFT" {
		t.Fatalf("restore never-published article = %q, %v; want DRAFT", status, err)
	}

	// Lifecycle invariants are enforced by the schema too.
	_, err = pool.Exec(ctx, `UPDATE articles SET status = 'PUBLISHED' WHERE id = $1`, draftOnly)
	expectPgError(t, err, codeCheckViolation, "articles_published_has_first_publish")
	_, err = pool.Exec(ctx, `UPDATE articles SET status = 'TRASHED' WHERE id = $1`, draftOnly)
	expectPgError(t, err, codeCheckViolation, "articles_trashed_has_time")
}

func TestForeignKeysProtectArticleData(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.NewMigratedPool(t)
	q := store.New(pool)
	admin := newAdmin(t, pool)
	id := newArticle(t, pool, admin.ID)

	_, err := q.CreateArticle(ctx, store.CreateArticleParams{ID: uuid.New(), AuthorID: uuid.New()})
	expectPgError(t, err, codeForeignKeyViolation, "articles_author_id_fkey")

	_, err = pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, admin.ID)
	expectPgError(t, err, codeForeignKeyViolation, "")

	err = q.AddDraftTags(ctx, store.AddDraftTagsParams{ArticleID: id, TagIds: []uuid.UUID{uuid.New()}})
	expectPgError(t, err, codeForeignKeyViolation, "article_draft_tags_tag_id_fkey")

	// Public relations hang off the publication, so an unpublished article cannot get public tags.
	tag, _ := q.CreateTag(ctx, store.CreateTagParams{ID: uuid.New(), Name: "Go", Slug: "go"})
	_, err = pool.Exec(ctx, `INSERT INTO article_publication_tags (article_id, tag_id) VALUES ($1, $2)`, id, tag.ID)
	expectPgError(t, err, codeForeignKeyViolation, "article_publication_tags_article_id_fkey")

	// Categories and tags in use cannot be deleted (DD §5.5).
	if _, err := saveDraft(ctx, pool, id, admin.ID, 1, draftInput{Title: "t", Body: "b", Tags: []uuid.UUID{tag.ID}}); err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(ctx, `DELETE FROM tags WHERE id = $1`, tag.ID)
	expectPgError(t, err, codeForeignKeyViolation, "article_draft_tags_tag_id_fkey")
}

func TestViewCounterIsAtomic(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.NewMigratedPool(t)
	q := store.New(pool)
	admin := newAdmin(t, pool)
	id := newArticle(t, pool, admin.ID)
	var wg sync.WaitGroup
	for range 50 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := q.IncrementArticleViews(ctx, id); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if n, err := q.GetArticleViews(ctx, id); err != nil || n != 50 {
		t.Fatalf("view_count = %d, %v; want 50", n, err)
	}
	_, err := pool.Exec(ctx, `UPDATE article_metrics SET view_count = -1 WHERE article_id = $1`, id)
	expectPgError(t, err, codeCheckViolation, "article_metrics_view_count_non_negative")
}
