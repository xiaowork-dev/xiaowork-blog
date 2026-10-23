package auth

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/xiaowork-dev/xiaowork-blog/apps/api/internal/store"
)

// ErrAdminExists is returned once any ADMIN account exists: V1.0 has a single blog owner and the
// first-admin path must not become a way to mint more administrators (PRD §3, SAD §6.3).
var ErrAdminExists = errors.New("an administrator account already exists")

// ErrInvalidAdminInput describes which field failed validation.
type ErrInvalidAdminInput struct{ Field, Problem string }

func (e ErrInvalidAdminInput) Error() string { return e.Field + ": " + e.Problem }

// CreateFirstAdmin creates the initial ADMIN with a verified e-mail. It is only reachable through the
// blog-admin command run by the operator — never through an HTTP endpoint. A transaction-scoped advisory
// lock serialises concurrent attempts so at most one admin is created.
func CreateFirstAdmin(ctx context.Context, pool *pgxpool.Pool, hasher *PasswordHasher, email, nickname, password string) (uuid.UUID, error) {
	display, normalized, ok := normalizeEmail(email)
	if !ok {
		return uuid.Nil, ErrInvalidAdminInput{"email", "INVALID"}
	}
	name, ok := validNickname(nickname)
	if !ok {
		return uuid.Nil, ErrInvalidAdminInput{"nickname", "INVALID_LENGTH"}
	}
	if p := passwordProblem(password); p != "" {
		return uuid.Nil, ErrInvalidAdminInput{"password", p}
	}
	hash, err := hasher.Hash(ctx, password)
	if err != nil {
		return uuid.Nil, err
	}
	id := uuid.New()
	err = pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('xiaowork-blog:first-admin'))`); err != nil {
			return err
		}
		q := store.New(tx)
		exists, err := q.AdminExists(ctx)
		if err != nil {
			return err
		}
		if exists {
			return ErrAdminExists
		}
		if _, err := q.CreateVerifiedAdmin(ctx, store.CreateVerifiedAdminParams{
			ID: id, Email: display, EmailNormalized: normalized, PasswordHash: hash, Nickname: name,
		}); err != nil {
			if isUniqueViolation(err, "users_email_normalized_key") {
				return ErrInvalidAdminInput{"email", "ALREADY_USED"}
			}
			return err
		}
		return q.CreateUserProfile(ctx, id)
	})
	if err != nil {
		return uuid.Nil, err
	}
	return id, nil
}
