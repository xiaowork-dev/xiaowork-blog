// Package auth implements accounts, sessions, CSRF protection, rate limiting and the P2 auth API.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// Argon2Params are encoded into every hash, so they can be raised later without breaking old hashes.
type Argon2Params struct {
	Memory  uint32 // KiB
	Time    uint32
	Threads uint8
	SaltLen uint32
	KeyLen  uint32
}

// DefaultArgon2 is the OWASP Password Storage baseline for Argon2id (19 MiB, t=2, p=1).
var DefaultArgon2 = Argon2Params{Memory: 19 * 1024, Time: 2, Threads: 1, SaltLen: 16, KeyLen: 32}

// PasswordHasher hashes with Argon2id and caps concurrent hashing so a burst of logins cannot
// exhaust memory (each hash allocates Params.Memory).
type PasswordHasher struct {
	params Argon2Params
	sem    chan struct{}
	dummy  string
}

func NewPasswordHasher(params Argon2Params, maxConcurrent int) *PasswordHasher {
	h := &PasswordHasher{params: params, sem: make(chan struct{}, maxConcurrent)}
	// Verifying against this hash keeps "unknown e-mail" as slow as "wrong password".
	h.dummy, _ = h.Hash(context.Background(), "dummy-password-for-timing-equalisation")
	return h
}

func (h *PasswordHasher) acquire(ctx context.Context) error {
	select {
	case h.sem <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (h *PasswordHasher) release() { <-h.sem }

// Hash returns $argon2id$v=19$m=..,t=..,p=..$salt$key (unpadded standard base64).
func (h *PasswordHasher) Hash(ctx context.Context, password string) (string, error) {
	if err := h.acquire(ctx); err != nil {
		return "", err
	}
	defer h.release()
	salt := make([]byte, h.params.SaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	p := h.params
	key := argon2.IDKey([]byte(password), salt, p.Time, p.Memory, p.Threads, p.KeyLen)
	enc := base64.RawStdEncoding
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, p.Memory, p.Time, p.Threads, enc.EncodeToString(salt), enc.EncodeToString(key)), nil
}

var errBadHash = errors.New("auth: unrecognised password hash")

// Verify reports whether password matches encoded, in constant time.
func (h *PasswordHasher) Verify(ctx context.Context, password, encoded string) (bool, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false, errBadHash
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return false, errBadHash
	}
	var p Argon2Params
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &p.Memory, &p.Time, &p.Threads); err != nil {
		return false, errBadHash
	}
	// Refuse absurd parameters rather than allocating them.
	if p.Memory < 8*1024 || p.Memory > 1024*1024 || p.Time < 1 || p.Time > 10 || p.Threads < 1 || p.Threads > 16 {
		return false, errBadHash
	}
	enc := base64.RawStdEncoding
	salt, err := enc.DecodeString(parts[4])
	if err != nil {
		return false, errBadHash
	}
	want, err := enc.DecodeString(parts[5])
	if err != nil || len(want) < 16 || len(want) > 64 {
		return false, errBadHash
	}
	if err := h.acquire(ctx); err != nil {
		return false, err
	}
	defer h.release()
	got := argon2.IDKey([]byte(password), salt, p.Time, p.Memory, p.Threads, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}

// VerifyDummy burns the same work as a real verification and always fails.
func (h *PasswordHasher) VerifyDummy(ctx context.Context, password string) {
	_, _ = h.Verify(ctx, password, h.dummy)
}
