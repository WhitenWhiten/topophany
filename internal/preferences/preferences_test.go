package preferences

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultsPersistenceAndValidation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "preferences.json")
	s, err := Load(path)
	if err != nil || s != Defaults() {
		t.Fatal(s, err)
	}
	if _, err = os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("read created a preferences file")
	}
	s.Page = "servers"
	s.UniformCards = true
	s.ServiceCards = true
	s.GroupsCollapsed = true
	s.FullMap = true
	s.MapMode = "network"
	if err = Save(path, s); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil || got != s {
		t.Fatal(got, err)
	}
	// The network, the mode before there were modes, is saved as none, so
	// older binaries still read the file; products is saved by name.
	raw, err := os.ReadFile(path)
	if err != nil || strings.Contains(string(raw), `"mode"`) {
		t.Fatalf("the network mode is written down: %s %v", raw, err)
	}
	s.Mode = "products"
	if err = Save(path, s); err != nil {
		t.Fatal(err)
	}
	if got, err = Load(path); err != nil || got != s || !got.Products() {
		t.Fatal(got, err)
	}
	if raw, _ = os.ReadFile(path); !strings.Contains(string(raw), `"mode": "products"`) {
		t.Fatalf("products is not saved: %s", raw)
	}
	for _, raw := range []string{`{"mode":"builder"}`, `{"page":"bogus"}`, `{"map_mode":"bogus"}`, `{"unknown":true}`, `{"uniform_cards":"yes"}`, `{"service_cards":1}`, `{} {}`} {
		if err = os.WriteFile(path, []byte(raw), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err = Load(path); err == nil {
			t.Fatal("accepted", raw)
		}
	}
	if err = Save(path, Defaults()); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, []byte(`{"page":"checks"}`), 0600); err != nil {
		t.Fatal(err)
	}
	got, err = Load(path)
	if err != nil || got.Page != "checks" || got.MapMode != "auto" || got.Mode != "network" {
		t.Fatal(got, err)
	}
}
