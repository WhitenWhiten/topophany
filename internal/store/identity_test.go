package store

import "testing"

func TestCollectorIdentityPersistsAndDistinguishesStores(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	id := s.CollectorID()
	s.Close()
	s, err = Open(dir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	other, err := Open(t.TempDir(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	if len(id) != 64 || id != s.CollectorID() || id == other.CollectorID() {
		t.Fatal("unstable or duplicate identity")
	}
}
