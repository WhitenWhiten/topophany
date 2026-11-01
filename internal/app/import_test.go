package app

import (
	"github.com/WhitenWhiten/topophany/internal/config"
	"os"
	"path/filepath"
	"testing"
)

func TestImportDoesNotOverwrite(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "ssh_config")
	output := filepath.Join(dir, "candidate.json")
	if err := os.WriteFile(input, []byte("Host server\n HostName 192.0.2.1\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := invoke("", "import", "--format", "ssh", "--input", input, "--output", output); err != nil {
		t.Fatal(err)
	}
	c, err := config.Load(output)
	if err != nil || len(c.Checks) != 1 || !c.Checks[0].Disabled {
		t.Fatal(c, err)
	}
	before, _ := os.ReadFile(output)
	if _, _, err := invoke("", "import", "--format", "ssh", "--input", input, "--output", output); err == nil {
		t.Fatal("overwrite allowed")
	}
	after, _ := os.ReadFile(output)
	if string(before) != string(after) {
		t.Fatal("existing candidate changed")
	}
}
