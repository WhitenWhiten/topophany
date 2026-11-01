package tui

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/WhitenWhiten/topophany/internal/config"
	"github.com/WhitenWhiten/topophany/internal/testkit"
)

func TestEditorRefusesExternalChangesAndKeepsDraft(t *testing.T) {
	path := filepath.Join(t.TempDir(), "topology.json")
	c := testkit.Config()
	data, _ := json.Marshal(c)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	a, err := NewEditor(path, c)
	if err != nil {
		t.Fatal(err)
	}
	b, err := NewEditor(path, c)
	if err != nil {
		t.Fatal(err)
	}
	a.Config.Nodes[0].Name = "First editor"
	if _, err = a.Save(); err != nil {
		t.Fatal(err)
	}
	b.Config.Nodes[0].Name = "Unsaved second editor"
	if _, err = b.Save(); !errors.Is(err, config.ErrFileChanged) {
		t.Fatal("lost update not refused", err)
	}
	disk, err := config.Load(path)
	if err != nil || disk.Nodes[0].Name != "First editor" {
		t.Fatal("overwrote external edit")
	}
	if b.Config.Nodes[0].Name != "Unsaved second editor" {
		t.Fatal("draft lost")
	}
	if _, err = a.Save(); err != nil {
		t.Fatal("repeat save failed", err)
	}
	// Formatting-only edits must also be noticed.
	bytes, _ := os.ReadFile(path)
	os.WriteFile(path, append(bytes, '\n'), 0600)
	if _, err = a.Save(); !errors.Is(err, config.ErrFileChanged) {
		t.Fatal("format edit lost", err)
	}
}
func TestEditorRefusesFileCreatedAfterOpening(t *testing.T) {
	path := filepath.Join(t.TempDir(), "new.json")
	editor, err := NewEditor(path, testkit.Config())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("external content"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = editor.Save(); !errors.Is(err, config.ErrFileChanged) {
		t.Fatal(err)
	}
}
