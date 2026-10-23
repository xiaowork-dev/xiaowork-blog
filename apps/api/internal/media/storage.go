package media

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Storage lays files out under the media root (SAD §8.1):
//
//	originals/<2 hex>/<uuid>.<ext>    validated uploads, byte-for-byte as received
//	thumbnails/<2 hex>/<uuid>.<ext>   generated previews of still images
//	temp/                             in-flight uploads; same filesystem so the final move is an atomic rename
//
// Keys are generated from random UUIDs only, so client file names never influence paths. The rename/remove
// hooks exist so tests can simulate disk failures.
type Storage struct {
	root   string
	rename func(oldpath, newpath string) error
	remove func(name string) error
}

func NewStorage(root string) *Storage {
	return &Storage{root: root, rename: os.Rename, remove: os.Remove}
}

// Init creates the directory layout and proves it is writable, so a mis-mounted or read-only volume
// fails at startup instead of on the first upload.
func (s *Storage) Init() error {
	for _, d := range []string{"originals", "thumbnails", "temp"} {
		if err := os.MkdirAll(filepath.Join(s.root, d), 0o750); err != nil {
			return fmt.Errorf("media root not usable: %w", err)
		}
	}
	probe, err := os.CreateTemp(filepath.Join(s.root, "temp"), "probe-*")
	if err != nil {
		return fmt.Errorf("media root not writable: %w", err)
	}
	name := probe.Name()
	_ = probe.Close()
	return os.Remove(name)
}

func (s *Storage) newTemp(pattern string) (*os.File, error) {
	return os.CreateTemp(filepath.Join(s.root, "temp"), pattern) // O_EXCL, mode 0600
}

func originalKey(id uuid.UUID, ext string) string {
	v := id.String()
	return "originals/" + v[:2] + "/" + v + "." + ext
}

// thumbKey derives the thumbnail location from the original key; "" when the format has none.
func thumbKey(storageKey, mime string) string {
	fm, ok := formats[mime]
	if !ok || fm.thumbExt == "" || !strings.HasPrefix(storageKey, "originals/") {
		return ""
	}
	base := strings.TrimPrefix(storageKey, "originals/")
	return "thumbnails/" + strings.TrimSuffix(base, path.Ext(base)) + "." + fm.thumbExt
}

var validKey = regexp.MustCompile(`^(originals|thumbnails)/[0-9a-f]{2}/[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\.(jpg|png|webp|gif)$`)

// path resolves a key to a file path, refusing anything that is not a generated key or that would
// escape the root (defence in depth on top of the DB CHECK constraint).
func (s *Storage) path(key string) (string, error) {
	if !validKey.MatchString(key) {
		return "", fmt.Errorf("invalid storage key %q", key)
	}
	p := filepath.Join(s.root, filepath.FromSlash(key))
	if rel, err := filepath.Rel(s.root, p); err != nil || strings.HasPrefix(rel, "..") {
		return "", fmt.Errorf("storage key escapes root: %q", key)
	}
	return p, nil
}

// commit atomically moves a finished temp file to its final key and flushes the directory entry.
func (s *Storage) commit(tempPath, key string) error {
	final, err := s.path(key)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(final), 0o750); err != nil {
		return err
	}
	if _, err := os.Lstat(final); err == nil {
		return fmt.Errorf("refusing to overwrite existing media file %s", key)
	}
	if err := s.rename(tempPath, final); err != nil {
		return err
	}
	syncDir(filepath.Dir(final))
	return nil
}

// delete removes a stored file; a file that is already gone counts as removed (idempotent retries).
func (s *Storage) delete(key string) error {
	if key == "" {
		return nil
	}
	p, err := s.path(key)
	if err != nil {
		return err
	}
	if err := s.remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

func (s *Storage) open(key string) (*os.File, error) {
	p, err := s.path(key)
	if err != nil {
		return nil, err
	}
	return os.Open(p)
}

// cleanTemp removes abandoned uploads (crashes, cancelled requests) older than maxAge.
func (s *Storage) cleanTemp(maxAge time.Duration) (int, error) {
	entries, err := os.ReadDir(filepath.Join(s.root, "temp"))
	if err != nil {
		return 0, err
	}
	removed := 0
	for _, e := range entries {
		info, err := e.Info()
		if err != nil || e.IsDir() || time.Since(info.ModTime()) < maxAge {
			continue
		}
		if os.Remove(filepath.Join(s.root, "temp", e.Name())) == nil {
			removed++
		}
	}
	return removed, nil
}

// listOriginals returns every key under originals/ (for the consistency report).
func (s *Storage) listOriginals() ([]string, error) {
	var keys []string
	base := filepath.Join(s.root, "originals")
	err := filepath.WalkDir(base, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(s.root, p)
		if err != nil {
			return err
		}
		keys = append(keys, filepath.ToSlash(rel))
		return nil
	})
	sort.Strings(keys)
	return keys, err
}

func (s *Storage) exists(key string) bool {
	p, err := s.path(key)
	if err != nil {
		return false
	}
	_, err = os.Stat(p)
	return err == nil
}

func fileSHA256(p string) ([]byte, error) {
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return nil, err
	}
	return h.Sum(nil), nil
}

// syncDir flushes a directory entry after a rename (best effort; not supported on every platform).
func syncDir(dir string) {
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
}
