package media

import (
	"context"

	"github.com/google/uuid"
)

// Test-only access for the external media_test package.

// SetFailures swaps the filesystem operations used to move and remove files (disk failure simulation).
func SetFailures(s *Storage, rename func(oldpath, newpath string) error, remove func(name string) error) {
	if rename != nil {
		s.rename = rename
	}
	if remove != nil {
		s.remove = remove
	}
}

// FinalizeDelete runs only the second phase of the delete state machine.
func (s *Service) FinalizeDelete(ctx context.Context, id uuid.UUID) error {
	return s.finalizeDelete(ctx, id)
}
