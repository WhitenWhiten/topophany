//go:build linux || darwin

package secure

import (
	"errors"
	"os"
	"syscall"
)

// Lock is advisory and intentionally held for the lifetime of the process.
// The lock file is never unlinked (unlinking would allow a second inode lock).
func Lock(path string) (func() error, error) {
	if s, e := os.Lstat(path); e == nil && !s.Mode().IsRegular() {
		return nil, errors.New("lock path is not a regular file")
	}
	f, e := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if e != nil {
		return nil, e
	}
	if e = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); e != nil {
		f.Close()
		if errors.Is(e, syscall.EWOULDBLOCK) {
			return nil, ErrLocked
		}
		return nil, e
	}
	return func() error { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN); return f.Close() }, nil
}
