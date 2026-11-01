package secure

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
)

func TestTokensAndMissingSecrets(t *testing.T) {
	a, e := Token()
	if e != nil {
		t.Fatal(e)
	}
	b, e := Token()
	if e != nil || a == b || len(a) != 64 || len(Hash(a)) != 64 {
		t.Fatal("bad random token")
	}
	if CheckToken(a) != nil {
		t.Fatal("generated token rejected")
	}
	for _, s := range []string{"short", strings.Repeat("x", 513), strings.Repeat("x", 40) + "\n"} {
		if CheckToken(s) == nil {
			t.Fatal("bad token accepted")
		}
	}
	t.Setenv("TOPOPHANY_TEST_MISSING", "")
	if _, e = SecretEnv("TOPOPHANY_TEST_MISSING"); e == nil {
		t.Fatal("missing secret silently accepted")
	}
	if v, e := SecretEnv(""); e != nil || v != "" {
		t.Fatal("optional secret should be empty")
	}
}
func TestPrivateAtomicCredentialFiles(t *testing.T) {
	p := filepath.Join(t.TempDir(), "private", "credentials.json")
	if e := AtomicWrite(p, []byte(`{"token":"a"}`), 0600); e != nil {
		t.Fatal(e)
	}
	got, e := PrivateRead(p)
	if e != nil || string(got) != `{"token":"a"}` {
		t.Fatal(string(got), e)
	}
	if runtime.GOOS != "windows" {
		if e = os.Chmod(p, 0644); e != nil {
			t.Fatal(e)
		}
		if _, e = PrivateRead(p); e == nil {
			t.Fatal("world-readable credentials accepted")
		}
		_ = os.Chmod(p, 0600)
	}
	link := filepath.Join(t.TempDir(), "link")
	if e = os.Symlink(p, link); e == nil {
		if _, e = PrivateRead(link); e == nil {
			t.Fatal("symlink read")
		}
		if e = AtomicWrite(link, []byte("x"), 0600); e == nil {
			t.Fatal("symlink replaced")
		}
	}
	if e = AtomicWrite(p, []byte(`{"token":"b"}`), 0600); e != nil {
		t.Fatal(e)
	}
	b, e := PrivateRead(p)
	if e != nil || string(b) != `{"token":"b"}` {
		t.Fatal("atomic replacement failed", e)
	}
}
func TestConcurrentAtomicWritesAreWhole(t *testing.T) {
	p := filepath.Join(t.TempDir(), "state.json")
	var wg sync.WaitGroup
	errs := make(chan error, 16)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			b, _ := json.Marshal(map[string]any{"n": n, "padding": strings.Repeat("x", 8192)})
			errs <- AtomicWrite(p, b, 0600)
		}(i)
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
	b, e := os.ReadFile(p)
	if e != nil || !json.Valid(b) {
		t.Fatal("torn atomic file", e)
	}
}
func TestTransportPolicyAndStrictJSON(t *testing.T) {
	for _, s := range []string{"https://example.invalid", "http://127.0.0.1:1234", "http://[::1]:1"} {
		if e := URL(s, false); e != nil {
			t.Fatal(s, e)
		}
	}
	for _, s := range []string{"http://example.invalid", "http://localhost:1", "https://u:p@example.invalid", "https://example.invalid?secret=x", "ftp://127.0.0.1"} {
		if e := URL(s, false); e == nil {
			t.Fatal("unsafe URL accepted", s)
		}
	}
	if e := URL("http://192.0.2.1:1", true); e != nil {
		t.Fatal(e)
	}
	var v struct {
		N int `json:"n"`
	}
	for _, bad := range []string{`{"unexpected":1}`, `{"n":1}{"n":2}`, strings.Repeat("x", 100)} {
		if e := Decode(strings.NewReader(bad), &v, 50); e == nil {
			t.Fatal("bad JSON accepted")
		}
	}
	if e := Decode(bytes.NewBufferString(`{"n":2}`), &v, 50); e != nil || v.N != 2 {
		t.Fatal(e)
	}
}
func TestTextBlocksTerminalControlAndBidi(t *testing.T) {
	s := Text("ok\x1b[2J\n\r\t\u202eABC\u2066x\u2069")
	if strings.ContainsAny(s, "\x1b\n\r\t\u202e\u2066\u2069") {
		t.Fatal("terminal control survived")
	}
	if Text("家庭网络 → 服务") != "家庭网络 → 服务" {
		t.Fatal("normal CJK text modified")
	}
}
func TestDirectoryLock(t *testing.T) {
	p := filepath.Join(t.TempDir(), "writer.lock")
	unlock, e := Lock(p)
	if e != nil {
		t.Fatal(e)
	}
	if other, e := Lock(p); e == nil {
		other()
		t.Fatal("second writer acquired lock")
	} else if !errors.Is(e, ErrLocked) {
		t.Fatalf("lock contention reported as %v", e)
	}
	if e = unlock(); e != nil {
		t.Fatal(e)
	}
	again, e := Lock(p)
	if e != nil {
		t.Fatal(e)
	}
	again()
}

func TestReadBoundedAndCredentialBudgets(t *testing.T) {
	p := filepath.Join(t.TempDir(), "data")
	if e := os.WriteFile(p, []byte("12345"), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e := ReadBounded(p, 4); e == nil {
		t.Fatal("size budget bypass")
	}
	if b, e := ReadBounded(p, 5); e != nil || string(b) != "12345" {
		t.Fatal(e)
	}
	if _, e := ReadBounded(filepath.Dir(p), 100); e == nil {
		t.Fatal("nonregular file accepted")
	}
	if _, e := ReadBounded(p+"missing", 10); e == nil {
		t.Fatal("missing file")
	}
	if e := os.WriteFile(p, make([]byte, (1<<20)+1), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e := PrivateRead(p); e == nil {
		t.Fatal("large credential accepted")
	}
	for _, s := range []string{strings.Repeat("x", 40) + "\t", strings.Repeat("x", 40) + " ", strings.Repeat("x", 40) + "\x1b"} {
		if CheckToken(s) == nil {
			t.Fatal("control token accepted")
		}
	}
}

// MkdirAll syncs each directory that gained an entry, the one it already
// found as well as those it made, so what is written durably under the
// new directory can be found after a power loss; with nothing to make it
// syncs nothing.
func TestMkdirAllSyncsWhatItChanged(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not sync directories")
	}
	base := t.TempDir()
	var synced []string
	stop := WatchSyncs(func(dir string) { synced = append(synced, dir) })
	defer stop()
	path := filepath.Join(base, "a", "b", "c")
	if e := MkdirAll(path, 0700); e != nil {
		t.Fatal(e)
	}
	if want := []string{base, filepath.Join(base, "a"), filepath.Join(base, "a", "b")}; !slices.Equal(synced, want) {
		t.Fatal(synced, want)
	}
	if s, e := os.Stat(path); e != nil || !s.IsDir() || s.Mode().Perm() != 0700 {
		t.Fatal(s, e)
	}
	synced = nil
	if e := MkdirAll(path, 0700); e != nil || len(synced) != 0 {
		t.Fatal("an existing directory synced", synced, e)
	}
	if e := AtomicWrite(filepath.Join(base, "d", "file"), []byte("x"), 0600); e != nil {
		t.Fatal(e)
	}
	if want := []string{base, filepath.Join(base, "d")}; !slices.Equal(synced, want) {
		t.Fatal("a write into a new directory", synced, want)
	}
	file := filepath.Join(base, "file")
	if e := os.WriteFile(file, nil, 0600); e != nil {
		t.Fatal(e)
	}
	if e := MkdirAll(filepath.Join(file, "x"), 0700); e == nil {
		t.Fatal("made a directory under a file")
	}
	if e := MkdirAll(file, 0700); e == nil {
		t.Fatal("took a file for a directory")
	}
}
