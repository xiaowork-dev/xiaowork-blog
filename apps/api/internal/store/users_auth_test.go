package store_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/xiaowork-dev/xiaowork-blog/apps/api/internal/platform/database/dbtest"
	"github.com/xiaowork-dev/xiaowork-blog/apps/api/internal/store"
)

// DB-01: one account per normalized email, also under concurrency.
func TestEmailUniqueness(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.NewMigratedPool(t)
	q := store.New(pool)

	newUser(t, q, "Alice@Example.com")
	_, err := q.CreateUser(ctx, store.CreateUserParams{
		ID: uuid.New(), Email: "ALICE@example.COM", EmailNormalized: "alice@example.com",
		PasswordHash: "h", Nickname: "dup", Role: "USER",
	})
	expectPgError(t, err, codeUniqueViolation, "users_email_normalized_key")

	// The normalized column must already be lower-cased and trimmed.
	_, err = q.CreateUser(ctx, store.CreateUserParams{
		ID: uuid.New(), Email: "Bob@Example.com", EmailNormalized: "Bob@Example.com",
		PasswordHash: "h", Nickname: "bob", Role: "USER",
	})
	expectPgError(t, err, codeCheckViolation, "users_email_normalized_form")

	_, err = q.CreateUser(ctx, store.CreateUserParams{
		ID: uuid.New(), Email: "root@example.com", EmailNormalized: "root@example.com",
		PasswordHash: "h", Nickname: "root", Role: "ROOT",
	})
	expectPgError(t, err, codeCheckViolation, "users_role_check")

	const workers = 8
	var wg sync.WaitGroup
	results := make([]error, workers)
	for i := range workers {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, results[i] = q.CreateUser(ctx, store.CreateUserParams{
				ID: uuid.New(), Email: "Race@Example.com", EmailNormalized: "race@example.com",
				PasswordHash: "h", Nickname: "race", Role: "USER",
			})
		}(i)
	}
	wg.Wait()
	ok := 0
	for _, err := range results {
		if err == nil {
			ok++
			continue
		}
		expectPgError(t, err, codeUniqueViolation, "users_email_normalized_key")
	}
	if ok != 1 {
		t.Fatalf("%d concurrent registrations succeeded, want exactly 1", ok)
	}
}

// DB-03: a one-time token is consumed at most once, and expired/revoked/wrong-purpose tokens never work.
func TestAuthTokenSingleUse(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.NewMigratedPool(t)
	q := store.New(pool)
	u := newUser(t, q, "token@example.com")

	create := func(raw, purpose string) {
		t.Helper()
		if err := q.CreateAuthToken(ctx, store.CreateAuthTokenParams{
			ID: uuid.New(), UserID: u.ID, Purpose: purpose, TokenDigest: digest(raw), ExpiresAt: time.Now().Add(time.Hour),
		}); err != nil {
			t.Fatal(err)
		}
	}
	create("verify-1", "VERIFY_EMAIL")

	const workers = 10
	var wg sync.WaitGroup
	errs := make([]error, workers)
	for i := range workers {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, errs[i] = q.ConsumeAuthToken(ctx, store.ConsumeAuthTokenParams{TokenDigest: digest("verify-1"), Purpose: "VERIFY_EMAIL"})
		}(i)
	}
	wg.Wait()
	ok := 0
	for _, err := range errs {
		switch {
		case err == nil:
			ok++
		case !errors.Is(err, pgx.ErrNoRows):
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if ok != 1 {
		t.Fatalf("token consumed %d times, want exactly 1", ok)
	}

	// Wrong purpose.
	create("reset-1", "RESET_PASSWORD")
	if _, err := q.ConsumeAuthToken(ctx, store.ConsumeAuthTokenParams{TokenDigest: digest("reset-1"), Purpose: "VERIFY_EMAIL"}); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("wrong purpose consumed: %v", err)
	}
	// Revoked.
	if n, err := q.RevokeUnusedTokens(ctx, store.RevokeUnusedTokensParams{UserID: u.ID, Purpose: "RESET_PASSWORD"}); err != nil || n != 1 {
		t.Fatalf("revoke = %d, %v", n, err)
	}
	if _, err := q.ConsumeAuthToken(ctx, store.ConsumeAuthTokenParams{TokenDigest: digest("reset-1"), Purpose: "RESET_PASSWORD"}); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("revoked token consumed: %v", err)
	}
	// Expired.
	if _, err := pool.Exec(ctx, `INSERT INTO auth_tokens (id, user_id, purpose, token_digest, created_at, expires_at)
		VALUES ($1, $2, 'VERIFY_EMAIL', $3, now() - interval '2 hours', now() - interval '1 hour')`,
		uuid.New(), u.ID, digest("expired-1")); err != nil {
		t.Fatal(err)
	}
	if _, err := q.ConsumeAuthToken(ctx, store.ConsumeAuthTokenParams{TokenDigest: digest("expired-1"), Purpose: "VERIFY_EMAIL"}); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("expired token consumed: %v", err)
	}

	// Schema guards: only 32-byte digests, CHANGE_EMAIL needs a target address.
	err := q.CreateAuthToken(ctx, store.CreateAuthTokenParams{
		ID: uuid.New(), UserID: u.ID, Purpose: "VERIFY_EMAIL", TokenDigest: []byte("raw-token-not-a-digest"), ExpiresAt: time.Now().Add(time.Hour),
	})
	expectPgError(t, err, codeCheckViolation, "auth_tokens_token_digest_len")
	err = q.CreateAuthToken(ctx, store.CreateAuthTokenParams{
		ID: uuid.New(), UserID: u.ID, Purpose: "CHANGE_EMAIL", TokenDigest: digest("change-1"), ExpiresAt: time.Now().Add(time.Hour),
	})
	expectPgError(t, err, codeCheckViolation, "auth_tokens_change_email_target")
}

// DB-04 (data part): disabling or revoking stops a session from authenticating.
func TestSessionValidity(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.NewMigratedPool(t)
	q := store.New(pool)
	u := newUser(t, q, "session@example.com")

	if err := q.CreateSession(ctx, store.CreateSessionParams{ID: uuid.New(), UserID: u.ID, TokenDigest: digest("s1"), ExpiresAt: time.Now().Add(14 * 24 * time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if _, err := q.GetActiveSessionUser(ctx, digest("s1")); err != nil {
		t.Fatalf("fresh session rejected: %v", err)
	}
	if _, err := q.SetUserStatus(ctx, store.SetUserStatusParams{ID: u.ID, Status: "DISABLED"}); err != nil {
		t.Fatal(err)
	}
	if _, err := q.GetActiveSessionUser(ctx, digest("s1")); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("disabled account still authenticated: %v", err)
	}
	if _, err := q.SetUserStatus(ctx, store.SetUserStatusParams{ID: u.ID, Status: "ACTIVE"}); err != nil {
		t.Fatal(err)
	}
	if n, err := q.RevokeUserSessions(ctx, u.ID); err != nil || n != 1 {
		t.Fatalf("revoke sessions = %d, %v", n, err)
	}
	if _, err := q.GetActiveSessionUser(ctx, digest("s1")); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("revoked session still authenticated: %v", err)
	}

	err := q.CreateSession(ctx, store.CreateSessionParams{ID: uuid.New(), UserID: uuid.New(), TokenDigest: digest("s2"), ExpiresAt: time.Now().Add(time.Hour)})
	expectPgError(t, err, codeForeignKeyViolation, "auth_sessions_user_id_fkey")
	err = q.CreateSession(ctx, store.CreateSessionParams{ID: uuid.New(), UserID: u.ID, TokenDigest: digest("s1"), ExpiresAt: time.Now().Add(time.Hour)})
	expectPgError(t, err, codeUniqueViolation, "auth_sessions_token_digest_key")
}

func TestRateWindowCountsAtomically(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.NewMigratedPool(t)
	q := store.New(pool)
	start := time.Now().Truncate(time.Minute)
	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := q.HitRateWindow(ctx, store.HitRateWindowParams{
				ActionKey: "LOGIN", SubjectDigest: digest("ip:203.0.113.7"), WindowStartedAt: start, ExpiresAt: start.Add(10 * time.Minute),
			}); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	var count int32
	if err := pool.QueryRow(ctx, `SELECT request_count FROM auth_rate_windows WHERE action_key = 'LOGIN'`).Scan(&count); err != nil || count != 20 {
		t.Fatalf("request_count = %d, %v; want 20", count, err)
	}
}
