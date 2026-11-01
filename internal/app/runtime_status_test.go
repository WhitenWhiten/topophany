package app

import (
	"github.com/WhitenWhiten/topophany/internal/testkit"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRuntimeRevisionTracksReloadAndExpires(t *testing.T) {
	dir := t.TempDir()
	c := testkit.Config()
	path := filepath.Join(dir, "topology.json")
	saveConfig(t, path, c)
	p := newRuntimePublisher(dir, "worker", "home", c, nil)
	defer p.Close()
	if _, _, err := invoke("", "runtime", "--data", dir, "--config", path, "--json"); err != nil {
		t.Fatal(err)
	}
	c.Nodes[0].Name = "Edited but not reloaded"
	saveConfig(t, path, c)
	if _, _, err := invoke("", "runtime", "--data", dir, "--config", path); exitCode(err) != 2 {
		t.Fatal("unapplied edit matched runtime", err)
	}
	p.Set(c)
	if _, _, err := invoke("", "runtime", "--data", dir, "--config", path); err != nil {
		t.Fatal(err)
	}
	if _, err := at(time.Now().Add(time.Minute), "runtime", "--data", dir, "--json"); exitCode(err) != 3 {
		t.Fatal("expired lease remained current", err)
	}
	p.Close()
	p.Tick(time.Now().Add(time.Minute))
	if _, err := os.Stat(filepath.Join(dir, "local-runtime.json")); !os.IsNotExist(err) {
		t.Fatal("closed publisher recreated file")
	}
}
