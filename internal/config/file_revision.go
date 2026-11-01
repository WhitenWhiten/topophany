package config

import (
	"crypto/sha256"
	"errors"
	"io"
	"os"
	"path/filepath"

	"github.com/WhitenWhiten/topophany/internal/secure"
)

// FileVersion compares exact bytes, including formatting. Cooperative topo
// writers serialize commit; changes made by other editors before commit are
// refused. An unrelated writer ignoring the advisory lock can still race it.
type FileVersion struct {
	Exists bool
	Digest [32]byte
}

var ErrFileChanged = errors.New("topology changed outside this edit; reopen it and review your changes before saving")

func ReadFileVersion(path string) ([]byte, FileVersion, error) {
	st, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, FileVersion{}, nil
	}
	if err != nil {
		return nil, FileVersion{}, err
	}
	if !st.Mode().IsRegular() {
		return nil, FileVersion{}, errors.New("editable topology must be a regular non-symlink file")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, FileVersion{}, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, 4<<20+1))
	if err != nil {
		return nil, FileVersion{}, err
	}
	if len(data) > 4<<20 {
		return nil, FileVersion{}, errors.New("topology exceeds 4 MiB")
	}
	return data, FileVersion{true, sha256.Sum256(data)}, nil
}
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
