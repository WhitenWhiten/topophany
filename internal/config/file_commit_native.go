//go:build !js

package config

import (
	"crypto/sha256"
	"github.com/WhitenWhiten/topophany/internal/secure"
	"os"
	"path/filepath"
)

func CommitFile(path string, expected FileVersion, data []byte, mode os.FileMode) (FileVersion, error) {
	if err := secure.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return expected, err
	}
	unlock, err := secure.Lock(path + ".edit.lock")
	if err != nil {
		return expected, err
	}
	defer unlock()
	_, actual, err := ReadFileVersion(path)
	if err != nil {
		return expected, err
	}
	if actual != expected {
		return expected, ErrFileChanged
	}
	if err := secure.AtomicWrite(path, data, mode); err != nil {
		return expected, err
	}
	return FileVersion{true, sha256.Sum256(data)}, nil
}
