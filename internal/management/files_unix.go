//go:build linux || darwin

package management

import (
	"errors"
	"os"
	"syscall"
)

func checkOwner(s os.FileInfo, system, secret bool) error {
	st, ok := s.Sys().(*syscall.Stat_t)
	if !ok {
		return errors.New("management_owner_unavailable")
	}
	if st.Uid != 0 && (system || int(st.Uid) != os.Geteuid()) {
		return errors.New("management_untrusted_owner")
	}
	if s.Mode()&os.ModeSymlink == 0 && s.Mode().Perm()&0022 != 0 && !(s.IsDir() && s.Mode()&os.ModeSticky != 0 && st.Uid == 0) {
		return errors.New("management_writable_by_others")
	}
	if secret && s.Mode().Perm()&0077 != 0 {
		return errors.New("management_secret_permissions")
	}
	return nil
}
