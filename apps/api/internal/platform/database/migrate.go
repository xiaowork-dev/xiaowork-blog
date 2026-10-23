// Package database wires PostgreSQL connections and schema migrations.
package database

import (
	"database/sql"
	"fmt"
	"io/fs"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"
	"github.com/xiaowork-dev/xiaowork-blog/apps/api/db"
)

// MigrationsFS returns the embedded migration directory with the "migrations/" prefix stripped.
func MigrationsFS() fs.FS {
	sub, err := fs.Sub(db.Migrations, "migrations")
	if err != nil {
		panic(err) // the embed pattern guarantees the directory exists
	}
	return sub
}

// NewMigrator builds a goose provider over fsys. A PostgreSQL advisory session lock
// stops two migrators (e.g. two deploys) from applying migrations at the same time.
func NewMigrator(sqlDB *sql.DB, fsys fs.FS) (*goose.Provider, error) {
	locker, err := lock.NewPostgresSessionLocker()
	if err != nil {
		return nil, fmt.Errorf("create migration lock: %w", err)
	}
	return goose.NewProvider(goose.DialectPostgres, sqlDB, fsys, goose.WithSessionLocker(locker))
}

// OpenSQL exposes a pgx pool as *sql.DB for goose. Closing the returned DB does not close the pool.
func OpenSQL(pool *pgxpool.Pool) *sql.DB {
	return stdlib.OpenDBFromPool(pool)
}
