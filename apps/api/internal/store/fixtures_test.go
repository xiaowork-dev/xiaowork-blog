package store_test

import (
	"context"
	"crypto/sha256"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/xiaowork-dev/xiaowork-blog/apps/api/internal/store"
)

// PostgreSQL SQLSTATE codes asserted by the tests.
const (
	codeUniqueViolation     = "23505"
	codeForeignKeyViolation = "23503"
	codeCheckViolation      = "23514"
)

var errRevisionConflict = errors.New("draft revision conflict")

// pgError returns the SQLSTATE and constraint name of err, or empty strings.
func pgError(err error) (code, constraint string) {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code, pgErr.ConstraintName
	}
	return "", ""
}

func expectPgError(t *testing.T, err error, code, constraint string) {
	t.Helper()
	gotCode, gotConstraint := pgError(err)
	if gotCode != code || (constraint != "" && gotConstraint != constraint) {
		t.Fatalf("got error %v (code=%q constraint=%q), want code=%q constraint=%q", err, gotCode, gotConstraint, code, constraint)
	}
}

func digest(s string) []byte {
	sum := sha256.Sum256([]byte(s))
	return sum[:]
}

func text(s string) pgtype.Text { return pgtype.Text{String: s, Valid: true} }

func nullID(id uuid.UUID) uuid.NullUUID { return uuid.NullUUID{UUID: id, Valid: true} }

func newUser(t *testing.T, q *store.Queries, email string) store.User {
	t.Helper()
	ctx := context.Background()
	u, err := q.CreateUser(ctx, store.CreateUserParams{
		ID:              uuid.New(),
		Email:           email,
		EmailNormalized: strings.ToLower(strings.TrimSpace(email)),
		PasswordHash:    "$argon2id$v=19$m=65536,t=3,p=1$placeholder$placeholder",
		Nickname:        "tester",
		Role:            "USER",
	})
	if err != nil {
		t.Fatalf("create user %s: %v", email, err)
	}
	if err := q.CreateUserProfile(ctx, u.ID); err != nil {
		t.Fatal(err)
	}
	return u
}

func newAdmin(t *testing.T, pool *pgxpool.Pool) store.User {
	t.Helper()
	q := store.New(pool)
	u := newUser(t, q, "admin-"+uuid.NewString()[:8]+"@example.com")
	if _, err := pool.Exec(context.Background(), `UPDATE users SET role = 'ADMIN' WHERE id = $1`, u.ID); err != nil {
		t.Fatal(err)
	}
	return u
}

// newArticle creates articles(DRAFT) + an empty draft (revision 1) + metrics in one transaction (DD T03 step 1).
func newArticle(t *testing.T, pool *pgxpool.Pool, author uuid.UUID) uuid.UUID {
	t.Helper()
	id := uuid.New()
	err := store.ExecTx(context.Background(), pool, func(q *store.Queries) error {
		if _, err := q.CreateArticle(context.Background(), store.CreateArticleParams{ID: id, AuthorID: author}); err != nil {
			return err
		}
		if _, err := q.CreateArticleDraft(context.Background(), store.CreateArticleDraftParams{ArticleID: id, LastEditedBy: author}); err != nil {
			return err
		}
		return q.CreateArticleMetrics(context.Background(), id)
	})
	if err != nil {
		t.Fatalf("create article: %v", err)
	}
	return id
}

func newMedia(t *testing.T, q *store.Queries, owner uuid.UUID, kind string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	_, err := q.CreateMediaAsset(context.Background(), store.CreateMediaAssetParams{
		ID: id, UploadedBy: owner, UsageKind: kind, StorageKey: "originals/" + id.String() + ".webp",
		OriginalFilename: "photo.webp", MimeType: "image/webp", SizeBytes: 1024, Width: 640, Height: 480,
	})
	if err != nil {
		t.Fatalf("create media: %v", err)
	}
	return id
}

type draftInput struct {
	Title, Body, Slug string
	Category, Cover   uuid.NullUUID
	CommentsEnabled   bool
	Tags, Media       []uuid.UUID
}

// saveDraft is the DD T03 step 2 sequence: lock article, revision CAS, replace draft relations — one transaction.
func saveDraft(ctx context.Context, pool *pgxpool.Pool, articleID, editor uuid.UUID, expected int64, in draftInput) (int64, error) {
	var revision int64
	err := store.ExecTx(ctx, pool, func(q *store.Queries) error {
		if _, err := q.LockArticle(ctx, articleID); err != nil {
			return err
		}
		var slug pgtype.Text // empty input means "no candidate slug" (NULL), not an empty string
		if in.Slug != "" {
			slug = text(in.Slug)
		}
		row, err := q.UpdateArticleDraft(ctx, store.UpdateArticleDraftParams{
			Title: in.Title, BodyMarkdown: in.Body, CandidateSlug: slug,
			CategoryID: in.Category, CoverMediaID: in.Cover, CommentsEnabled: in.CommentsEnabled,
			LastEditedBy: editor, ArticleID: articleID, ExpectedRevision: expected,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return errRevisionConflict
		}
		if err != nil {
			return err
		}
		revision = row.Revision
		if err := q.DeleteDraftTags(ctx, articleID); err != nil {
			return err
		}
		if err := q.AddDraftTags(ctx, store.AddDraftTagsParams{ArticleID: articleID, TagIds: in.Tags}); err != nil {
			return err
		}
		if err := q.DeleteDraftMedia(ctx, articleID); err != nil {
			return err
		}
		return q.AddDraftMedia(ctx, store.AddDraftMediaParams{ArticleID: articleID, MediaIds: in.Media})
	})
	return revision, err
}

type publishResult struct {
	Changed             bool
	PublicationRevision int64
}

// publish is the DD T03 step 3 sequence, run in one transaction so any failure rolls everything back.
// The production service (P4) adds validation, audit and Markdown→plain text; the SQL steps are the same.
func publish(ctx context.Context, pool *pgxpool.Pool, articleID uuid.UUID, expected int64, at time.Time) (publishResult, error) {
	var res publishResult
	err := store.ExecTx(ctx, pool, func(q *store.Queries) error {
		art, err := q.LockArticle(ctx, articleID)
		if err != nil {
			return err
		}
		draft, err := q.GetArticleDraft(ctx, articleID)
		if err != nil {
			return err
		}
		if draft.Revision != expected {
			return errRevisionConflict
		}
		// Same revision re-published while still PUBLISHED: idempotent no-op (API §5.3).
		if art.Status == "PUBLISHED" && art.LastPublishedDraftRevision.Valid && art.LastPublishedDraftRevision.Int64 == draft.Revision {
			return nil
		}
		slug := draft.CandidateSlug.String
		if err := q.RegisterArticleSlug(ctx, store.RegisterArticleSlugParams{Slug: slug, ArticleID: articleID}); err != nil {
			return err
		}
		if err := q.ClearCurrentArticleSlug(ctx, store.ClearCurrentArticleSlugParams{ArticleID: articleID, Slug: slug}); err != nil {
			return err
		}
		if err := q.SetCurrentArticleSlug(ctx, store.SetCurrentArticleSlugParams{Slug: slug, ArticleID: articleID}); err != nil {
			return err
		}
		row, err := q.UpsertPublicationFromDraft(ctx, store.UpsertPublicationFromDraftParams{
			FallbackSummary: "", BodyPlainText: draft.BodyMarkdown, Slug: slug, PublishedAt: at, ArticleID: articleID,
		})
		if err != nil {
			return err
		}
		for _, step := range []func(context.Context, uuid.UUID) error{
			q.DeletePublicationTags, q.CopyDraftTagsToPublication, q.DeletePublicationMedia, q.CopyDraftMediaToPublication,
		} {
			if err := step(ctx, articleID); err != nil {
				return err
			}
		}
		if err := q.MarkArticlePublished(ctx, store.MarkArticlePublishedParams{PublishedAt: at, DraftRevision: draft.Revision, ID: articleID}); err != nil {
			return err
		}
		res = publishResult{Changed: true, PublicationRevision: row.PublicationRevision}
		return nil
	})
	return res, err
}

// createPublished creates, saves and publishes an article with the given slug and body.
func createPublished(t *testing.T, pool *pgxpool.Pool, author uuid.UUID, slug, title, body string) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	id := newArticle(t, pool, author)
	rev, err := saveDraft(ctx, pool, id, author, 1, draftInput{Title: title, Body: body, Slug: slug, CommentsEnabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := publish(ctx, pool, id, rev, time.Now()); err != nil {
		t.Fatalf("publish %s: %v", slug, err)
	}
	return id
}
