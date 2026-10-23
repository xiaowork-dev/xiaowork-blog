//go:build linux || darwin

package media

import "syscall"

// freeBytes reports the space available to unprivileged users on the volume holding dir.
func freeBytes(dir string) (int64, bool) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(dir, &st); err != nil {
		return 0, false
	}
	return int64(st.Bavail) * int64(st.Bsize), true
}
