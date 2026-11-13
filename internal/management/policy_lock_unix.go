//go:build linux || darwin

package management

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"

	"github.com/WhitenWhiten/topophany/internal/secure"
)

// PolicyLease serializes the final authorization/commit boundary with local
// enable/disable. The public lock carries no secret and is never unlinked.
func PolicyLease(path string) (func(), error) {
	lock := path + ".lock"
	if e := ValidateLocalFile(lock, false, false); e != nil {
		return nil, e
	}
	f, e := os.Open(lock)
	if e != nil {
		return nil, e
	}
	if e = syscall.Flock(int(f.Fd()), syscall.LOCK_SH); e != nil {
		f.Close()
		return nil, e
	}
	return func() { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN); f.Close() }, nil
}
func WritePolicy(path string, p Policy) error {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return errors.New("management_path_must_be_absolute")
	}
	if e := p.Validate(); e != nil {
		return e
	}
	if e := ValidateDirectory(directory(path), p.System); e != nil {
		return e
	}
	lock := path + ".lock"
	if _, e := os.Lstat(lock); e == nil {
		if e = ValidateLocalFile(lock, p.System, false); e != nil {
			return e
		}
	} else if !os.IsNotExist(e) {
		return e
	}
	f, e := os.OpenFile(lock, os.O_CREATE|os.O_RDWR, 0644)
	if e != nil {
		return e
	}
	defer f.Close()
	if e = syscall.Flock(int(f.Fd()), syscall.LOCK_EX); e != nil {
		return e
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	if old, e := LoadPolicy(path); e == nil && old.Revision >= p.Revision && old.Installation != "" {
		return errors.New("local_policy_revision_conflict")
	}
	mode := os.FileMode(0600)
	var owner *syscall.Stat_t
	if st, e := os.Lstat(path); e == nil {
		if e = ValidateLocalFile(path, p.System, false); e != nil {
			return e
		}
		mode = st.Mode().Perm()
		owner, _ = st.Sys().(*syscall.Stat_t)
	}
	temp, e := os.CreateTemp(filepath.Dir(path), ".topophany-policy-*")
	if e != nil {
		return e
	}
	defer os.Remove(temp.Name())
	if owner != nil {
		e = temp.Chown(int(owner.Uid), int(owner.Gid))
	}
	if e == nil {
		e = temp.Chmod(mode)
	}
	if e == nil {
		_, e = temp.Write(Marshal(p))
	}
	if e == nil {
		e = temp.Sync()
	}
	closeErr := temp.Close()
	if e == nil {
		e = closeErr
	}
	if e != nil {
		return e
	}
	if e = os.Rename(temp.Name(), path); e != nil {
		return e
	}
	return secure.SyncDir(filepath.Dir(path))
}
