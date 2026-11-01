package app

import (
	"encoding/json"
	"github.com/WhitenWhiten/topophany/internal/preferences"
	"path/filepath"
	"testing"
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
