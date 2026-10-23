package database_test

import (
	"context"
	"io/fs"
	"slices"
	"sync"
	"testing"
	"testing/fstest"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/xiaowork-dev/xiaowork-blog/apps/api/internal/platform/database"
	"github.com/xiaowork-dev/xiaowork-blog/apps/api/internal/platform/database/dbtest"
)

// The 24 tables of DD §3, sorted.
var designTables = []string{
	"admin_audit_logs", "article_draft_media", "article_draft_tags", "article_drafts", "article_metrics",
	"article_publication_media", "article_publication_tags", "article_publications", "article_slugs", "articles",
	"auth_rate_windows", "auth_sessions", "auth_tokens", "categories", "comments", "media_assets",
	"site_navigation_links", "site_settings", "site_social_links", "static_page_media", "static_pages", "tags",
	"user_profiles", "users",
}

const latestVersion = 7

func publicTables(t *testing.T, pool *pgxpool.Pool) []string {
	t.Helper()
	rows, err := pool.Query(context.Background(), `
		SELECT table_name FROM information_schema.tables
		WHERE table_schema = 'public' AND table_type = 'BASE TABLE' AND table_name <> 'goose_db_version'
		ORDER BY table_name`)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatal(err)
		}
		names = append(names, n)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return names
}

func TestMigrationsCreateExactlyTheDesignTablesAndAreRepeatable(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.NewPool(t, dbtest.NewEmptyDatabase(t))
	sqlDB := database.OpenSQL(pool)
	defer sqlDB.Close()
	m, err := database.NewMigrator(sqlDB, database.MigrationsFS())
	if err != nil {
		t.Fatal(err)
	}

	results, err := m.Up(ctx)
	if err != nil {
		t.Fatalf("first up: %v", err)
	}
	if len(results) != latestVersion {
		t.Fatalf("applied %d migrations, want %d", len(results), latestVersion)
	}
	if got := publicTables(t, pool); !slices.Equal(got, designTables) {
		t.Fatalf("tables after up:\n got %v\nwant %v", got, designTables)
	}

	// Re-running up on an up-to-date database is a no-op (repeatable deploys).
	results, err = m.Up(ctx)
	if err != nil {
		t.Fatalf("second up: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("second up applied %d migrations, want 0", len(results))
	}
	if v, err := m.GetDBVersion(ctx); err != nil || v != latestVersion {
		t.Fatalf("version = %d, %v; want %d", v, err, latestVersion)
	}
}

func TestMigrationsRollBackStepByStepAndReapply(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.NewPool(t, dbtest.NewEmptyDatabase(t))
	sqlDB := database.OpenSQL(pool)
	defer sqlDB.Close()
	m, err := database.NewMigrator(sqlDB, database.MigrationsFS())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Up(ctx); err != nil {
		t.Fatal(err)
	}

	// Every Down section must succeed in reverse order on a schema that holds data.
	if _, err := pool.Exec(ctx, `INSERT INTO users (id, email, email_normalized, password_hash, nickname)
		VALUES (gen_random_uuid(), 'a@example.com', 'a@example.com', 'x', 'a')`); err != nil {
		t.Fatal(err)
	}
	for want := int64(latestVersion - 1); want >= 0; want-- {
		if _, err := m.Down(ctx); err != nil {
			t.Fatalf("down to %d: %v", want, err)
		}
		if v, _ := m.GetDBVersion(ctx); v != want {
			t.Fatalf("version after down = %d, want %d", v, want)
		}
	}
	if got := publicTables(t, pool); len(got) != 0 {
		t.Fatalf("tables left after full rollback: %v", got)
	}
	var trgm bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_extension WHERE extname = 'pg_trgm')`).Scan(&trgm); err != nil || trgm {
		t.Fatalf("pg_trgm still installed after rollback (err=%v)", err)
	}

	if _, err := m.Up(ctx); err != nil {
		t.Fatalf("re-apply after rollback: %v", err)
	}
	if got := publicTables(t, pool); !slices.Equal(got, designTables) {
		t.Fatalf("tables after re-apply: %v", got)
	}
}

// DB-19: a failing migration must leave no partial schema and must not advance the version.
func TestFailedMigrationIsAtomic(t *testing.T) {
	ctx := context.Background()
	fsys := fstest.MapFS{}
	err := fs.WalkDir(database.MigrationsFS(), ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := fs.ReadFile(database.MigrationsFS(), path)
		fsys[path] = &fstest.MapFile{Data: data}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	fsys["00008_broken.sql"] = &fstest.MapFile{Data: []byte(`-- +goose Up
CREATE TABLE p1_broken_probe (id integer);
SELECT 1 / 0;
-- +goose Down
DROP TABLE p1_broken_probe;
`)}

	pool := dbtest.NewPool(t, dbtest.NewEmptyDatabase(t))
	sqlDB := database.OpenSQL(pool)
	defer sqlDB.Close()
	m, err := database.NewMigrator(sqlDB, fsys)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Up(ctx); err == nil {
		t.Fatal("expected the broken migration to fail")
	}
	if v, _ := m.GetDBVersion(ctx); v != latestVersion {
		t.Fatalf("version after failure = %d, want %d", v, latestVersion)
	}
	var exists bool
	if err := pool.QueryRow(ctx, `SELECT to_regclass('public.p1_broken_probe') IS NOT NULL`).Scan(&exists); err != nil || exists {
		t.Fatalf("partial table from failed migration exists=%v err=%v", exists, err)
	}
}

// Two deploys racing on the same database: the advisory lock serialises them and both succeed.
func TestConcurrentMigratorsAreSerialised(t *testing.T) {
	ctx := context.Background()
	databaseURL := dbtest.NewEmptyDatabase(t)
	errs := make([]error, 2)
	var wg sync.WaitGroup
	for i := range errs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			pool, err := pgxpool.New(ctx, databaseURL)
			if err != nil {
				errs[i] = err
				return
			}
			defer pool.Close()
			sqlDB := database.OpenSQL(pool)
			defer sqlDB.Close()
			m, err := database.NewMigrator(sqlDB, database.MigrationsFS())
			if err != nil {
				errs[i] = err
				return
			}
			_, errs[i] = m.Up(ctx)
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("migrator %d: %v", i, err)
		}
	}
	pool := dbtest.NewPool(t, databaseURL)
	if got := publicTables(t, pool); !slices.Equal(got, designTables) {
		t.Fatalf("tables after concurrent up: %v", got)
	}
}
