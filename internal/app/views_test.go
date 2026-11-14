package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/WhitenWhiten/topophany/internal/preferences"
)

func TestNamedViewsPersistAndOpenWithoutCredentials(t *testing.T) {
	dir := t.TempDir()
	registry := filepath.Join(dir, "views.json")
	prefs := filepath.Join(dir, "preferences.json")
	base := []string{"--views-file", registry, "--preferences", prefs}
	args := append([]string{"views", "save", "trouble", "--page", "incidents", "--related-incidents", "--filter", "target"}, base...)
	out, _, err := invoke("", args...)
	if err != nil {
		t.Fatal(err, out)
	}
	var views preferences.Views
	if json.Unmarshal([]byte(out), &views) != nil || len(views.Items) != 1 || !views.Items[0].Related || views.Items[0].Search != "target" {
		t.Fatal(out)
	}
	// A view of the network keeps no mode, so older binaries, which refuse
	// a field they do not know, still read the file; products is kept.
	if raw, err := os.ReadFile(registry); err != nil || strings.Contains(string(raw), `"mode"`) {
		t.Fatalf("the network mode is written down: %s %v", raw, err)
	}
	if out, _, err = invoke("", append([]string{"views", "save", "shipped", "--mode", "products"}, base...)...); err != nil {
		t.Fatal(err, out)
	}
	if raw, _ := os.ReadFile(registry); !strings.Contains(string(raw), `"mode": "products"`) {
		t.Fatalf("a products view is not kept: %s", raw)
	}
	open := append([]string{"views", "open", "trouble", "--preview"}, base...)
	open = append(open, "--", "--json", "--preferences", prefs)
	if _, _, err = invoke("", open...); err != nil {
		t.Fatal(err)
	}
	if _, _, err = invoke("", append([]string{"views", "remove", "trouble"}, base...)...); err != nil {
		t.Fatal(err)
	}
	if _, _, err = invoke("", open...); err == nil {
		t.Fatal("removed view opened")
	}
}
