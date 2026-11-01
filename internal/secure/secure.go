// Package secure contains small shared security primitives. Tokens are random
// bearer secrets, not passwords. Their hashes can be stored on the collector.
package secure

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/WhitenWhiten/topophany/internal/ioaccount"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"time"
	"unicode"
)

// ErrLocked means another process holds the requested lock.
var ErrLocked = errors.New("data directory is locked by another process")

func Token() (string, error) {
	b := make([]byte, 32)
	_, e := rand.Read(b)
	return hex.EncodeToString(b), e
}
func Hash(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }
func SecretEnv(name string) (string, error) {
	if name == "" {
		return "", nil
	}
	s, ok := os.LookupEnv(name)
	if !ok || s == "" {
		return "", errors.New("configured secret environment variable is missing")
	}
	return s, nil
}
func Decode(r io.Reader, v any, limit int64) error {
	b, e := io.ReadAll(io.LimitReader(r, limit+1))
	if e != nil {
		return e
	}
	if int64(len(b)) > limit {
		return errors.New("input limit exceeded")
	}
	if e := JSONBudget(b); e != nil {
		return e
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if e = d.Decode(v); e != nil {
		return e
	}
	var more any
	if e = d.Decode(&more); e != io.EOF {
		return errors.New("expected one JSON value")
	}
	return nil
}

// PrivateRead rejects symlink files and permissive POSIX modes. Windows ACLs
// are inherited from the per-user config directory and must be reviewed there.
func PrivateRead(path string) ([]byte, error) {
	s, e := os.Lstat(path)
	if e != nil {
		return nil, e
	}
	if !s.Mode().IsRegular() {
		return nil, errors.New("credential must be a regular non-symlink file")
	}
	if runtime.GOOS != "windows" && s.Mode().Perm()&0077 != 0 {
		return nil, errors.New("credential permissions must be 0600 or stricter")
	}
	if s.Size() > 1<<20 {
		return nil, errors.New("credential file too large")
	}
	return ReadBounded(path, 1<<20)
}
func AtomicWrite(path string, b []byte, mode os.FileMode) error {
	return atomicWrite(path, b, mode, true, nil)
}

// CacheWrite replaces rebuildable data without forcing stable storage. Durable
// callers must continue to use AtomicWrite.
func CacheWrite(path string, b []byte, mode os.FileMode) error {
	return atomicWrite(path, b, mode, false, nil)
}

// AtomicWriteMeasured retains AtomicWrite's durability and counts bytes
// actually written to the temporary file, even if replacement later fails.
func AtomicWriteMeasured(path string, b []byte, mode os.FileMode, counter *ioaccount.Counter) error {
	return atomicWrite(path, b, mode, true, counter)
}

// AtomicWriteStreamMeasured publishes a streamed file only after write succeeds
// and the temporary file is synced. Seek permits patching a checksum without
// buffering the whole file; all written bytes, including patches, are counted.
func AtomicWriteStreamMeasured(path string, mode os.FileMode, counter *ioaccount.Counter, write func(io.WriteSeeker) error) error {
	return atomicWriteFunc(path, mode, true, counter, write)
}

type measuredStream struct {
	io.WriteSeeker
	written *int
}

func (w measuredStream) Write(p []byte) (int, error) {
	n, err := w.WriteSeeker.Write(p)
	*w.written += n
	if err == nil && n != len(p) {
		err = io.ErrShortWrite
	}
	return n, err
}

func atomicWrite(path string, b []byte, mode os.FileMode, durable bool, counter *ioaccount.Counter) (result error) {
	return atomicWriteFunc(path, mode, durable, counter, func(w io.WriteSeeker) error {
		_, err := w.Write(b)
		return err
	})
}

func atomicWriteFunc(path string, mode os.FileMode, durable bool, counter *ioaccount.Counter, write func(io.WriteSeeker) error) (result error) {
	started := time.Now()
	written := 0
	if counter != nil {
		defer func() { counter.Add(written, time.Since(started), result) }()
	}
	dir := filepath.Dir(path)
	mkdir := os.MkdirAll
	if durable {
		mkdir = MkdirAll
	}
	if e := mkdir(dir, 0700); e != nil {
		return e
	}
	if s, e := os.Lstat(path); e == nil && !s.Mode().IsRegular() {
		return errors.New("refusing to replace non-regular file")
	}
	f, e := os.CreateTemp(dir, ".topophany-*")
	if e != nil {
		return e
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if e = f.Chmod(mode); e == nil {
		e = write(measuredStream{f, &written})
	}
	if e == nil && durable {
		e = ioaccount.Sync(f)
	}
	ce := f.Close()
	if e == nil {
		e = ce
	}
	if e != nil {
		return e
	}
	if e = replaceFile(tmp, path); e != nil {
		return e
	}
	if durable {
		return SyncDir(dir)
	}
	return nil
}

// MkdirAll makes path and the parents it lacks, as os.MkdirAll does, and
// syncs each directory that gained an entry, from the one it found down,
// so that what is then written durably under path can be found after a
// power loss: syncing a file or a directory does not make the entry that
// names it durable. With nothing to make, it syncs nothing.
func MkdirAll(path string, perm os.FileMode) error {
	made := []string{}
	for p := filepath.Clean(path); ; {
		s, e := os.Stat(p)
		if e == nil && !s.IsDir() {
			return &os.PathError{Op: "mkdir", Path: p, Err: errors.New("not a directory")}
		}
		if e == nil {
			break
		}
		if !errors.Is(e, os.ErrNotExist) {
			return e
		}
		made = append(made, p)
		parent := filepath.Dir(p)
		if parent == p {
			break
		}
		p = parent
	}
	if len(made) == 0 {
		return nil
	}
	if e := os.MkdirAll(path, perm); e != nil {
		return e
	}
	for i := len(made) - 1; i >= 0; i-- {
		if e := SyncDir(filepath.Dir(made[i])); e != nil {
			return e
		}
	}
	return nil
}

// synced is told of each directory SyncDir syncs; see WatchSyncs.
var synced atomic.Pointer[func(dir string)]

// WatchSyncs has f told of each directory SyncDir syncs until stop is
// called. Whether an entry survives a power loss cannot be seen without
// one, so tests use it to see what a write made durable.
func WatchSyncs(f func(dir string)) (stop func()) {
	synced.Store(&f)
	return func() { synced.Store(nil) }
}

func SyncDir(path string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	f, e := os.Open(path)
	if e != nil {
		return e
	}
	defer f.Close()
	started := time.Now()
	e = f.Sync()
	ioaccount.DirectorySync.Add(0, time.Since(started), e)
	if e != nil {
		return e
	}
	if f := synced.Load(); f != nil {
		(*f)(path)
	}
	return nil
}
func URL(raw string, insecure bool) error {
	u, e := url.Parse(raw)
	if e != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return errors.New("invalid collector URL")
	}
	if u.Scheme == "https" {
		return nil
	}
	ip := net.ParseIP(u.Hostname())
	if u.Scheme == "http" && (insecure || (ip != nil && ip.IsLoopback())) {
		return nil
	}
	return errors.New("HTTPS required except literal loopback; insecure HTTP requires explicit consent")
}
func Text(s string) string {
	var b strings.Builder
	for _, r := range s {
		if unicode.IsControl(r) || unicode.Is(unicode.Bidi_Control, r) {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// Terminal writes to w what Text leaves of each write, newlines and tabs
// kept, so that nothing a process says on its terminal, such as an error
// or a log line naming a value from a file it read or a peer it asked,
// carries a control or bidi rune there.
func Terminal(w io.Writer) io.Writer { return terminal{w} }

type terminal struct{ w io.Writer }

func (t terminal) Write(p []byte) (int, error) {
	var b strings.Builder
	for _, r := range string(p) {
		if r == '\n' || r == '\t' || !unicode.IsControl(r) && !unicode.Is(unicode.Bidi_Control, r) {
			b.WriteRune(r)
		}
	}
	if _, e := io.WriteString(t.w, b.String()); e != nil {
		return 0, e
	}
	return len(p), nil
}
func ErrorCode(e error) string {
	if e == nil {
		return ""
	}
	var unknownCA x509.UnknownAuthorityError
	if errors.As(e, &unknownCA) {
		return "tls_unknown_authority"
	}
	var hostname x509.HostnameError
	if errors.As(e, &hostname) {
		return "tls_name_mismatch"
	}
	var invalidCert x509.CertificateInvalidError
	if errors.As(e, &invalidCert) {
		return "tls_certificate_invalid"
	}
	var ne net.Error
	if errors.As(e, &ne) && ne.Timeout() {
		return "timeout"
	}
	if errors.Is(e, os.ErrPermission) {
		return "permission_denied"
	}
	return "network_error"
}
func CheckToken(s string) error {
	if len(s) < 32 || len(s) > 512 || strings.IndexFunc(s, func(r rune) bool { return unicode.IsControl(r) || unicode.IsSpace(r) }) >= 0 {
		return fmt.Errorf("token must be 32..512 characters without control delimiters")
	}
	return nil
}

// ReadBounded checks a regular file before reading and still limits the stream
// if its size changes concurrently. It must not block on FIFOs/devices.
func ReadBounded(path string, limit int64) ([]byte, error) {
	st, e := os.Stat(path)
	if e != nil {
		return nil, e
	}
	if !st.Mode().IsRegular() {
		return nil, errors.New("expected regular file")
	}
	if st.Size() > limit {
		return nil, errors.New("file limit exceeded")
	}
	f, e := os.Open(path)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	// Checkpoints can be tens of MiB. Use the known size instead of
	// geometrically growing ReadAll's buffer and briefly retaining both copies.
	b := make([]byte, st.Size())
	n, e := io.ReadFull(f, b)
	if e == io.EOF || e == io.ErrUnexpectedEOF {
		return b[:n], nil
	}
	if e != nil {
		return nil, e
	}
	extra, e := io.ReadAll(io.LimitReader(f, limit-int64(len(b))+1))
	if e != nil {
		return nil, e
	}
	if int64(len(b))+int64(len(extra)) > limit {
		return nil, errors.New("file limit exceeded")
	}
	b = append(b, extra...)
	return b, nil
}
