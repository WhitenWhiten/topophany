package session

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
	"time"
)

func TestPrivateSessionLeaseAndLifecycle(t *testing.T) {
	root := filepath.Join(t.TempDir(), "sessions")
	w, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	state := State{Project: "lab", Observer: "wsl", Events: []Event{{At: now, Kind: "connection_error", Code: "tls_timeout"}}}
	if err = w.Publish(state, now); err != nil {
		t.Fatal(err)
	}
	got, err := Read(root, w.ID(), now)
	if err != nil || !got.Live || !reflect.DeepEqual(got.Events, state.Events) {
		t.Fatal(got, err)
	}
	info, err := os.Stat(filepath.Join(root, w.ID()+".json"))
	if err != nil {
		t.Fatal(info, err)
	}
	// Windows uses ACLs; FileMode does not expose POSIX permission bits.
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0600 {
		t.Fatalf("session not written privately: %v", info.Mode())
	}
	list, err := List(root, now)
	if err != nil || len(list) != 1 {
		t.Fatal(list, err)
	}
	got, err = Read(root, w.ID(), now.Add(11*time.Second))
	if err != nil || got.Live {
		t.Fatal(got, err)
	}
	if _, err = Read(root, "../credentials", now); err == nil {
		t.Fatal("traversal accepted")
	}
	w.Close()
	list, err = List(root, now)
	if err != nil || len(list) != 0 {
		t.Fatal(list, err)
	}
}
