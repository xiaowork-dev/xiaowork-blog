// Package dbtest gives integration tests their own throwaway PostgreSQL databases.
//
// TEST_DATABASE_URL must be a postgres:// URL for a role allowed to CREATE DATABASE on a server
// dedicated to tests (never the compose volume). Each test gets a new database that is dropped afterwards.
// Without the variable the tests are skipped, unless REQUIRE_DB_TESTS=1 (CI) turns that into a failure.
package dbtest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/xiaowork-dev/xiaowork-blog/apps/api/internal/platform/database"
)

func adminURL(t testing.TB) string {
	t.Helper()
	raw := os.Getenv("TEST_DATABASE_URL")
	if raw == "" {
		if os.Getenv("REQUIRE_DB_TESTS") == "1" {
			t.Fatal("REQUIRE_DB_TESTS=1 but TEST_DATABASE_URL is not set")
		}
		t.Skip("TEST_DATABASE_URL not set; skipping PostgreSQL integration test")
	}
	return raw
}

// NewEmptyDatabase creates a fresh, unmigrated database and returns its URL. It is dropped on cleanup.
func NewEmptyDatabase(t testing.TB) string {
	t.Helper()
	admin := adminURL(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	suffix := make([]byte, 6)
	if _, err := rand.Read(suffix); err != nil {
		t.Fatal(err)
	}
	name := "xwb_test_" + hex.EncodeToString(suffix)

	conn, err := pgx.Connect(ctx, admin)
	if err != nil {
		t.Fatalf("connect to test server: %v", err)
	}
	defer conn.Close(ctx)
	if _, err := conn.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{name}.Sanitize()); err != nil {
		t.Fatalf("create test database: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		conn, err := pgx.Connect(ctx, admin)
		if err != nil {
			t.Errorf("connect to drop test database: %v", err)
			return
		}
		defer conn.Close(ctx)
		if _, err := conn.Exec(ctx, "DROP DATABASE IF EXISTS "+pgx.Identifier{name}.Sanitize()+" WITH (FORCE)"); err != nil {
			t.Errorf("drop test database %s: %v", name, err)
		}
	})

	u, err := url.Parse(admin)
	if err != nil {
		t.Fatalf("TEST_DATABASE_URL must be a URL: %v", err)
	}
	u.Path = "/" + name
	return u.String()
}

// NewPool opens a pool on databaseURL that is closed on cleanup (before the database is dropped).
func NewPool(t testing.TB, databaseURL string) *pgxpool.Pool {
	t.Helper()
	pool, err := pgxpool.New(context.Background(), databaseURL)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// NewMigratedPool creates a fresh database, applies every embedded migration and returns a pool on it.
func NewMigratedPool(t testing.TB) *pgxpool.Pool {
	t.Helper()
	pool := NewPool(t, NewEmptyDatabase(t))
	sqlDB := database.OpenSQL(pool)
	t.Cleanup(func() { _ = sqlDB.Close() })
	migrator, err := database.NewMigrator(sqlDB, database.MigrationsFS())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := migrator.Up(context.Background()); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}
	return pool
}
