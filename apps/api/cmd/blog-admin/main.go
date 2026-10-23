// Command blog-admin performs protected operator tasks that must never be exposed over HTTP.
//
//	blog-admin create-admin -email owner@example.com -nickname XiaoWork   (password read from stdin)
//	blog-admin media-check [-root /data/media]                             (consistency report, read-only)
//
// create-admin only succeeds while no ADMIN account exists. The password is read from the first line of
// stdin so it never appears in the process list, environment or shell history.
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/xiaowork-dev/xiaowork-blog/apps/api/internal/auth"
	"github.com/xiaowork-dev/xiaowork-blog/apps/api/internal/media"
)

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	switch os.Args[1] {
	case "create-admin":
		createAdmin(os.Args[2:])
	case "media-check":
		mediaCheck(os.Args[2:])
	default:
		usage()
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage:\n  blog-admin create-admin -email EMAIL -nickname NAME  (password on stdin)\n  blog-admin media-check [-root DIR]")
	os.Exit(2)
}

func openDB(ctx context.Context) *pgxpool.Pool {
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		fail("DATABASE_URL is required")
	}
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		fail("cannot open database: " + err.Error())
	}
	return pool
}

func createAdmin(args []string) {
	fs := flag.NewFlagSet("create-admin", flag.ExitOnError)
	email := fs.String("email", "", "administrator e-mail address")
	nickname := fs.String("nickname", "", "display name")
	_ = fs.Parse(args)

	fmt.Fprintln(os.Stderr, "Reading the administrator password from stdin (first line, 12–128 characters)...")
	password, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && password == "" {
		fail("no password on stdin")
	}
	password = strings.TrimRight(password, "\r\n")

	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	pool := openDB(ctx)
	defer pool.Close()

	id, err := auth.CreateFirstAdmin(ctx, pool, auth.NewPasswordHasher(auth.DefaultArgon2, 1), *email, *nickname, password)
	var invalid auth.ErrInvalidAdminInput
	switch {
	case errors.Is(err, auth.ErrAdminExists):
		fail("refused: an administrator already exists")
	case errors.As(err, &invalid):
		fail("invalid input: " + invalid.Error())
	case err != nil:
		fail("failed: " + err.Error())
	}
	fmt.Printf("administrator created: id=%s email=%s\n", id, strings.TrimSpace(*email))
}

// mediaCheck compares media_assets with the files on the volume, e.g. after restoring a backup or
// recreating containers. It changes nothing; exit status 3 means inconsistencies were found.
func mediaCheck(args []string) {
	fs := flag.NewFlagSet("media-check", flag.ExitOnError)
	root := fs.String("root", envOr("MEDIA_ROOT", "/data/media"), "media root directory")
	_ = fs.Parse(args)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	pool := openDB(ctx)
	defer pool.Close()
	svc := media.NewService(pool, nil, media.NewStorage(*root), media.Options{Root: *root}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	rep, err := svc.CheckConsistency(ctx)
	if err != nil {
		fail("check failed: " + err.Error())
	}
	fmt.Printf("missing files (database rows without a file): %d\n", len(rep.MissingFiles))
	for _, id := range rep.MissingFiles {
		fmt.Printf("  media %s\n", id)
	}
	fmt.Printf("orphan files (files without a database row): %d\n", len(rep.OrphanFiles))
	for _, k := range rep.OrphanFiles {
		fmt.Printf("  %s\n", k)
	}
	if len(rep.MissingFiles) > 0 || len(rep.OrphanFiles) > 0 {
		os.Exit(3)
	}
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func fail(msg string) {
	fmt.Fprintln(os.Stderr, "blog-admin: "+msg)
	os.Exit(1)
}
