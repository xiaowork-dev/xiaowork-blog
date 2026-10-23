//go:build !linux && !darwin

package media

// freeBytes is not implemented on this platform (local Windows development); the check is skipped.
func freeBytes(string) (int64, bool) { return 0, false }
