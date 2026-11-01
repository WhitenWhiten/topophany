package secure

import (
	"errors"
	"os"
	"syscall"
	"unsafe"
)

var lockFileEx = syscall.NewLazyDLL("kernel32.dll").NewProc("LockFileEx")
var unlockFileEx = syscall.NewLazyDLL("kernel32.dll").NewProc("UnlockFileEx")

func Lock(path string) (func() error, error) {
	if s, e := os.Lstat(path); e == nil && !s.Mode().IsRegular() {
		return nil, errors.New("lock path is not a regular file")
	}
	f, e := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if e != nil {
		return nil, e
	}
	ov := new(syscall.Overlapped)
	r, _, lockErr := lockFileEx.Call(f.Fd(), 3, 0, 1, 0, uintptr(unsafe.Pointer(ov)))
	if r == 0 {
		f.Close()
		if errors.Is(lockErr, syscall.Errno(33)) { // ERROR_LOCK_VIOLATION
			return nil, ErrLocked
		}
		return nil, lockErr
	}
	return func() error {
		_, _, _ = unlockFileEx.Call(f.Fd(), 0, 1, 0, uintptr(unsafe.Pointer(ov)))
		return f.Close()
	}, nil
}
