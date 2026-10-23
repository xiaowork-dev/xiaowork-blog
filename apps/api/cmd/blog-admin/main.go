// Command blog-admin performs protected operator tasks that must never be exposed over HTTP.
//
//	blog-admin create-admin -email owner@example.com -nickname XiaoWork   (password read from stdin)
//
// It only succeeds while no ADMIN account exists. The password is read from the first line of stdin so
// it never appears in the process list, environment or shell history.
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/xiaowork-dev/xiaowork-blog/apps/api/internal/auth"
)

func main() {
	if len(os.Args) < 2 || os.Args[1] != "create-admin" {
		fmt.Fprintln(os.Stderr, "usage: blog-admin create-admin -email EMAIL -nickname NAME  (password on stdin)")
		os.Exit(2)
	}
	fs := flag.NewFlagSet("create-admin", flag.ExitOnError)
	email := fs.String("email", "", "administrator e-mail address")
	nickname := fs.String("nickname", "", "display name")
	_ = fs.Parse(os.Args[2:])

	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		fail("DATABASE_URL is required")
	}
	fmt.Fprintln(os.Stderr, "Reading the administrator password from stdin (first line, 12–128 characters)...")
	password, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && password == "" {
		fail("no password on stdin")
	}
	password = strings.TrimRight(password, "\r\n")

	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		fail("cannot open database: " + err.Error())
	}
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

func fail(msg string) {
	fmt.Fprintln(os.Stderr, "blog-admin: "+msg)
	os.Exit(1)
}
