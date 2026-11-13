package management

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"

	"github.com/WhitenWhiten/topophany/internal/secure"
)

// ReadLocal reads only bounded regular files in trusted directory chains.
// system policy is administrator-owned; user policy trusts the invoking user.
func ReadLocal(path string, limit int64, system, secret bool) ([]byte, error) {
	if !filepath.IsAbs(path) {
		return nil, errors.New("management_path_must_be_absolute")
	}
	if err := trustedFile(path, system, secret); err != nil {
		return nil, err
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	a, err := f.Stat()
	if err != nil {
		return nil, err
	}
	b, err := os.Lstat(path)
	if err != nil || !a.Mode().IsRegular() || !os.SameFile(a, b) {
		return nil, errors.New("management_file_changed")
	}
	// Parents are trusted and not writable by an untrusted identity.
	return readLimit(f, limit)
}
func trustedFile(path string, system, secret bool) error {
	return trustedPath(path, true, system, secret)
}
func trustedPath(path string, file, system, secret bool) error {
	for p := filepath.Clean(path); ; p = filepath.Dir(p) {
		st, err := os.Lstat(p)
		if err != nil {
			return err
		}
		if st.Mode()&os.ModeSymlink != 0 {
			if runtime.GOOS != "darwin" || (p != "/var" && p != "/etc" && p != "/tmp") {
				return errors.New("management_symlink_refused")
			}
			if err = checkOwner(st, true, false); err != nil {
				return err
			}
			resolved, err := filepath.EvalSymlinks(p)
			if err != nil {
				return err
			}
			st, err = os.Stat(resolved)
			if err != nil {
				return err
			}
		}
		if file && p == filepath.Clean(path) {
			if !st.Mode().IsRegular() {
				return errors.New("management_regular_file_required")
			}
			if err = checkOwner(st, system, secret); err != nil {
				return err
			}
		} else {
			if !st.IsDir() {
				return errors.New("management_parent_not_directory")
			}
			// A root-owned sticky temporary parent cannot replace another user's child.
			if err = checkOwner(st, system, false); err != nil {
				return err
			}
		}
		if filepath.Dir(p) == p {
			break
		}
	}
	return nil
}
func WriteLocal(path string, b []byte) error {
	if !filepath.IsAbs(path) {
		return errors.New("management_path_must_be_absolute")
	}
	// The parent must already have been explicitly created/enrolled locally.
	marker := filepath.Dir(path)
	st, err := os.Stat(marker)
	if err != nil || !st.IsDir() {
		return errors.New("management_directory_missing")
	}
	if err = trustedPath(marker, false, false, false); err != nil {
		return err
	}
	if _, err = os.Lstat(path); err == nil {
		if err = trustedFile(path, false, true); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	return secure.AtomicWrite(path, b, 0600)
}

// ValidateLocalFile exposes the same ownership boundary to the updater without
// reading an entire executable into memory.
func ValidateLocalFile(path string, system, secret bool) error {
	return trustedFile(path, system, secret)
}
func ValidateDirectory(path string, system bool) error {
	return trustedPath(path, false, system, false)
}
func MakePrivateDirectory(path string, system bool) error {
	if !filepath.IsAbs(path) {
		return errors.New("management_path_must_be_absolute")
	}
	existing := filepath.Clean(path)
	missing := []string{}
	for {
		_, e := os.Lstat(existing)
		if e == nil {
			break
		}
		if !os.IsNotExist(e) {
			return e
		}
		missing = append(missing, filepath.Base(existing))
		next := filepath.Dir(existing)
		if next == existing {
			return errors.New("management_parent_missing")
		}
		existing = next
	}
	if e := trustedPath(existing, false, system, false); e != nil {
		return e
	}
	root, e := os.OpenRoot(existing)
	if e != nil {
		return e
	}
	defer func() { root.Close() }()
	for i := len(missing) - 1; i >= 0; i-- {
		name := missing[i]
		if e = root.Mkdir(name, 0700); e != nil && !os.IsExist(e) {
			return e
		}
		st, e := root.Lstat(name)
		if e != nil {
			return e
		}
		if !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
			return errors.New("management_directory_replaced")
		}
		if e = checkOwner(st, system, false); e != nil {
			return e
		}
		parent, e := root.Open(".")
		if e != nil {
			return e
		}
		e = parent.Sync()
		parent.Close()
		if e != nil {
			return e
		}
		child, e := root.OpenRoot(name)
		if e != nil {
			return e
		}
		root.Close()
		root = child
	}
	return trustedPath(path, false, system, false)
}
func directory(path string) string { return filepath.Dir(path) }
