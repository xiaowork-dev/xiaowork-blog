// Command blog-migrate applies the embedded goose migrations to DATABASE_URL.
//
//	blog-migrate up       apply all pending migrations (idempotent)
//	blog-migrate status   list migrations and whether they are applied
//	blog-migrate version  print the current schema version
//	blog-migrate down     roll back ONE migration (development/test databases only)
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/xiaowork-dev/xiaowork-blog/apps/api/internal/platform/database"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: blog-migrate up|status|version|down")
		os.Exit(2)
	}
	if err := run(logger, os.Args[1]); err != nil {
		logger.Error("migration command failed", "command", os.Args[1], "error", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger, command string) error {
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		return fmt.Errorf("DATABASE_URL is required")
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return fmt.Errorf("create DB pool: %w", err)
	}
	defer pool.Close()
	sqlDB := database.OpenSQL(pool)
	defer sqlDB.Close()

	migrator, err := database.NewMigrator(sqlDB, database.MigrationsFS())
	if err != nil {
		return err
	}

	switch command {
	case "up":
		results, err := migrator.Up(ctx)
		for _, r := range results {
			logger.Info("migration applied", "version", r.Source.Version, "file", r.Source.Path, "duration", r.Duration.String())
		}
		if err != nil {
			return err
		}
	case "down":
		r, err := migrator.Down(ctx)
		if err != nil {
			return err
		}
		logger.Info("migration rolled back", "version", r.Source.Version, "file", r.Source.Path)
	case "status":
		statuses, err := migrator.Status(ctx)
		if err != nil {
			return err
		}
		for _, s := range statuses {
			logger.Info("migration status", "version", s.Source.Version, "file", s.Source.Path, "state", string(s.State))
		}
	case "version":
	default:
		return fmt.Errorf("unknown command %q", command)
	}
	version, err := migrator.GetDBVersion(ctx)
	if err != nil {
		return err
	}
	logger.Info("schema version", "version", version)
	return nil
}
